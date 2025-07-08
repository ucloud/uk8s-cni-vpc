// Copyright UCloud. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
// https://www.apache.org/licenses/LICENSE-2.0
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package ipamd

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1beta1 "github.com/ucloud/uk8s-cni-vpc/kubernetes/apis/vipcontroller/v1beta1"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	v1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

const (
	STS_ENABLE_STATIC_IP  = "network.beta.kubernetes.io/ucloud-statefulset-static-ip"
	STS_STATIC_IP_RELEASE = "network.beta.kubernetes.io/ucloud-statefulset-ip-claim-policy"
)

func (s *ipamServer) createVpcIpClaim(vip *v1beta1.VpcIpClaim) (*v1beta1.VpcIpClaim, error) {
	return s.crdClient.VipcontrollerV1beta1().VpcIpClaims(vip.Namespace).Create(context.TODO(), vip, metav1.CreateOptions{})
}

func (s *ipamServer) getVpcipClaim(podNS, podName string) (*v1beta1.VpcIpClaim, error) {
	return s.crdClient.VipcontrollerV1beta1().VpcIpClaims(podNS).Get(context.TODO(), podName, metav1.GetOptions{})
}

func (s *ipamServer) markVPCIpClaimAttached(req *rpc.AddPodNetworkRequest, vip *v1beta1.VpcIpClaim, sandboxId string) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		vip, localErr := s.crdClient.VipcontrollerV1beta1().VpcIpClaims(vip.Namespace).Get(context.TODO(), vip.Name, metav1.GetOptions{})
		if localErr != nil {
			ulog.Infof("Get latest vpcipclaim %s/%s error: %v", vip.Namespace, vip.Name, localErr)
			return localErr
		}

		if vip.Labels != nil {
			vip.Labels["attached"] = "true"
		} else {
			vip.Labels = map[string]string{"attached": "true"}
		}
		// Restore finalizers
		vip.Finalizers = []string{"cni-vpc-ipamd"}
		vip.Status.Attached = true
		vip.Status.ReleaseTime = "Never"
		vip.Status.SandboxId = sandboxId
		vip.Status.Mac = req.MacAddress
		vip.Status.Zone = s.zoneId
		// Update release time
		if pod, err := s.getPod(vip.Name, vip.Namespace); err == nil {
			if val, found := pod.Annotations[STS_STATIC_IP_RELEASE]; found {
				vip.Status.ReleaseTime = val
			}
		} else {
			ulog.Warnf("Cannot get pod %s/%s, %v", vip.Name, vip.Namespace, err)
		}

		_, localErr = s.crdClient.VipcontrollerV1beta1().VpcIpClaims(vip.Namespace).Update(context.TODO(), vip, metav1.UpdateOptions{})
		return localErr
	})
	if err != nil {
		ulog.Errorf("Mark crd vpcipclaim %s/%s as attached error: %v", vip.Namespace, vip.Name, err)
	}
	return err
}

func (s *ipamServer) findStaticIp(podNS, podName string) (*v1beta1.VpcIpClaim, error) {
	if vip, err := s.getVpcipClaim(podNS, podName); err == nil {
		return vip, nil
	} else if k8serr.IsNotFound(err) {
		ulog.Warnf("vpcipclaim for %s/%s not exists, need to create a new one", podNS, podName)
		return nil, nil
	} else {
		ulog.Errorf("Get vpcipclaim for %s/%s failed, %v", podNS, podName, err)
		return nil, err
	}
}

func getOwnerStatefulSetName(pod *v1.Pod) string {
	for _, of := range pod.OwnerReferences {
		if of.Kind == "StatefulSet" {
			return of.Name
		}
	}
	return ""
}

func (s *ipamServer) createVpcIpClaimByPodNetwork(req *rpc.AddPodNetworkRequest, pn *rpc.PodNetwork, pod *v1.Pod) (*v1beta1.VpcIpClaim, error) {
	vip := PodNetworkToVip(pn)
	vip.Labels = make(map[string]string)
	ownerSts := getOwnerStatefulSetName(pod)
	if len(ownerSts) > 0 {
		vip.Labels["owner-statefulset"] = ownerSts
	} else {
		ulog.Warnf("Cannot find owner statefulset of %v/%v", pod.Namespace, pod.Name)
	}

	vpcclaim, err := s.createVpcIpClaim(vip)
	if err != nil {
		return nil, err
	}
	nvip, err := s.localAttach(req, vpcclaim, vip.Status.SandboxId)
	if err != nil {
		return nil, err
	}
	return nvip, nil
}

