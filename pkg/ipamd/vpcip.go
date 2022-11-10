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
	"time"

	"github.com/j-keck/arping"

	"github.com/ucloud/ucloud-sdk-go/services/vpc"
	"github.com/ucloud/uk8s-cni-vpc/pkg/rpc"
	"github.com/ucloud/uk8s-cni-vpc/pkg/storage"

	v1 "k8s.io/api/core/v1"
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
	Reciever chan *InnerAddPodNetworkResponse
}

type InnerAddPodNetworkResponse struct {
	PodNetwork *rpc.PodNetwork
	Err        error
}

type InnerDelPodNetworkRequest struct {
	Req      *rpc.DelPodNetworkRequest
	Reciever chan error
}

var chanAddPodIp = make(chan *InnerAddPodNetworkRequest, 0)
var chanDelPodIp = make(chan *InnerDelPodNetworkRequest, 0)
var chanStopLoop = make(chan bool, 0)

func getReservedIPKey(pNet *rpc.PodNetwork) string {
	return pNet.VPCID + "-" + pNet.VPCIP
}

func (s *ipamServer) staticIpPodExists(pNet *rpc.PodNetwork) bool {
	if _, err := s.getVpcipClaim(pNet.GetPodNS(), pNet.GetPodName()); err != nil {
		klog.Warningf("%v/%v can not found, %v", pNet.GetPodNS(), pNet.GetPodName(), err)
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

			size = s.pool.Len()
			if size == 0 && !s.unschedulable {
				// Even after pool watermark scheduling, the pool is still empty. It means that
				// the upstream VPC server is out of IP, or we have problem communicating with
				// VPC server.
				// In either cases, the Pod should not be scheduled to this Node anymore. We should
				// mark this node as unschedulable in k8s.
				err := s.markNodeUnschedulable()
				if err != nil {
					klog.Errorf("failed to mark node %q as unschedulable: %v", s.nodeName, err)
				} else {
					klog.Infof("the node %q is out of ip, marked it as unschedulable", s.nodeName)
				}
			}
			if size > 0 && s.unschedulable {
				// The Node was marked as unschedulable, but now we have available IP(s), it is
				// safe to mark it as schedulable in k8s again.
				err := s.markNodeSchedulable()
				if err != nil {
					klog.Errorf("failed to mark node %q as schedulable: %v", s.nodeName, err)
				} else {
					klog.Infof("the node %q ip pool has been recovered, marked it as schedulable", s.nodeName)
				}
			}
		case r := <-chanAddPodIp:
			pNet, err := s.getPodIp(r.Req)
			r.Reciever <- &InnerAddPodNetworkResponse{
				PodNetwork: pNet,
				Err:        err,
			}
		case r := <-chanDelPodIp:
			pNet := r.Req.GetPodNetwork()
			if s.staticIpPodExists(pNet) {
				// Nothing to do for static ip
				r.Reciever <- nil
				continue
			}
			// After the Pod releases the IP, set a cooldown time for the IP, and put it back into
			// the IP pool after the IP cools down.
			// The cooldown time is to prevent the IP from being assigned to the next pod before the
			// kubelet deleting the pod.
			s.cooldownIP(pNet)
			r.Reciever <- nil

		case <-cooldownTk:
			// Recycle the cooldown IP to pool
			s.recycleCooldownIP()

		case <-chanStopLoop:
			klog.Infof("Now stop vpc ip pool manager loop")
			return
		}
	}
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

func (s *ipamServer) markNodeUnschedulable() error {
	node, err := s.getKubeNode(s.nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node %q: %v", s.nodeName, err)
	}

	var found bool
	for _, taint := range node.Spec.Taints {
		if taint.Key == outOfIpTaintKey {
			found = true
			break
		}
	}
	if found {
		s.unschedulable = true
		return nil
	}

	node.Spec.Taints = append(node.Spec.Taints, v1.Taint{
		Key: outOfIpTaintKey,

		// The Effect must be NoSchedule to prevent new Pod being scheduled to this node.
		// And we cannot evict the existsing Pod(s). So the effect cannot be NoExecute.
		Effect: v1.TaintEffectNoSchedule,
	})
	_, err = s.k8s.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node %q: %v", s.nodeName, err)
	}
	s.unschedulable = true
	return nil
}

func (s *ipamServer) markNodeSchedulable() error {
	node, err := s.getKubeNode(s.nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node %q: %v", s.nodeName, err)
	}

	var found bool
	newTaints := make([]v1.Taint, 0)
	for _, taint := range node.Spec.Taints {
		if taint.Key == outOfIpTaintKey {
			// We need to remove the out-of-ip taint
			found = true
			continue
		}
		newTaints = append(newTaints, taint)
	}
	if !found {
		s.unschedulable = false
		return nil
	}

	node.Spec.Taints = newTaints
	_, err = s.k8s.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node %q: %v", s.nodeName, err)
	}
	s.unschedulable = false
	return nil
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
