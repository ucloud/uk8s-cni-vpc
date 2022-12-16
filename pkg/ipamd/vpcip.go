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
	"flag"
	"fmt"
	"net"
	"reflect"
	"time"

	"github.com/j-keck/arping"

	"github.com/ucloud/ucloud-sdk-go/services/vpc"
	"github.com/ucloud/uk8s-cni-vpc/pkg/storage"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	ipamdv1beta1 "github.com/ucloud/uk8s-cni-vpc/apis/ipamd/v1beta1"

	v1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

var (
	AvailablePodIPLowWatermark  int   = 3
	AvailablePodIPHighWatermark int   = 50
	CalicoPolicyFlag            bool  = false
	CooldownPeriodSeconds       int64 = 30
)

func init() {
	flag.IntVar(&AvailablePodIPLowWatermark, "availablePodIPLowWatermark", 3, "low watermark number of allocated vpc ip to be assigned to new pods")
	flag.IntVar(&AvailablePodIPHighWatermark, "availablePodIPHighWatermark", 50, "high watermark number of allocated vpc ip to be assigned to new pods")
	flag.BoolVar(&CalicoPolicyFlag, "calicoPolicyFlag", false, "enable ipamd to set ip to the pod annotation, calico can get the ip from annotation")
	flag.Int64Var(&CooldownPeriodSeconds, "cooldownPeriodSeconds", 30, "period seconds to cooldown a deleted ip")
}

const (
	outOfIpTaintKey = "ipamd-pool-out-of-ip"
)

type cooldownIPItem struct {
	vpcIP *rpc.PodNetwork

	cooldownOver int64
}

type InnerAddPodNetworkRequest struct {
	Req      *rpc.AddPodNetworkRequest
	Receiver chan *InnerAddPodNetworkResponse
}

type InnerAddPodNetworkResponse struct {
	PodNetwork *rpc.PodNetwork
	Err        error
}

type InnerDelPodNetworkRequest struct {
	Req      *rpc.DelPodNetworkRequest
	Receiver chan error
}

var chanAddPodIp = make(chan *InnerAddPodNetworkRequest, 0)
var chanDelPodIp = make(chan *InnerDelPodNetworkRequest, 0)
var chanStopLoop = make(chan bool, 0)

func getReservedIPKey(pNet *rpc.PodNetwork) string {
	return pNet.VPCID + "-" + pNet.VPCIP
}

func (s *ipamServer) staticIpPodExists(pNet *rpc.PodNetwork) bool {
	if _, err := s.getVpcipClaim(pNet.GetPodNS(), pNet.GetPodName()); err != nil {
		return false
	}
	return true
}

func (s *ipamServer) assignStaticPodIP(pod *v1.Pod, sandboxID string) (*rpc.PodNetwork, error) {
	if vip, err := s.findStaticIp(pod.Namespace, pod.Name); err != nil {
		return nil, err
	} else {
		if vip != nil {
			if vip, err = s.localAttach(vip, sandboxID); err != nil {
				return nil, err
			}
			return VipToPodNetwork(vip), nil
		}
	}

	if nvip, err := s.assignPodIP(); err == nil {
		nvip.CreateTime = time.Now().Unix()
		nvip.PodName = pod.Name
		nvip.PodNS = pod.Namespace
		nvip.SandboxID = sandboxID
		_, err = s.createVpcIpClaimByPodNetwork(nvip, pod)
		if err != nil {
			klog.Infof("Save static ip %v to crd failed, %v", nvip, err)
			return nil, err
		}
		return nvip, nil
	}

	return nil, fmt.Errorf("can not get static ip for %s/%s", pod.Namespace, pod.Name)
}

func (s *ipamServer) getPodIp(r *rpc.AddPodNetworkRequest) (*rpc.PodNetwork, error) {
	podName := r.GetPodName()
	podNS := r.GetPodNamespace()
	sandboxId := r.GetSandboxID()
	enable, pod, err := s.podEnableStaticIP(podName, podNS)
	if err != nil {
		return nil, err
	}
	var pn *rpc.PodNetwork
	if enable {
		pn, err = s.assignStaticPodIP(pod, sandboxId)
	} else {
		pn, err = s.assignPodIP()
	}
	if err != nil {
		return nil, err
	}
	if !pn.Recycled && pn.VPCIP != "" {
		// We need to detect IP conflict before using it.
		// See: https://www.rfc-editor.org/rfc/rfc5227
		err = s.checkIPConflict(pn.VPCIP)
		if err != nil {
			klog.Errorf("failed to detect ip conflict for %s: %v, we will release it", pn.VPCIP, err)
			err = s.uapiDeleteSecondaryIp(pn.VPCIP)
			if err != nil {
				klog.Errorf("failed to release ip %s after conflict: %v", pn.VPCIP, err)
				return nil, err
			}
			return nil, err
		}
	} else {
		klog.Infof("ip %s is recycled, no need to detect conflict", pn.VPCIP)
	}
	return pn, nil
}

