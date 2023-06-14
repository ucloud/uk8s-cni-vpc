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

package main

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/ucloud/uk8s-cni-vpc/pkg/snapshot"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
)

const (
	hostVethPrefix = "ucni"
	defaultMtu     = 1452
)

var IPConflictError = errors.New("allocated IP is conflict with existing IP")

type netlinkInterface interface {
	LinkByName(name string) (netlink.Link, error)
	RouteAdd(route *netlink.Route) error
}

type netlinkImpl struct {
}

func (netlinkImpl) LinkByName(name string) (netlink.Link, error) {
	return netlink.LinkByName(name)
}

func (netlinkImpl) RouteAdd(route *netlink.Route) error {
	return netlink.RouteAdd(route)
}

var NewNl = func() netlinkInterface {
	return netlinkImpl{}
}

func addRouteRuleForPodIp(hostVeth, ip string) error {
	nl := NewNl()
	link, err := nl.LinkByName(hostVeth)
	if err != nil {
		ulog.Errorf("Get host veth link error: %v", err)
		return err
	}
	cidr := ip + "/32"
	_, dstcidr, err := net.ParseCIDR(cidr)
	if err != nil {
		ulog.Errorf("Parse CIDR %s error: %v", cidr, err)
		return err
	}
	r := netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       dstcidr,
		Scope:     netlink.SCOPE_LINK,
	}
	return nl.RouteAdd(&r)
}

func enableForwarding(ipv4 bool, ipv6 bool) error {
	if ipv4 {
		err := ip.EnableIP4Forward()
		if err != nil {
			return fmt.Errorf("Could not enable IPv4 forwarding: %v", err)
		}
	}
	if ipv6 {
		err := ip.EnableIP6Forward()
		if err != nil {
			return fmt.Errorf("Could not enable IPv6 forwarding: %v", err)
		}
	}
	return nil
}

