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
	"sort"
	"time"

	"github.com/j-keck/arping"
	"google.golang.org/grpc"

	"github.com/ucloud/ucloud-sdk-go/services/vpc"
	ipamdv1beta1 "github.com/ucloud/uk8s-cni-vpc/kubernetes/apis/ipamd/v1beta1"
	"github.com/ucloud/uk8s-cni-vpc/pkg/database"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	v1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

type CooldownItem struct {
	Network *rpc.PodNetwork

	CooldownOver int64
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

var (
	chanAddPodIP = make(chan *InnerAddPodNetworkRequest, 0)
	chanDelPodIP = make(chan *InnerDelPodNetworkRequest, 0)
	chanStopLoop = make(chan bool, 0)
)

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
			ulog.Errorf("Save static ip %v to crd error: %v", nvip, err)
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
			ulog.Errorf("Detect ip conflict for %s error: %v, we will release it", pn.VPCIP, err)
			err = s.uapiDeleteSecondaryIp(pn.VPCIP)
			if err != nil {
				ulog.Errorf("Release ip %s after conflict error: %v", pn.VPCIP, err)
				return nil, err
			}
			return nil, err
		}
	} else {
		ulog.Infof("IP %s is recycled, no need to detect conflict", pn.VPCIP)
	}
	return pn, nil
}

func (s *ipamServer) assignPodIP() (*rpc.PodNetwork, error) {
	kv, err := s.poolDB.Pop()
	if database.IsEOF(err) {
		// The pool is empty, try to assign a new one from VPC backend
		vpcIps, err := s.uapiAllocateSecondaryIP(1)
		if err != nil {
			if err == ErrOutOfIP {
				// Borrowing: When the vpc has no ip, other ipamd pools may still
				// have surplus, so we try to choose one of them to borrow.
				// The borrowed ip will call the vpc's MoveSecondaryIPMac to migrate
				// MAC address, see:
				//   https://docs.ucloud.cn/api/vpc2.0-api/move_secondary_ip_mac
				ulog.Infof("Out of ip in VPC, try to borrow one from other ipamd")
				pn, err := s.borrowIP()
				if err != nil {
					return nil, fmt.Errorf("vpc out of ip, and failed to borrow from others: %v", err)
				}
				return pn, nil
			}
			ulog.Errorf("Assign new ip for pod error: %v", err)
			return nil, err
		}
		if len(vpcIps) == 0 {
			err = errors.New("empty vpcIPs returned by server")
			ulog.Errorf("Assign new ip for pod error: %v", err)
			return nil, err
		}

		ip := vpcIps[0]
		ulog.Infof("Pool empty, allocated a new ip %q for pod", ip.Ip)
		return convertIpToPodNetwork(ip), nil
	}
	if err != nil {
		ulog.Errorf("Pop vpc ip from pool error: %v", err)
		return nil, err
	}

	return kv.Value, nil
}

func sendGratuitousArp(vpcip string) {
	// Send a gratuitous arp, using Host's primary interface hardware address
	for i := 0; i <= 2; i++ {
		err := arping.GratuitousArpOverIfaceByName(net.ParseIP(vpcip), iputils.GetMasterInterface())
		if err != nil {
			ulog.Errorf("Send gratuitous arp error: %v", err)
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
	ulog.Infof("Start ip pool watermark manager loop")
	tk := time.Tick(10 * time.Second)
	cooldownTk := time.Tick(time.Second * time.Duration(CooldownPeriodSeconds))
	recycleStatusTk := time.Tick(10 * time.Minute)
	healthSize := 0
	checkIpHealthTk := time.Tick(10 * time.Minute)
	for {
		select {
		case <-tk:
			size, err := s.poolDB.Count()
			if err != nil {
				ulog.Errorf("Get pool size error: %v", err)
				continue
			}
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
					ulog.Infof("Pool size %d, healthy", size)
					healthSize = size
				}
			}

		case r := <-chanAddPodIP:
			pNet, err := s.getPodIp(r.Req)
			r.Receiver <- &InnerAddPodNetworkResponse{
				PodNetwork: pNet,
				Err:        err,
			}

		case r := <-chanDelPodIP:
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

		case <-checkIpHealthTk:
			err := s.checkIPHealth()
			if err != nil {
				ulog.Errorf("Check ip health error: %v", err)
			}

		case <-recycleStatusTk:
			err := s.recycleStatus()
			if err != nil {
				ulog.Errorf("Recycle ipamd status error: %v", err)
			}

		case <-chanStopLoop:
			ulog.Infof("Now stop vpc ip pool manager loop")
			return
		}

		// Update the ipamd CR resource, save the latest watermark to the status.
		err := s.updateStatus()
		if err != nil {
			ulog.Errorf("Update ipamd status error: %v", err)
		}
	}
}

