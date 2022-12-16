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

	v1beta1 "github.com/ucloud/uk8s-cni-vpc/apis/vipcontroller/v1beta1"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	v1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
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

func (s *ipamServer) markVPCIpClaimAttached(vip *v1beta1.VpcIpClaim, sandboxId string) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		vip, localErr := s.crdClient.VipcontrollerV1beta1().VpcIpClaims(vip.Namespace).Get(context.TODO(), vip.Name, metav1.GetOptions{})
		if localErr != nil {
			klog.Infof("cannot get latest vpcipclaim  %s/%s, %v", vip.Namespace, vip.Name, localErr)
			return localErr
		}

		if vip.Labels != nil {
			vip.Labels["attached"] = "true"
		} else {
			vip.Labels = map[string]string{"attached": "true"}
		}
		// Restore finalizers
		vip.ObjectMeta.Finalizers = []string{"cni-vpc-ipamd"}
		vip.Status.Attached = true
		vip.Status.ReleaseTime = "Never"
		vip.Status.SandboxId = sandboxId
		vip.Status.Mac = s.hostMacAddr
		vip.Status.Zone = s.zoneId
		// Update release time
		if pod, err := s.getPod(vip.Name, vip.Namespace); err == nil {
			if val, found := pod.Annotations[STS_STATIC_IP_RELEASE]; found {
				vip.Status.ReleaseTime = val
			}
		} else {
			klog.Warningf("Cannot get pod %s/%s, %v", vip.Name, vip.Namespace, err)
		}

		_, localErr = s.crdClient.VipcontrollerV1beta1().VpcIpClaims(vip.Namespace).Update(context.TODO(), vip, metav1.UpdateOptions{})
		return localErr
	})
	if err != nil {
		klog.Errorf("Mark crd vpcipclaim %s/%s as attached failed, %v", vip.Namespace, vip.Name, err)
	}
	return err
}

func (s *ipamServer) findStaticIp(podNS, podName string) (*v1beta1.VpcIpClaim, error) {
	if vip, err := s.getVpcipClaim(podNS, podName); err == nil {
		return vip, nil
	} else if k8serr.IsNotFound(err) {
		klog.Warningf("vpcipclaim for %s/%s not exists, need to create a new one", podNS, podName)
		return nil, nil
	} else {
		klog.Errorf("Get vpcipclaim for %s/%s failed, %v", podNS, podName, err)
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

func (s *ipamServer) createVpcIpClaimByPodNetwork(pNet *rpc.PodNetwork, pod *v1.Pod) (*v1beta1.VpcIpClaim, error) {
	vip := PodNetworkToVip(pNet)
	vip.Labels = make(map[string]string)
	ownerSts := getOwnerStatefulSetName(pod)
	if len(ownerSts) > 0 {
		vip.Labels["owner-statefulset"] = ownerSts
	} else {
		klog.Warningf("Cannot find owner statefulset of %v/%v", pod.Namespace, pod.Name)
	}

	vpcclaim, err := s.createVpcIpClaim(vip)
	if err != nil {
		return nil, err
	}
	nvip, err := s.localAttach(vpcclaim, pNet.SandboxID)
	if err != nil {
		return nil, err
	}
	return nvip, nil
}

func (s *ipamServer) localAttach(vip *v1beta1.VpcIpClaim, sandboxId string) (*v1beta1.VpcIpClaim, error) {
	if vip == nil {
		return nil, errors.New("vpcipclaim object is nil")
	}
	// Find previous mac addr, double check by api call DescribeSecondaryIp
	prevMac := vip.Status.Mac
	if vpcIp, err := s.uapiDescribeSecondaryIp(vip.Spec.Ip, vip.Spec.SubnetId); err == nil {
		if vpcIp == nil {
			//secondary ip has been deleted, so secondary ip should be specify to allocate
			vpcIp, err = s.uapiAllocateSpecifiedSecondaryIp(vip.Spec.Ip, vip.Spec.SubnetId)
			if err != nil {
				return nil, fmt.Errorf("failed to allocate specify secondary ip %s in subnet %s err:%v", vip.Spec.Ip, vip.Spec.SubnetId, err)
			}
			klog.Infof("success to allocate specified secondary ip %s in subnet %s", vip.Spec.Ip, vip.Spec.SubnetId)
		}
		prevMac = vpcIp.Mac
	} else {
		klog.Warningf("API DescribeSecondaryIp %s of %s/%s failed, %v", vip.Spec.Ip, vip.Namespace, vip.Name, err)
	}

	// Update ip <--> mac by api call MoveSecondaryIPMac
	if prevMac != s.hostMacAddr {
		vip.Status.Mac = s.hostMacAddr
		if err := s.uapiMoveSecondaryIPMac(vip.Spec.Ip, prevMac, s.hostMacAddr, vip.Spec.SubnetId); err != nil {
			klog.Errorf("Failed to move secondary ip %s in subnet %s from %s to %s, %v", vip.Spec.Ip, vip.Spec.SubnetId, prevMac, s.hostMacAddr, err)
			return nil, fmt.Errorf("failed to move secondary ip %s in subnet %s from %s to %s, %v", vip.Spec.Ip, vip.Spec.SubnetId, prevMac, s.hostMacAddr, err)
		}
		klog.Infof("Finished moving secondary ip %s's mac in subnet %s from %s to %s", vip.Spec.Ip, vip.Spec.SubnetId, prevMac, s.hostMacAddr)
	}

	if err := s.markVPCIpClaimAttached(vip, sandboxId); err != nil {
		return nil, err
	}

	return s.getVpcipClaim(vip.Namespace, vip.Name)
}

func VipToPodNetwork(vip *v1beta1.VpcIpClaim) *rpc.PodNetwork {
	if vip == nil {
		return nil
	}
	pNet := &rpc.PodNetwork{
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

	return pNet
}

func PodNetworkToVip(pNet *rpc.PodNetwork) *v1beta1.VpcIpClaim {
	if pNet == nil {
		return nil
	}
	vip := &v1beta1.VpcIpClaim{
		ObjectMeta: metav1.ObjectMeta{
			Finalizers: []string{"cni-vpc-ipamd"},
		},
		Spec: v1beta1.VpcIpClaimSpec{
			Ip:       pNet.VPCIP,
			Gateway:  pNet.Gateway,
			Mask:     pNet.Mask,
			VpcId:    pNet.VPCID,
			SubnetId: pNet.SubnetID,
		},
		Status: v1beta1.VpcIpClaimStatus{
			Attached:       true,
			LastDetachTime: time.Now().Format("2006-01-02 15:04:05"),
			ReleaseTime:    "Never",
			Mac:            pNet.MacAddress,
		},
	}
	vip.Kind = "vpcIpClaim"
	vip.Name = pNet.PodName
	vip.Namespace = pNet.PodNS
	return vip
}
