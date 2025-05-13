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
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/ucloud/ucloud-sdk-go/services/vpc"
	ipamdv1beta1 "github.com/ucloud/uk8s-cni-vpc/kubernetes/apis/ipamd/v1beta1"
	"github.com/ucloud/uk8s-cni-vpc/pkg/database"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	flag.IntVar(&AvailablePodIPLowWatermark, "availablePodIPLowWatermark", 3, "low watermark number of allocated vpc ip to be assigned to new pods (deprecated, won't be used anymore)")
	flag.IntVar(&AvailablePodIPHighWatermark, "availablePodIPHighWatermark", 20, "high watermark number of allocated vpc ip to be assigned to new pods")
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
	chanAddPodIP = make(chan *InnerAddPodNetworkRequest)
	chanDelPodIP = make(chan *InnerDelPodNetworkRequest)
	chanStopLoop = make(chan bool)
)

func getReservedIPKey(pn *rpc.PodNetwork) string {
	return pn.VPCID + "-" + pn.VPCIP
}

func (s *ipamServer) staticIpPodExists(pn *rpc.PodNetwork) bool {
	if _, err := s.getVpcipClaim(pn.GetPodNS(), pn.GetPodName()); err != nil {
		return false
	}
	return true
}

func (s *ipamServer) assignStaticPodIP(pod *v1.Pod, sandboxID string, req *rpc.AddPodNetworkRequest) (*rpc.PodNetwork, error) {
	if vip, err := s.findStaticIp(pod.Namespace, pod.Name); err != nil {
		return nil, err
	} else {
		if vip != nil {
			if vip, err = s.localAttach(req, vip, sandboxID); err != nil {
				return nil, err
			}
			return VipToPodNetwork(vip), nil
		}
	}

	if nvip, err := s.assignPodIP(req); err == nil {
		nvip.CreateTime = time.Now().Unix()
		nvip.PodName = pod.Name
		nvip.PodNS = pod.Namespace
		nvip.SandboxID = sandboxID
		_, err = s.createVpcIpClaimByPodNetwork(req, nvip, pod)
		if err != nil {
			ulog.Errorf("Save static ip %v to crd error: %v", nvip, err)
			return nil, err
		}
		return nvip, nil
	}

	return nil, fmt.Errorf("can not get static ip for %s/%s", pod.Namespace, pod.Name)
}

func (s *ipamServer) getPodIp(req *rpc.AddPodNetworkRequest) (*rpc.PodNetwork, error) {
	podName := req.GetPodName()
	podNS := req.GetPodNamespace()
	sandboxId := req.GetSandboxID()
	enableStatic, pod, err := s.podEnableStaticIP(podName, podNS)
	if err != nil {
		return nil, err
	}
	var pn *rpc.PodNetwork
	if enableStatic {
		pn, err = s.assignStaticPodIP(pod, sandboxId, req)
	} else {
		pn, err = s.assignPodIP(req)
	}
	if err != nil {
		return nil, err
	}

	ulog.Infof("Check IP %s status in VPC", pn.VPCIP)
	// In some cases, the IP is deleted in VPC but still remain in the pool. If we give it to
	// the Pod, the Pod network will be unavailable.
	// So this check must be done before we returning IP. If the IP does not exist, returns
	// error to make kubelet retries to get another one.
	ok, err := s.checkSecondaryIpExist(pn)
	if err != nil {
		if !enableStatic {
			s.putIpToPool(pn)
		}
		return nil, fmt.Errorf("check ip %v status in vpc error: %v", pn.VPCIP, err)
	}
	if !ok {
		return nil, fmt.Errorf("ip %v does not exist on current node, we will try to use another one", pn.VPCIP)
	}

	if !pn.Recycled && pn.VPCIP != "" {
		// We need to detect IP conflict before using it.
		// See: https://www.rfc-editor.org/rfc/rfc5227
		err = s.checkIPConflict(pn.VPCIP)
		if err != nil {
			ulog.Errorf("Detect ip conflict for %s error: %v, we will release it", pn.VPCIP, err)
			delErr := s.uapiDeleteSecondaryIp(pn)
			if delErr != nil {
				ulog.Errorf("Release ip %s after conflict error: %v", pn.VPCIP, delErr)
			}
			return nil, err
		}
	}

	return pn, nil
}