func (s *ipamServer) assignPodIP() (*rpc.PodNetwork, error) {
	val, err := s.pool.Pop()
	if err == storage.ErrNotFound {
		// The pool is empty, try to assign a new one from VPC backend
		vpcIps, err := s.uapiAllocateSecondaryIp(1)
		if err != nil {
			klog.Errorf("failed to assign new ip for pod: %v", err)
			return nil, err
		}
		if len(vpcIps) == 0 {
			err = errors.New("empty vpcIPs returned by server")
			klog.Error(err)
			return nil, err
		}

		ip := vpcIps[0]
		klog.Infof("pool empty, allocated a new ip %q for pod", ip.Ip)
		return convertIpToPodNetwork(ip), nil
	}
	if err != nil {
		klog.Errorf("failed to pop vpc ip from pool: %v", err)
		return nil, err
	}

	return val, nil
}

func sendGratuitousArp(vpcip string) {
	// Send a gratuitous arp, using Host's primary interface hardware address
	for i := 0; i <= 2; i++ {
		err := arping.GratuitousArpOverIfaceByName(net.ParseIP(vpcip), getMasterInterface())
		if err != nil {
			klog.Errorf("send gratuitous arp error:%v", err)
		}
		if i != 2 {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func convertIpToPodNetwork(ip *vpc.IpInfo) *rpc.PodNetwork {
	if ip == nil {
		return nil
	}
	return &rpc.PodNetwork{
		VPCIP:        ip.Ip,
		VPCID:        ip.VPCId,
		SubnetID:     ip.SubnetId,
		Mask:         ip.Mask,
		MacAddress:   ip.Mac,
		Gateway:      ip.Gateway,
		DedicatedUNI: false,
		CreateTime:   time.Now().Unix(),
	}
}

func (s *ipamServer) ipPoolWatermarkManager() {
	klog.Infof("Start ip pool watermark manager loop")
	tk := time.Tick(10 * time.Second)
	cooldownTk := time.Tick(time.Second * time.Duration(CooldownPeriodSeconds))
	healthSize := 0
	for {
		select {
		case <-tk:
			size := s.pool.Len()
			switch {
			case size < AvailablePodIPLowWatermark:
				// The pool is below the low watermark and we need to expand the pool
				s.appendPool(size)

			case size > AvailablePodIPHighWatermark:
				// The pool is above the high watermark and we need to release the pool
				s.releasePool(size)

			default:
				// The pool is at normal watermark
				if healthSize != size {
					klog.Infof("pool size %d, healthy", size)
					healthSize = size
				}
			}

		case r := <-chanAddPodIp:
			pNet, err := s.getPodIp(r.Req)
			r.Receiver <- &InnerAddPodNetworkResponse{
				PodNetwork: pNet,
				Err:        err,
			}
		case r := <-chanDelPodIp:
			pNet := r.Req.GetPodNetwork()
			if s.staticIpPodExists(pNet) {
				// Nothing to do for static ip
				r.Receiver <- nil
				continue
			}
			// After the Pod releases the IP, set a cooldown time for the IP, and put it back into
			// the IP pool after the IP cools down.
			// The cooldown time is to prevent the IP from being assigned to the next pod before the
			// kubelet deleting the pod.
			s.cooldownIP(pNet)
			r.Receiver <- nil

		case <-cooldownTk:
			// Recycle the cooldown IP to pool
			s.recycleCooldownIP()

		case <-chanStopLoop:
			klog.Infof("Now stop vpc ip pool manager loop")
			return
		}

		// Update the ipamd CR resource, save the latest watermark to the status.
		err := s.updateStatus()
		if err != nil {
			klog.Errorf("failed to update ipamd status: %v", err)
		}
	}
}

func (s *ipamServer) updateStatus() error {
	watermark := s.pool.Len()
	status := ipamdv1beta1.IpamdStatus{
		Current: watermark,

		High: AvailablePodIPHighWatermark,
		Low:  AvailablePodIPLowWatermark,
	}

	switch {
	case watermark <= 0:
		status.Status = ipamdv1beta1.StatusDry

	case watermark < AvailablePodIPLowWatermark:
		status.Status = ipamdv1beta1.StatusHungry

	case watermark >= AvailablePodIPLowWatermark && watermark < AvailablePodIPHighWatermark:
		status.Status = ipamdv1beta1.StatusNormal

	default:
		status.Status = ipamdv1beta1.StatusOverflow
	}

	ctx := context.Background()
	ipamdInfo, err := s.crdClient.IpamdV1beta1().Ipamds("kube-system").Get(ctx, s.nodeName, metav1.GetOptions{})
	switch {
	case kerrors.IsNotFound(err):
		ipamdInfo = &ipamdv1beta1.Ipamd{
			ObjectMeta: metav1.ObjectMeta{
				Name: s.nodeName,
			},
			Spec: ipamdv1beta1.IpamdSpec{
				Node: s.nodeName,
				Addr: s.tcpAddr,
			},
			Status: status,
		}
		_, err = s.crdClient.IpamdV1beta1().Ipamds("kube-system").Create(ctx, ipamdInfo, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("faield to create ipamd resource: %v", err)
		}
		klog.Infof("create ipamd resource %q, status: %+v", s.nodeName, status)
		return nil

	case err != nil:
		return fmt.Errorf("failed to get ipamd resource: %v", err)
	}

	if reflect.DeepEqual(ipamdInfo.Status, status) {
		return nil
	}

	ipamdInfo.Status = status
	_, err = s.crdClient.IpamdV1beta1().Ipamds("kube-system").Update(ctx, ipamdInfo, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ipamd resource: %v", err)
	}

	klog.Infof("update ipamd resource %q, status: %+v", s.nodeName, status)
	return nil
}

func (s *ipamServer) appendPool(size int) {
	toAssign := AvailablePodIPLowWatermark - size
	if toAssign <= 0 {
		return
	}
	klog.Infof("pool size %d, below low watermark %d, try to assign %d ip",
		size, AvailablePodIPLowWatermark, toAssign)
	vpcIps, err := s.uapiAllocateSecondaryIp(toAssign)
	if err != nil {
		klog.Errorf("failed to allocate ips: %v", err)
		return
	}
	for _, ip := range vpcIps {
		sendGratuitousArp(ip.Ip)
		pNet := convertIpToPodNetwork(ip)
		s.pool.Set(getReservedIPKey(pNet), pNet)
		klog.Infof("successfully allocated ip %s to pool", ip.Ip)
	}
}

func (s *ipamServer) releasePool(size int) {
	toRelease := size - AvailablePodIPHighWatermark
	klog.Infof("pool size %d, above high watermark %d, try to release %d ip",
		size, AvailablePodIPHighWatermark, toRelease)
	for i := 0; i < toRelease; i++ {
		ip, err := s.pool.Pop()
		if err != nil {
			// If the pool is empty here, it means that someone else had consumed all ip(s)
			// during the release process, we can safely interrupt in this case.
			if !errors.Is(err, storage.ErrNotFound) {
				klog.Errorf("release ip: failed to pop ip: %v", err)
			}
			return
		}
		err = s.uapiDeleteSecondaryIp(ip.VPCIP)
		if err != nil {
			klog.Errorf("failed to release secondary ip %s: %v", ip.VPCIP, err)
			// Failed to release the ip, we may have problem communicating with the VPC server.
			// Put the ip back to pool to let it have chance to be released in the next loop.
			err = s.pool.Set(getReservedIPKey(ip), ip)
			if err != nil {
				klog.Errorf("failed to put secondary ip %s back to pool after releasing failed: %v", ip.VPCIP, err)
			}
			return
		}
	}
}

func (s *ipamServer) cooldownIP(ip *rpc.PodNetwork) {
	s.cooldownLock.Lock()
	defer s.cooldownLock.Unlock()

	over := time.Now().Unix() + CooldownPeriodSeconds
	klog.Infof("put ip %s to cooldown set, it will be recycled on %d", ip.VPCIP, over)
	ip.RecycleTime = time.Now().Unix()
	ip.Recycled = true
	s.cooldownSet = append(s.cooldownSet, &cooldownIPItem{
		vpcIP: ip,

		cooldownOver: over,
	})
}

func (s *ipamServer) recycleCooldownIP() {
	s.cooldownLock.Lock()
	defer s.cooldownLock.Unlock()

	if len(s.cooldownSet) == 0 {
		return
	}

	now := time.Now().Unix()
	newSet := make([]*cooldownIPItem, 0, len(s.cooldownSet))
	for _, cdIP := range s.cooldownSet {
		if cdIP.cooldownOver >= now {
			err := s.pool.Set(getReservedIPKey(cdIP.vpcIP), cdIP.vpcIP)
			if err != nil {
				klog.Errorf("recycle cooldown ip %s failed: %v, it will be put back to cooldown queue", cdIP.vpcIP.VPCIP, err)
				newSet = append(newSet, cdIP)
				continue
			}
			klog.Infof("recycle cooldown ip %s to pool successfully", cdIP.vpcIP.VPCIP)
			continue
		}
		newSet = append(newSet, cdIP)
	}
	s.cooldownSet = newSet
}

func (s *ipamServer) doFreeIpPool() {
	var free int
	for {
		ip, err := s.pool.Pop()
		if err == storage.ErrNotFound {
			klog.Infof("Free vpc ip pool(size %d) before my death.", free)
			return
		}
		if err != nil {
			klog.Errorf("failed to pop ip from pool: %v", err)
			return
		}
		s.uapiDeleteSecondaryIp(ip.VPCIP)
		s.pool.Delete(getReservedIPKey(ip))
		free++
	}
}

func (s *ipamServer) doFreeCooldown() {
	s.cooldownLock.Lock()
	defer s.cooldownLock.Unlock()

	for _, cdIP := range s.cooldownSet {
		err := s.uapiDeleteSecondaryIp(cdIP.vpcIP.VPCIP)
		if err != nil {
			klog.Errorf("failed to delete cooldown ip %s: %v", cdIP.vpcIP.VPCIP, err)
			continue
		}
	}
	klog.Infof("Free vpc ip cooldown(size %d) before my death.", len(s.cooldownSet))
}
