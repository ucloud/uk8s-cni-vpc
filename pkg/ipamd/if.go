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
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ucloud/uk8s-cni-vpc/pkg/database"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/j-keck/arping"
	"github.com/ucloud/ucloud-sdk-go/services/vpc"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	v1 "k8s.io/api/core/v1"
)

const (
	defaultMtu  = 1452
	storageFile = "/opt/cni/networkbolt.db"
)

// netNS /proc/9791/ns/net
func (s *ipamServer) setupDedicatedUNIForPod(pod *v1.Pod, netNS string, cfg *EIPCfg) (*vpc.NetworkInterface, error) {
	ulog.Infof("Set up UNI for pod %s/%s, network namespace %s", pod.Name, pod.Namespace, netNS)

	// Create a new UCloud NetworkInterface
	id, err := s.uapiCreateUNI(pod, cfg)
	if err != nil {
		ulog.Errorf("Allocate UNI from unetwork service error: %v", err)
		return nil, err
	}

	// Set up UNI for pod
	uni, err := s.setupUNI(id, netNS)
	if err != nil {
		ulog.Errorf("Setup UNI %v for pod %s/%s error: %v, give UNI back", uni.InterfaceId, pod.Name, pod.Namespace, err)
		// Rollback uni resource
		s.rollBackUNI(string(pod.UID), uni.InterfaceId)
		return nil, err
	}
	// Allocate EIP and associate it to UNI; if EIP is provided by user, then use it.
	var eipId string
	if len(cfg.EIPId) == 0 {
		eip, err := s.uapiAllocateEIP(pod, cfg)
		if err != nil {
			ulog.Errorf("Allocate EIP for pod %s/%s error: %v", pod.Name, pod.Namespace, err)
			// Rollback uni resource
			s.rollBackUNI(string(pod.UID), uni.InterfaceId)
			return nil, err
		}
		eipId = eip.EIPId
	} else {
		eipId = cfg.EIPId
	}

	err = s.uapiBindEIPForUNI(eipId, uni.InterfaceId)
	if err != nil {
		ulog.Errorf("Bind EIP for pod %s/%s in uni %v error: %v", pod.Name, pod.Namespace, uni.InterfaceId, err)
		s.rollBackUNI(string(pod.UID), uni.InterfaceId)
		return nil, err
	}

	uni, err = s.uapiDescribeUNI(uni.InterfaceId)
	if err == nil {
		annotations := map[string]string{AnnotationUNIID: uni.InterfaceId}
		eipSet, err := s.uapiDescribeEIP(eipId)
		if err == nil && len(eipSet.EIPAddr) > 0 {
			annotations[AnnotationEIPAddr] = eipSet.EIPAddr[0].IP
		}
		err = s.setPodAnnotation(pod, annotations)
		if err != nil {
			ulog.Warnf("Update annotation for pod %s/%s error: %v", pod.Name, pod.Namespace, err)
		}
		ulog.Infof("Pod %s/%s, network namespace %s now has dedicated UNI %s, and EIP %s is bound", pod.Name, pod.Namespace, netNS, uni.InterfaceId,
			eipId)
		return uni, nil
	} else {
		ulog.Errorf("Update uni %s error: %v", uni.InterfaceId, err)
		return nil, err
	}
}

func (s *ipamServer) rollBackUNI(podUID string, interfaceId string) {
	go func() {
		e := s.releaseUNI(string(podUID), interfaceId)
		if e != nil {
			ulog.Errorf("Release uni %s error: %v", interfaceId, e)
		}
	}()
}

func (s *ipamServer) tearDownDedicatedUNIForPod(pNet *rpc.PodNetwork) error {
	ulog.Infof("Tear down UNI for pod %s/%s", pNet.PodName, pNet.PodNS)
	// Get the pid of the pause process inside the pod
	p, err := s.networkDB.Get(database.PodKey(pNet.PodName, pNet.PodNS, pNet.SandboxID))
	if err != nil {
		ulog.Errorf("Get pod %s network information error: %v", pNet.PodName+"/"+pNet.PodNS, err)
		return err
	}
	if p == nil {
		ulog.Infof("Pod network information for %s/%s no more exist", pNet.PodName, pNet.PodNS)
		return nil
	}
	ulog.Infof("Pod network for %s/%s:%+v", pNet.PodName, pNet.PodNS, pNet)
	err = s.releaseUNI(pNet.PodUID, pNet.InterfaceID)
	if err != nil {
		return fmt.Errorf("fail to release uni %s, %v", pNet.InterfaceID, err)
	}

	return nil
}

func parsePid(netNS string) (int, error) {
	segs := strings.Split(netNS, "/")
	if len(segs) < 3 {
		return 0, fmt.Errorf("NS Path %s don't contains pid", netNS)
	}

	return strconv.Atoi(segs[2])
}