func (s *ipamServer) assignPodIP(req *rpc.AddPodNetworkRequest) (*rpc.PodNetwork, error) {
	pool, err := s.getPool(req.SubnetID, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool to assign ip: %v", err)
	}

	kv, err := pool.Pop()
	if database.IsEOF(err) {
		// The pool is empty, try to assign a new one from VPC backend
		vpcIp, err := s.uapiAllocateSecondaryIP(req)
		if err != nil {
			if err == ErrOutOfIP {
				// Borrowing: When the vpc has no ip, other ipamd pools may still
				// have surplus, so we try to choose one of them to borrow.
				// The borrowed ip will call the vpc's MoveSecondaryIPMac to migrate
				// MAC address, see:
				//   https://docs.ucloud.cn/api/vpc2.0-api/move_secondary_ip_mac
				ulog.Infof("Out of ip in VPC, try to borrow one from other ipamd")
				pn, err := s.borrowIP(req)
				if err != nil {
					return nil, fmt.Errorf("vpc out of ip, and failed to borrow from others: %v", err)
				}
				return pn, nil
			}
			ulog.Errorf("Assign new ip for pod error: %v", err)
			return nil, err
		}

		return convertIpToPodNetwork(vpcIp, req.InterfaceID), nil
	}
	if err != nil {
		ulog.Errorf("Pop vpc ip from pool error: %v", err)
		return nil, err
	}

	return kv.Value, nil
}

func convertIpToPodNetwork(ip *vpc.IpInfo, ifaceID string) *rpc.PodNetwork {
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
		InterfaceID:  ifaceID,
		DedicatedUNI: false,
		CreateTime:   time.Now().Unix(),
	}
}

func (s *ipamServer) ipPoolWatermarkManager() {
	ulog.Infof("Start ip pool watermark manager loop")
	tk := time.NewTicker(10 * time.Second)
	cooldownTk := time.NewTicker(time.Second * time.Duration(CooldownPeriodSeconds))
	recycleStatusTk := time.NewTicker(10 * time.Minute)
	healthSizes := make(map[string]int)
	for {
		select {
		case <-tk.C:
			for key, pool := range s.poolDB {
				size, err := pool.Count()
				if err != nil {
					ulog.Errorf("Get pool size for %q error: %v", key, err)
					continue
				}

				if size > AvailablePodIPHighWatermark {
					// The pool is above the high watermark and we need to release the pool
					s.releasePool(key, pool, size)
				} else {
					healthSize := healthSizes[key]
					if healthSize != size {
						ulog.Infof("Pool %q size %d, healthy", key, size)
						healthSizes[key] = size
					}
				}
			}

		case r := <-chanAddPodIP:
			req := r.Req
			if req.SubnetID == "" {
				req.SubnetID = s.uapi.SubnetID()
			}
			if req.MacAddress == "" {
				req.MacAddress = s.masterMacAddress
			}
			pn, err := s.getPodIp(r.Req)
			r.Receiver <- &InnerAddPodNetworkResponse{
				PodNetwork: pn,
				Err:        err,
			}

		case r := <-chanDelPodIP:
			pn := r.Req.GetPodNetwork()
			if s.staticIpPodExists(pn) {
				// Nothing to do for static ip
				r.Receiver <- nil
				continue
			}
			// After the Pod releases the IP, set a cooldown time for the IP, and put it back into
			// the IP pool after the IP cools down.
			// The cooldown time is to prevent the IP from being assigned to the next pod before the
			// kubelet deleting the pod.
			s.cooldownIP(pn)
			r.Receiver <- nil

		case <-cooldownTk.C:
			// Recycle the cooldown IP to pool
			s.recycleCooldownIP()

		case <-recycleStatusTk.C:
			err := s.recycleStatus()
			if err != nil {
				ulog.Errorf("Recycle ipamd status error: %v", err)
			}

		case <-chanStopLoop:
			ulog.Infof("Now stop vpc ip pool manager loop")
			return
		}

		for key, pool := range s.poolDB {
			// Update the ipamd CR resource, save the latest watermark to the status.
			err := s.updateStatus(key, pool)
			if err != nil {
				ulog.Errorf("Update ipamd status for %q error: %v", key, err)
			}
		}
	}
}