func ensureProxyArp(dev string) error {
	proxyArpCnfFile := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/proxy_arp", dev)
	err := os.Truncate(proxyArpCnfFile, 0)
	if err != nil {
		ulog.Errorf("Truncate file %s error: %v", proxyArpCnfFile, err)
		return err
	}
	f, err := os.OpenFile(proxyArpCnfFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	defer f.Close()
	if err != nil {
		ulog.Errorf("Open file %s error: %v", proxyArpCnfFile, err)
		return err
	}
	io.WriteString(f, "1")
	return nil
}

type iptable interface {
	AppendUnique(table, chain string, rulespec ...string) error
}

var newIPTable = func(protocol iptables.Protocol) (iptable, error) {
	return iptables.NewWithProtocol(protocol)
}

func setupPodVethNetwork(podName, podNS, netNS, sandBoxId, nic string, pNet *rpc.PodNetwork) error {
	netns, err := ns.GetNS(netNS)
	if err != nil {
		ulog.Errorf("Open netns %q error: %v", netNS, err)
		releasePodIp(podName, podNS, sandBoxId, pNet)
		return fmt.Errorf("Failed to open netns %q: %v", netNS, err)
	}
	defer netns.Close()
	mface, err := netlink.LinkByName(nic)
	if err != nil {
		ulog.Errorf("Lookup %s error: %v", nic, err)
		releasePodIp(podName, podNS, sandBoxId, pNet)
		return fmt.Errorf("failed to lookup %s: %v", nic, err)
	}

	hostAddrs, err := netlink.AddrList(mface, netlink.FAMILY_V4)
	if err != nil || len(hostAddrs) == 0 {
		ulog.Errorf("Get host ip addresses for %q error: %v", mface, err)
		releasePodIp(podName, podNS, sandBoxId, pNet)
		return fmt.Errorf("Failed to get host ip addresses for %q: %v", mface, err)
	}
	hostVeth, _, err := setupVethPair(netns, os.Getenv("CNI_IFNAME"),
		generateHostVethName(hostVethPrefix, podNS, podName),
		defaultMtu, hostAddrs,
		pNet.VPCIP+"/32")
	if err != nil {
		ulog.Errorf("Setup vethpair between host and container error: %v", err)
		releasePodIp(podName, podNS, sandBoxId, pNet)
		return err
	}

	// Add a route rule in host when accessing pod ip go to veth
	err = addRouteRuleForPodIp(hostVeth.Name, pNet.VPCIP)
	if err != nil {
		ulog.Errorf("Add route rule for ip %v error: %v", pNet.VPCIP, err)

		// When adding a route fails, for the convenience of debugging, we use
		// snapshot to save some output of the ip command.
		// The snapshot will be saved to /opt/cni/snapshot/ip_route_{vpcip}
		snapshot := snapshot.New(fmt.Sprintf("ip_route_%s", pNet.VPCIP))
		snapshot.SetDesc(fmt.Sprintf("Adding route rule failure, veth name: %s", hostVeth.Name))
		snapshot.SetError(err)
		snapshot.Add("ip", "addr")
		snapshot.Add("ip", "link")
		snapshot.Add("ip", "route")
		snapshot.Add("ip", "netns", "list-id")
		snapshot.Save()

		return err
	}
	return nil
}

func setupVethPair(netns ns.NetNS, ifName, hostVethName string, mtu int, hostAddrs []netlink.Addr, containerIp string) (*current.Interface, *current.Interface, error) {
	hostInterface := &current.Interface{}
	containerInterface := &current.Interface{}
	// Clean up old veth, if old veth exists
	err := checkAndCleanOldVeth(hostVethName)
	if err != nil {
		return nil, nil, err
	}
	// In pod's network namespace
	err = netns.Do(func(hostNS ns.NetNS) error {
		hostVeth, contVeth0, err := ip.SetupVethWithName(ifName, hostVethName, mtu, hostNS)
		if err != nil {
			return err
		}
		hostInterface.Name = hostVeth.Name
		hostInterface.Mac = hostVeth.HardwareAddr.String()
		containerInterface.Name = contVeth0.Name
		// ip.SetupVeth does not retrieve MAC address from peer in veth
		containerNetlinkIface, _ := netlink.LinkByName(contVeth0.Name)
		containerInterface.Mac = containerNetlinkIface.Attrs().HardwareAddr.String()
		containerInterface.Sandbox = netns.Path()

		addr, err := netlink.ParseAddr(containerIp)
		if err != nil {
			ulog.Errorf("Parse container IP error: %v", err)
			return err
		}

		err = netlink.AddrAdd(containerNetlinkIface, addr)
		if err != nil {
			ulog.Errorf("Add container IP to veth error: %v", err)
			return err
		}

		contVeth, err := net.InterfaceByName(ifName)
		if err != nil {
			return fmt.Errorf("Failed to look up %q: %v", ifName, err)
		}

		// Add a default gateway pointed at the eth0
		// ip route add default via $UHostIP onlink dev eth0
		err = netlink.RouteAdd(&netlink.Route{
			LinkIndex: contVeth.Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Dst:       nil,
			Gw:        hostAddrs[0].IP,
			Flags:     int(netlink.FLAG_ONLINK),
		})
		if err != nil {
			return fmt.Errorf("Failed to add default route on eth0:%v", err)
		}
		// Add a static arp entry, avoiding initial proxy arp request
		// ip neigh add $UHostIP lladdr $vethMac(uhost'ns side) dev eth0
		err = netlink.NeighAdd(
			&netlink.Neigh{
				LinkIndex:    contVeth.Index,
				IP:           hostAddrs[0].IP,
				State:        netlink.NUD_PERMANENT,
				HardwareAddr: hostVeth.HardwareAddr,
			},
		)
		if err != nil {
			return fmt.Errorf("Failed to add a neigh entry for veth of uhost's ns: %v", err)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return hostInterface, containerInterface, nil
}

func delHostSideVeth(podNS, podName string) error {
	vethName := generateHostVethName(hostVethPrefix, podNS, podName)
	link, err := netlink.LinkByName(vethName)
	if err != nil {
		return nil
	}
	err = netlink.LinkDel(link)
	if err != nil {
		ulog.Warnf("Delete hostside veth %s for pod %s/%s error: %v", vethName, podNS, podName, err)
		return err
	}
	ulog.Infof("Finished deleting hostside veth %s for pod %s/%s", vethName, podNS, podName)
	return nil
}

func generateHostVethName(prefix, namespace, podname string) string {
	// A SHA1 is always 20 bytes long, and so is sufficient for generating the
	// veth name and mac addr.
	h := sha1.New()
	h.Write([]byte(fmt.Sprintf("%s.%s", namespace, podname)))
	return fmt.Sprintf("%s%s", prefix, hex.EncodeToString(h.Sum(nil))[:11])
}

func checkAndCleanOldVeth(hostVethName string) error {
	if oldHostVeth, err := netlink.LinkByName(hostVethName); err == nil {
		if err = netlink.LinkDel(oldHostVeth); err != nil {
			ulog.Errorf("Delete old hostVeth %v error: %v", hostVethName, err)
			return fmt.Errorf("failed to delete old hostVeth %v: %v", hostVethName, err)
		}
		ulog.Infof("Finished clean old hostVeth: %v", hostVethName)
		return nil
	}
	return nil
}