func (s *ipamServer) updateStatus() error {
	watermark, err := s.poolDB.Count()
	if err != nil {
		return fmt.Errorf("Count pool size error: %v", err)
	}
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
				Node:   s.nodeName,
				Addr:   s.tcpAddr,
				Subnet: s.uapi.SubnetID(),
			},
			Status: status,
		}
		_, err = s.crdClient.IpamdV1beta1().Ipamds("kube-system").Create(ctx, ipamdInfo, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("faield to create ipamd resource: %v", err)
		}
		ulog.Infof("Create ipamd resource %q, status: %+v", s.nodeName, status)
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

	ulog.Infof("Update ipamd resource %q, status: %+v", s.nodeName, status)
	return nil
}

func (s *ipamServer) recycleStatus() error {
	ctx := context.Background()
	ipamdList, err := s.crdClient.IpamdV1beta1().Ipamds("kube-system").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list ipamd items error: %v", err)
	}

	nodeList, err := s.kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list node items error: %v", err)
	}

	nodeMap := make(map[string]struct{}, len(nodeList.Items))
	for _, node := range nodeList.Items {
		nodeMap[node.Name] = struct{}{}
	}

	for _, ipamd := range ipamdList.Items {
		nodeName := ipamd.Spec.Node
		if _, ok := nodeMap[nodeName]; ok {
			continue
		}

		err = s.crdClient.IpamdV1beta1().Ipamds("kube-system").Delete(ctx, ipamd.Name, metav1.DeleteOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				continue
			}
			ulog.Errorf("Delete unused ipamd %q error: %v", ipamd.Name, err)
			continue
		}
		ulog.Infof("Delete unused ipamd %s done", ipamd.Name)
	}

	return nil
}

func (s *ipamServer) appendPool(size int) {
	toAssign := AvailablePodIPLowWatermark - size
	if toAssign <= 0 {
		return
	}
	ulog.Infof("Pool size %d, below low watermark %d, try to assign %d ip",
		size, AvailablePodIPLowWatermark, toAssign)
	vpcIps, err := s.uapiAllocateSecondaryIP(toAssign)
	switch err {
	case ErrOutOfIP:
		ulog.Warnf("VPC is out of ip, pool size is not fulfilled")

	case nil:

	default:
		ulog.Errorf("Allocate ips error: %v", err)
	}
	for _, ip := range vpcIps {
		sendGratuitousArp(ip.Ip)
		pNet := convertIpToPodNetwork(ip)
		if s.putIpToPool(pNet) {
			ulog.Infof("Add ip %s to pool", ip.Ip)
		}
	}
}

func (s *ipamServer) releasePool(size int) {
	toRelease := size - AvailablePodIPHighWatermark
	ulog.Infof("Pool size %d, above high watermark %d, try to release %d ip",
		size, AvailablePodIPHighWatermark, toRelease)
	for i := 0; i < toRelease; i++ {
		kv, err := s.poolDB.Pop()
		if err != nil {
			// If the pool is empty here, it means that someone else had consumed all ip(s)
			// during the release process, we can safely interrupt in this case.
			if !database.IsEOF(err) {
				ulog.Errorf("Pop ip error: %v", err)
			}
			return
		}
		ip := kv.Value
		err = s.uapiDeleteSecondaryIp(ip.VPCIP)
		if err != nil {
			ulog.Errorf("Release secondary ip %s error: %v", ip.VPCIP, err)
			// Failed to release the ip, we may have problem communicating with the VPC server.
			// Put the ip back to pool to let it have chance to be released in the next loop.
			s.backupPushSecondaryIP(ip)
			return
		}
	}
}

func (s *ipamServer) cooldownIP(ip *rpc.PodNetwork) {
	over := time.Now().Unix() + CooldownPeriodSeconds
	ip.RecycleTime = time.Now().Unix()
	ip.Recycled = true
	cooldown := &CooldownItem{
		Network:      ip,
		CooldownOver: over,
	}
	if s.putIpToCooldown(cooldown) {
		ulog.Infof("Put ip %s to cooldown set, it will be recycled on %d", ip.VPCIP, over)
	}
}