func (s *ipamServer) localAttach(req *rpc.AddPodNetworkRequest, vip *v1beta1.VpcIpClaim, sandboxID string) (*v1beta1.VpcIpClaim, error) {
	if vip == nil {
		return nil, errors.New("vpcipclaim object is nil")
	}
	// Find previous mac addr, double check by api call DescribeSecondaryIp
	prevMac := vip.Status.Mac
	if vpcIp, err := s.uapiDescribeSecondaryIp(vip.Spec.Ip, vip.Spec.SubnetId); err == nil {
		if vpcIp == nil {
			//secondary ip has been deleted, so secondary ip should be specify to allocate
			vpcIp, err = s.uapiAllocateSpecifiedSecondaryIp(vip.Spec.Ip, req)
			if err != nil {
				return nil, fmt.Errorf("failed to allocate specify secondary ip %s in subnet %s err:%v", vip.Spec.Ip, vip.Spec.SubnetId, err)
			}
			ulog.Infof("Allocate specified secondary ip %s in subnet %s", vip.Spec.Ip, vip.Spec.SubnetId)
		}
		prevMac = vpcIp.Mac
	} else {
		ulog.Warnf("API DescribeSecondaryIp %s of %s/%s failed, %v", vip.Spec.Ip, vip.Namespace, vip.Name, err)
	}

	// Update ip <--> mac by api call MoveSecondaryIPMac
	if prevMac != req.MacAddress {
		vip.Status.Mac = req.MacAddress
		if err := s.uapiMoveSecondaryIPMac(&rpc.PodNetwork{
			VPCIP:      vip.Spec.Ip,
			MacAddress: prevMac,
			SubnetID:   vip.Spec.SubnetId,
		}, req.MacAddress); err != nil {
			ulog.Errorf("Move secondary ip %s in subnet %s from %s to %s error: %v", vip.Spec.Ip, vip.Spec.SubnetId, prevMac, req.MacAddress, err)
			return nil, fmt.Errorf("failed to move secondary ip %s in subnet %s from %s to %s, %v", vip.Spec.Ip, vip.Spec.SubnetId, prevMac, req.MacAddress, err)
		}
		ulog.Infof("Move secondary ip %s's mac in subnet %s from %s to %s", vip.Spec.Ip, vip.Spec.SubnetId, prevMac, req.MacAddress)
	}

	if err := s.markVPCIpClaimAttached(req, vip, sandboxID); err != nil {
		return nil, err
	}

	return s.getVpcipClaim(vip.Namespace, vip.Name)
}

func VipToPodNetwork(vip *v1beta1.VpcIpClaim) *rpc.PodNetwork {
	if vip == nil {
		return nil
	}
	pn := &rpc.PodNetwork{
		CreateTime:   time.Now().Unix(),
		VPCIP:        vip.Spec.Ip,
		VPCID:        vip.Spec.VpcId,
		SubnetID:     vip.Spec.SubnetId,
		Mask:         vip.Spec.Mask,
		MacAddress:   vip.Status.Mac,
		Gateway:      vip.Spec.Gateway,
		PodName:      vip.Name,
		PodNS:        vip.Namespace,
		DedicatedUNI: false,
	}

	return pn
}

func PodNetworkToVip(pn *rpc.PodNetwork) *v1beta1.VpcIpClaim {
	if pn == nil {
		return nil
	}
	vip := &v1beta1.VpcIpClaim{
		ObjectMeta: metav1.ObjectMeta{
			Finalizers: []string{"cni-vpc-ipamd"},
		},
		Spec: v1beta1.VpcIpClaimSpec{
			Ip:       pn.VPCIP,
			Gateway:  pn.Gateway,
			Mask:     pn.Mask,
			VpcId:    pn.VPCID,
			SubnetId: pn.SubnetID,
		},
		Status: v1beta1.VpcIpClaimStatus{
			Attached:       true,
			LastDetachTime: time.Now().Format("2006-01-02 15:04:05"),
			ReleaseTime:    "Never",
			Mac:            pn.MacAddress,
		},
	}
	vip.Kind = "vpcIpClaim"
	vip.Name = pn.PodName
	vip.Namespace = pn.PodNS
	return vip
}