func (s *ipamServer) updateStatus(key string, pool database.Database[rpc.PodNetwork]) error {
	subnetID, defaultSubnet := s.getPoolSubnetID(key)
	watermark, err := pool.Count()
	if err != nil {
		return fmt.Errorf("count pool size error: %v", err)
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

	var name string
	if defaultSubnet {
		name = s.nodeName
	} else {
		name = fmt.Sprintf("%s-%s", s.nodeName, subnetID)
	}

	ctx := context.Background()
	ipamdInfo, err := s.crdClient.IpamdV1beta1().Ipamds("kube-system").Get(ctx, name, metav1.GetOptions{})
	switch {
	case kerrors.IsNotFound(err):
		ipamdInfo = &ipamdv1beta1.Ipamd{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: ipamdv1beta1.IpamdSpec{
				Node:   s.nodeName,
				Addr:   s.tcpAddr,
				Subnet: subnetID,
			},
			Status: status,
		}
		_, err = s.crdClient.IpamdV1beta1().Ipamds("kube-system").Create(ctx, ipamdInfo, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("faield to create ipamd resource: %v", err)
		}
		ulog.Infof("Create ipamd resource %q, status: %+v", name, status)
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

	ulog.Infof("Update ipamd resource %q, status: %+v", name, status)
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

func (s *ipamServer) releasePool(key string, pool database.Database[rpc.PodNetwork], size int) {
	toRelease := size - AvailablePodIPHighWatermark
	ulog.Infof("Pool %q size %d, above high watermark %d, try to release %d ip",
		key, size, AvailablePodIPHighWatermark, toRelease)
	for i := 0; i < toRelease; i++ {
		kv, err := pool.Pop()
		if err != nil {
			// If the pool is empty here, it means that someone else had consumed all ip(s)
			// during the release process, we can safely interrupt in this case.
			if !database.IsEOF(err) {
				ulog.Errorf("Pop ip error: %v", err)
			}
			return
		}
		pn := kv.Value
		err = s.uapiDeleteSecondaryIp(pn)
		if err != nil {
			ulog.Errorf("Release secondary ip %s error: %v", pn.VPCIP, err)
			// Failed to release the ip, we may have problem communicating with the VPC server.
			// Put the ip back to pool to let it have chance to be released in the next loop.
			s.backupPushSecondaryIP(pn)
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

			if s.putIpToPool(kv.Value.Network) {
				ulog.Infof("Recycle cooldown ip %s to pool", kv.Value.Network.VPCIP)
			}
		}
	}
}

func (s *ipamServer) borrowIP(req *rpc.AddPodNetworkRequest) (*rpc.PodNetwork, error) {
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

	currentSubnet := req.SubnetID

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
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		conn, err := grpc.DialContext(ctx, ipamd.Spec.Addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			ulog.Errorf("Dial ipamd %q error: %v", ipamd.Name, err)
			continue
		}
		defer conn.Close()

		cli := rpc.NewCNIIpamClient(conn)
		resp, err := cli.BorrowIP(ctx, &rpc.BorrowIPRequest{
			SubnetID:   req.SubnetID,
			MacAddress: req.MacAddress,
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

func (s *ipamServer) lendIP(req *rpc.BorrowIPRequest) (*rpc.PodNetwork, error) {
	pool, err := s.getPool(req.SubnetID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool: %v", err)
	}

	kv, err := pool.Pop()
	if err != nil {
		if database.IsEOF(err) {
			return nil, errors.New("ip pool is empty")
		}
		return nil, fmt.Errorf("failed to pop ip from pool: %v", err)
	}
	pn := kv.Value
	err = s.uapiMoveSecondaryIPMac(pn, req.MacAddress)
	if err != nil {
		// If the loan fails, we need to put the IP back into the pool, otherwise
		// it will cause IP leakage.
		s.putIpToPool(pn)
		return nil, fmt.Errorf("failed to call uapi to move ip: %v", err)
	}
	return pn, nil
}

func (s *ipamServer) putIpToPool(pn *rpc.PodNetwork) bool {
	pool, err := s.getPool(pn.SubnetID, true)
	if err != nil {
		ulog.Errorf("Get pool to put pod network error: %v, try to release it", err)
		s.backupReleaseSecondaryIP(pn)
		return false
	}

	err = pool.Put(getReservedIPKey(pn), pn)
	if err != nil {
		ulog.Errorf("Put ip %q to pool error: %v, try to release it", pn.VPCIP, err)
		s.backupReleaseSecondaryIP(pn)
		return false
	}
	return true
}

func (s *ipamServer) putIpToCooldown(item *CooldownItem) bool {
	err := s.cooldownDB.Put(getReservedIPKey(item.Network), item)
	if err != nil {
		ulog.Errorf("Put ip %q to cooldown error: %v, try to release it", item.Network.VPCIP, err)
		s.backupReleaseSecondaryIP(item.Network)
		return false
	}
	return true
}

func (s *ipamServer) backupReleaseSecondaryIP(pn *rpc.PodNetwork) {
	err := s.uapiDeleteSecondaryIp(pn)
	if err != nil {
		ulog.Errorf("Backup release ip %s error: %v, this ip will leak", pn.VPCIP, err)
	} else {
		ulog.Infof("Release ip %s after failure", pn.VPCIP)
	}
}

func (s *ipamServer) backupPushSecondaryIP(pn *rpc.PodNetwork) {
	pool, err := s.getPool(pn.SubnetID, true)
	if err != nil {
		ulog.Errorf("Backput get pool for %q error: %v, this ip will leak", pn.VPCIP, err)
		return
	}

	err = pool.Put(getReservedIPKey(pn), pn)
	if err != nil {
		ulog.Errorf("Backup put ip %s error: %v, this ip will leak", pn.VPCIP, err)
	} else {
		ulog.Infof("Add ip %s after failure", pn.VPCIP)
	}
}

func (s *ipamServer) getPool(subnetID string, create bool) (database.Database[rpc.PodNetwork], error) {
	s.poolLock.Lock()
	defer s.poolLock.Unlock()

	var dbName string
	if subnetID != "" {
		dbName = fmt.Sprintf("%s-%s", PoolDBName, subnetID)
	} else {
		dbName = PoolDBName
	}
	pool, ok := s.poolDB[dbName]
	if !ok {
		if !create {
			return nil, fmt.Errorf("cannot find subnet %q in this node", subnetID)
		}
		var err error
		pool, err = database.NewBolt[rpc.PodNetwork](dbName, s.dbHandler)
		if err != nil {
			return nil, fmt.Errorf("failed to init vpc ip pool database: %v", err)
		}
		s.poolDB[dbName] = pool
	}

	return pool, nil
}

func (s *ipamServer) getPoolSubnetID(key string) (string, bool) {
	key = strings.TrimPrefix(key, PoolDBName)
	if key == "" {
		return s.uapi.SubnetID(), true
	}
	key = strings.TrimPrefix(key, "-")
	return key, false
}