func (s *ipamServer) recycleCooldownIP() {
	kvs, err := s.cooldownDB.List()
	if err != nil {
		ulog.Errorf("List cooldown ips from database error: %v", err)
		return
	}

	now := time.Now().Unix()
	for _, kv := range kvs {
		cd := kv.Value
		if now >= cd.CooldownOver {
			err = s.cooldownDB.Delete(kv.Key)
			if err != nil {
				ulog.Errorf("Delete cooldown ip %v from database error: %v", kv.Key, err)
				return
			}

			network := kv.Value.Network
			exists, err := s.checkSecondaryIpExist(network.VPCIP, s.hostMacAddr)
			if err != nil {
				ulog.Errorf("Check cooldown ip %v from vpc error: %v", network.VPCIP, err)
				err = s.cooldownDB.Put(kv.Key, kv.Value)
				if err != nil {
					ulog.Errorf("Put ip %v back to cooldown pool after checking health error: %v, it will leak", network.VPCIP, err)
				}
				return
			}
			if !exists {
				ulog.Warnf("The cooldwon ip %s is not exist in vpc, ignore it", network.VPCIP)
				continue
			}

			if s.putIpToPool(kv.Value.Network) {
				ulog.Infof("Recycle cooldown ip %s to pool", kv.Value.Network.VPCIP)
			}
		}
	}
}

func (s *ipamServer) borrowIP() (*rpc.PodNetwork, error) {
	ctx := context.Background()
	val, err := s.crdClient.IpamdV1beta1().Ipamds("kube-system").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ipamd resources: %v", err)
	}
	if len(val.Items) <= 1 {
		// If the number of items is 1, it means that the current cluster only
		// has one current ipamd, and there is no ipamd for us to borrow.
		return nil, errors.New("no other ipamd to borrow ip")
	}

	currentSubnet := s.uapi.SubnetID()

	ipamds := make([]*ipamdv1beta1.Ipamd, 0, len(val.Items)-1)
	for _, ipamd := range val.Items {
		if ipamd.Spec.Node == s.nodeName {
			// Skip myself
			continue
		}
		ipamd := ipamd
		if ipamd.Status.Status == ipamdv1beta1.StatusDry {
			// We can't get anything out of a pool that's been dry, so skip it
			continue
		}
		if ipamd.Spec.Subnet != currentSubnet {
			// Borrowing IP across subnets is not allowed. Because the Mac address
			// of the current node does not exist in other subnets, routing cannot
			// be performed.
			continue
		}
		_, err = s.kubeClient.CoreV1().Nodes().Get(ctx, ipamd.Name, metav1.GetOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("call kube-client to get node %s: %v", ipamd.Name, err)
		}

		ipamds = append(ipamds, &ipamd)
	}

	if len(ipamds) == 0 {
		// If ipamds is empty after filtering, all ipamds are in dry status.
		return nil, errors.New("no ipamd to borrow")
	}

	// Prioritize borrowing from pools with more remaining IPs.
	// When a pool fails, we will continue to borrow downwards.
	sort.Slice(ipamds, func(i, j int) bool {
		return ipamds[i].Status.Current > ipamds[j].Status.Current
	})

	for _, ipamd := range ipamds {
		conn, err := grpc.Dial(ipamd.Spec.Addr, grpc.WithInsecure(),
			grpc.WithTimeout(time.Second))
		if err != nil {
			ulog.Errorf("Dial ipamd %q error: %v", ipamd.Name, err)
			continue
		}
		defer conn.Close()

		cli := rpc.NewCNIIpamClient(conn)
		resp, err := cli.BorrowIP(ctx, &rpc.BorrowIPRequest{
			MacAddr: s.hostMacAddr,
		})
		if err != nil {
			ulog.Errorf("Borrow from ipamd %q error: %v", ipamd.Name, err)
			continue
		}
		if resp.IP == nil {
			ulog.Errorf("Borrow from ipamd %q error: empty result", ipamd.Name)
			continue
		}
		pn := resp.IP
		ulog.Infof("Borrowed ip %q from ipamd %q", pn.VPCIP, ipamd.Name)
		return pn, nil
	}

	return nil, errors.New("failed to borrow ip from other ipamd")
}

func (s *ipamServer) lendIP(newMac string) (*rpc.PodNetwork, error) {
	kv, err := s.poolDB.Pop()
	if err != nil {
		if database.IsEOF(err) {
			return nil, errors.New("ip pool is empty")
		}
		return nil, fmt.Errorf("failed to pop ip from pool: %v", err)
	}
	val := kv.Value
	err = s.uapiMoveSecondaryIPMac(val.VPCIP, s.hostMacAddr, newMac, val.SubnetID)
	if err != nil {
		// If the loan fails, we need to put the IP back into the pool, otherwise
		// it will cause IP leakage.
		s.putIpToPool(val)
		return nil, fmt.Errorf("failed to call uapi to move ip: %v", err)
	}
	return val, nil
}