func (s *ipamServer) setupUNI(id string, netNS string) (*vpc.NetworkInterface, error) {
	nsHandler, err := ns.GetNS(netNS)
	if err != nil {
		return nil, err
	}
	defer nsHandler.Close()

	// Attach UNI to UHost
	err = s.uapiAttachUNI(s.hostId, id)
	if err != nil {
		ulog.Errorf("Attach UNI %v to %v error: %v", id, s.hostId, err)
		return nil, err
	}
	uni, _ := s.uapiDescribeUNI(id)
	link, err := findInterfaceOfUNI(uni, nil)
	if err != nil {
		return nil, err
	}
	// Modify MTU
	err = netlink.LinkSetMTU(link, defaultMtu)
	if err != nil {
		ulog.Errorf("Modify mtu for link %v error: %v", link.Attrs().Name, err)
		return nil, err
	}
	// Move link to pod's namespace
	err = netlink.LinkSetNsFd(link, int(nsHandler.Fd()))
	if err != nil {
		return nil, fmt.Errorf("cannot move link %s to network namespace of netns %v(%d), %v",
			link.Attrs().Name, netNS, int(nsHandler.Fd()), err)
	}
	podNS, err := netns.GetFromPath(netNS)
	if err != nil {
		return nil, fmt.Errorf("cannot get netns handler of pid %v, %v", netNS, err)
	}
	// Get namespace handler
	podNShandle, err := netlink.NewHandleAt(podNS, netlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("cannot get netlink handler of %v, %v", podNS, err)
	}
	ulog.Infof("Get access to pod network namespace, %+v", podNShandle)
	defer podNShandle.Delete()
	// Set link name eth0
	link, err = findInterfaceOfUNI(uni, podNShandle)
	if err != nil {
		return nil, err
	}
	err = podNShandle.LinkSetName(link, "eth0")
	if err != nil {
		return nil, fmt.Errorf("cannot change link name to eth0 inside pod network namespace, %v", err)
	}
	// Assign primary ip to interface
	if len(uni.PrivateIpSet) == 0 {
		return nil, fmt.Errorf("no primary ip to be assigned to uni %v", uni.InterfaceId)
	}
	// Parse cidr
	h, _ := net.IPMask(net.ParseIP(uni.Netmask).To4()).Size()
	addr, err := netlink.ParseAddr(uni.PrivateIpSet[0] + "/" + fmt.Sprintf("%d", h))
	if err != nil {
		return nil, fmt.Errorf("parse addr %s of uni %v failed, %v", uni.PrivateIpSet[0], uni.InterfaceId, err)
	}
	err = podNShandle.AddrAdd(link, addr)
	if err != nil {
		return nil, fmt.Errorf("cannot add ip %s to uni %v, %v", uni.PrivateIpSet[0], uni.InterfaceId, err)
	}

	// Set link up
	err = podNShandle.LinkSetUp(link)
	if err != nil {
		return nil, fmt.Errorf("cannot set link up inside pod network namespace, %v", err)
	}

	// Establish default route like:
	// ip r add default via 10.10.0.1 dev eth0
	err = podNShandle.RouteAdd(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Scope:     netlink.SCOPE_UNIVERSE,
		Dst:       nil,
		Gw:        net.ParseIP(uni.Gateway),
	})
	if err != nil {
		return nil, fmt.Errorf("unable to add default route rule to gateway %s,%v", uni.Gateway, err)
	}
	// Route service ip cidr to root network namespace
	if s.svcCIDR != nil {
		err = podNShandle.RouteAdd(&netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Dst:       s.svcCIDR,
			Gw:        s.nodeIpAddr.IP,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to route service cidr %v to root network namespace(next hop %s), %v", s.svcCIDR, s.nodeIpAddr.IP, err)
	}

	// Send a gratuitous arp, using hardware address of UNI
	netns, err := ns.GetNS(netNS)
	if err != nil {
		ulog.Warnf("Failed to open netns %s:, %v", netNS, err)
		return uni, nil
	}
	defer netns.Close()
	_ = netns.Do(func(_ ns.NetNS) error {
		for i := 0; i <= 2; i++ {
			_ = arping.GratuitousArpOverIfaceByName(net.ParseIP(uni.PrivateIpSet[0]), iputils.GetMasterInterface())
			if i != 2 {
				time.Sleep(100 * time.Millisecond)
			}
		}
		return nil
	})
	return uni, nil
}

func (s *ipamServer) releaseUNI(podUid, uniId string) error {
	uni, err := s.uapiDescribeUNI(uniId)
	if err != nil {
		return err
	}

	if uni.Status == 1 {
		// If uni already attached to another instance, we skip
		if uni.AttachInstanceId != s.hostId {
			return nil
		}
		// Unbind and release EIP(s)
		for _, eip := range uni.EIPIdSet {
			err = s.uapiUnbindEIP(eip, uni.InterfaceId, ResourceTypeUNI)
			if err != nil {
				return err
			}
			eipSet, err := s.uapiDescribeEIP(eip)
			if err == nil {
				expectedRemark := getUNetRemark(podUid)
				if eipSet.Remark == expectedRemark {
					ulog.Infof("Release EIP %s for %s", eip, expectedRemark)
					err = s.uapiReleaseEIP(eip)
					if err != nil {
						return err
					}
				}
			} else {
				ulog.Warnf("Describe eip %s error: %v", eip, err)
			}
		}
		// Detach UNI
		err := s.uapiDetachUNI(uni.AttachInstanceId, uni.InterfaceId)
		if err != nil {
			ulog.Errorf("Detach UNI %s from %s error: %v", uni.InterfaceId, uni.AttachInstanceId, err)
			return err
		}
	}

	// Delete UNI
	return s.uapiDeleteUNI(uni.InterfaceId)
}

func findInterfaceOfUNI(uni *vpc.NetworkInterface, handler *netlink.Handle) (netlink.Link, error) {
	var links []netlink.Link
	var err error
	if handler == nil {
		// Use current network namespace
		links, err = netlink.LinkList()
	} else {
		links, err = handler.LinkList()
	}

	if err != nil {
		ulog.Errorf("List all interfaces error: %v", err)
		return nil, err
	}

	for _, link := range links {
		if strings.ToLower(link.Attrs().HardwareAddr.String()) == strings.ToLower(uni.MacAddress) {
			ulog.Infof("Link %s (type %s) is the interface of UNI %s, MacAddr %v", link.Attrs().Name, link.Type(), uni.InterfaceId, uni.MacAddress)
			return link, nil
		}
	}

	return nil, fmt.Errorf("cannot find interface of attached UNI %v", uni.InterfaceId)
}