func (s *ipamServer) usedIP() (map[string]struct{}, error) {
	poolKvs, err := s.poolDB.List()
	if err != nil {
		return nil, err
	}
	pool := database.Values(poolKvs)

	cooldownKvs, err := s.cooldownDB.List()
	if err != nil {
		return nil, err
	}
	cooldown := database.Values(cooldownKvs)

	networkKvs, err := s.networkDB.List()
	if err != nil {
		return nil, err
	}
	networks := database.Values(networkKvs)

	used := make(map[string]struct{}, len(pool)+len(cooldown)+len(networks))
	for _, ip := range pool {
		used[ip.VPCIP] = struct{}{}
	}
	for _, ip := range cooldown {
		used[ip.Network.VPCIP] = struct{}{}
	}
	for _, ip := range networks {
		used[ip.VPCIP] = struct{}{}
	}

	return used, nil
}

func (s *ipamServer) checkIPHealth() error {
	kvs, err := s.poolDB.List()
	if err != nil {
		return err
	}

	// Let's pick an IP that hasn't been checked for the longest time.
	var toCheck *database.KeyValue[rpc.PodNetwork]
	for _, kv := range kvs {
		if toCheck == nil {
			toCheck = kv
			continue
		}

		if kv.Value.CheckHealthTime < toCheck.Value.CheckHealthTime {
			toCheck = kv
		}
	}

	if toCheck == nil {
		ulog.Infof("No ip in pool, skip checking health")
		return nil
	}

	ulog.Infof("Check health for ip %s", toCheck.Value.VPCIP)
	exists, err := s.checkSecondaryIpExist(toCheck.Value.VPCIP, s.hostMacAddr)
	if err != nil {
		return fmt.Errorf("failed to check ip health for %s, vpc api error: %v", toCheck.Value.VPCIP, err)
	}

	if !exists {
		// This ip has already been removed in VPC, it should not be assigned to Pod.
		// So it should be removed from pool immediately.
		ulog.Warnf("The ip %s is not exist in vpc, remove it in pool", toCheck.Value.VPCIP)
		err = s.poolDB.Delete(toCheck.Key)
		if err != nil {
			return fmt.Errorf("failed to remove not exists ip %s from pool: %v", toCheck.Value.VPCIP, err)
		}

		return nil
	}

	ulog.Infof("The ip %s is exists in VPC", toCheck.Value.VPCIP)

	toCheck.Value.CheckHealthTime = time.Now().Unix()
	err = s.poolDB.Put(toCheck.Key, toCheck.Value)
	if err != nil {
		return fmt.Errorf("failed to update check health time for ip %s: %v", toCheck.Value.VPCIP, err)
	}
	return nil
}

func (s *ipamServer) putIpToPool(ip *rpc.PodNetwork) bool {
	err := s.poolDB.Put(getReservedIPKey(ip), ip)
	if err != nil {
		ulog.Errorf("Put ip %q to pool error: %v, try to release it", ip.VPCIP, err)
		s.backupReleaseSecondaryIP(ip.VPCIP)
		return false
	}
	return true
}

func (s *ipamServer) putIpToCooldown(item *CooldownItem) bool {
	err := s.cooldownDB.Put(getReservedIPKey(item.Network), item)
	if err != nil {
		ulog.Errorf("Put ip %q to cooldown error: %v, try to release it", item.Network.VPCIP, err)
		s.backupReleaseSecondaryIP(item.Network.VPCIP)
		return false
	}
	return true
}

func (s *ipamServer) backupReleaseSecondaryIP(ip string) {
	err := s.uapiDeleteSecondaryIp(ip)
	if err != nil {
		ulog.Errorf("Backup release ip %s error: %v, this ip will leak", ip, err)
	} else {
		ulog.Infof("Release ip %s after failure", ip)
	}
}

func (s *ipamServer) backupPushSecondaryIP(network *rpc.PodNetwork) {
	err := s.poolDB.Put(getReservedIPKey(network), network)
	if err != nil {
		ulog.Errorf("Backup put ip %s error: %v, this ip will leak", network.VPCIP, err)
	} else {
		ulog.Infof("Add ip %s after failure", network.VPCIP)
	}
}
