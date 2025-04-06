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
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/ucloud/uk8s-cni-vpc/config"
	"github.com/ucloud/uk8s-cni-vpc/pkg/arping"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/lockfile"
	"github.com/ucloud/uk8s-cni-vpc/pkg/portmap"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	vs "github.com/ucloud/uk8s-cni-vpc/pkg/version"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

// loadSandboxArgs parses args from a string in the form "K=V;K2=V2;..."
// This are CNI_ARGS for sandbox containers passed by kubelet
func loadSandboxArgs(args string) map[string]string {
	ret := make(map[string]string, 0)
	pairs := strings.Split(args, ";")
	for _, pair := range pairs {
		kv := strings.Split(pair, "=")
		if len(kv) != 2 {
			continue
		}
		keyString := kv[0]
		valueString := kv[1]
		ret[keyString] = valueString
	}
	return ret
}

func cmdVersion(args *skel.CmdArgs) error {
	return nil
}

func cmdArgsString(args *skel.CmdArgs) string {
	stdin := string(args.StdinData)
	stdin = strings.ReplaceAll(stdin, "\n", "")
	return fmt.Sprintf("container: %s, netns: %s, ifname: %s, args: %s, path: %s, stdin: %s",
		args.ContainerID, args.Netns, args.IfName, args.Args, args.Path, stdin)
}

// cmdAdd is called for ADD requests
func cmdAdd(args *skel.CmdArgs) error {
	releaseLock := lockfile.MustAcquire()
	defer releaseLock()

	ulog.Infof("CmdAdd, %s", cmdArgsString(args))
	conf, err := config.ParsePlugin(args.StdinData)
	if err != nil {
		ulog.Errorf("Parse cmdAdd config error: %v", err)
		return fmt.Errorf("failed to parse cmdadd config: %v", err)
	}

	podArgs := loadSandboxArgs(args.Args)
	podName := podArgs["K8S_POD_NAME"]
	podNS := podArgs["K8S_POD_NAMESPACE"]
	sandBoxId := podArgs["K8S_POD_INFRA_CONTAINER_ID"]
	netNS := os.Getenv("CNI_NETNS")
	masterInterface := iputils.GetMasterInterface()

	// To assign a VPC IP for pod
	pNet, fromIpam, err := assignPodIp(podName, podNS, netNS, sandBoxId)
	if err != nil {
		ulog.Errorf("Assign a vpc ip for pod %s/%s error: %v", podName, podNS, err)
		return fmt.Errorf("failed to assign ip: %v", err)
	}

	rollbackReleaseIP := func() {
		err = releasePodIp(podName, podNS, sandBoxId, pNet)
		if err != nil {
			ulog.Errorf("Release ip %s after failure error: %v, ip might leak", pNet.VPCIP, err)
		}
	}

	if !fromIpam {
		err = ensureProxyArp(masterInterface)
		if err != nil {
			ulog.Errorf("Enable %s proxy arp error: %v", masterInterface, err)
			rollbackReleaseIP()
			return fmt.Errorf("failed to enable proxy arp: %v", err)
		}
		conflict, err := arping.DetectIpConflictWithGratuitousArp(net.ParseIP(pNet.VPCIP), iputils.GetMasterInterface())
		if err != nil {
			ulog.Errorf("Detect conflict for ip %v of pod %v error: %v", pNet.VPCIP, podName, err)
			rollbackReleaseIP()
			return fmt.Errorf("failed to detect conflict: %v", err)
		}
		if conflict {
			ulog.Errorf("IP %v is still in conflict after retrying for pod %v", pNet.VPCIP, podName)
			rollbackReleaseIP()
			return IPConflictError
		}
	}

	// No dedicated UNI, we need to setup vethpair to pod's network namespace
	if !pNet.DedicatedUNI {
		err = setupPodVethNetwork(podName, podNS, netNS, sandBoxId, masterInterface, pNet)
		if err != nil {
			ulog.Errorf("Setup pod veth network error: %v", err)
			rollbackReleaseIP()
			return fmt.Errorf("failed to setup veth network: %v", err)
		}
	}

	// ip_local_port_range
	err = setNodePortRange(podName, podNS, netNS, sandBoxId, pNet)
	if err != nil {
		ulog.Errorf("Set node port range network error: %v", err)
		rollbackReleaseIP()
		return fmt.Errorf("failed to set node port: %v", err)
	}

	result := &current.Result{}
	// Fill result DNS
	result.DNS = parseDNSConfig()
	// Fill result ipconfig
	ipconfig := &current.IPConfig{
		// TODO: support v6
		Version: "4",
		Address: net.IPNet{
			IP:   net.ParseIP(pNet.VPCIP),
			Mask: net.IPMask(net.ParseIP(pNet.Mask)),
		},
		Gateway:   net.ParseIP(pNet.Gateway),
		Interface: current.Int(0),
	}
	result.IPs = append(result.IPs, ipconfig)
	// Fill result interface
	itface := &current.Interface{
		// We need to use eth0 to virualize slave interfaces during next phase: ipvlan
		Name:    "eth0",
		Mac:     pNet.MacAddress,
		Sandbox: netNS,
	}
	result.Interfaces = append(result.Interfaces, itface)
	routes, err := getRoutes()
	if err != nil {
		result.Routes = routes
	}

	err = addPodNetworkRecord(podName, podNS, sandBoxId, pNet)
	if err != nil {
		ulog.Errorf("Record pod network info for %s/%s, sandbox: %s, ip: %s, error: %v", podName, podNS, sandBoxId, pNet.VPCIP, err)
		return fmt.Errorf("failed to add pod network record: %v", err)
	}
	// Fill result routes
	ulog.Infof("[Result]: %+v", result)
	conf.PrevResult = result
	err = portmap.CmdAdd(args, conf)
	if err != nil {
		return fmt.Errorf("portmap add error: %v", err)
	}
	return nil
}

// cmdDel is called for DELETE requests
func cmdDel(args *skel.CmdArgs) error {
	releaseLock := lockfile.MustAcquire()
	defer releaseLock()

	ulog.Infof("CmdDel, %s", cmdArgsString(args))
	conf, err := config.ParsePlugin(args.StdinData)
	if err != nil {
		ulog.Errorf("Parse cmdDel config error: %v", err)
		return err
	}
	podArgs := loadSandboxArgs(args.Args)
	podName := podArgs["K8S_POD_NAME"]
	podNS := podArgs["K8S_POD_NAMESPACE"]
	netNS := os.Getenv("CNI_NETNS")
	sandBoxId := podArgs["K8S_POD_INFRA_CONTAINER_ID"]
	_ = conf
	pNet, err := getPodNetworkRecord(podName, podNS, sandBoxId)
	if err != nil {
		// podIP may be deleted in previous CNI DEL action
		ulog.Warnf("Get pod IP from local storage for pods %s, sandbox %v error: %v", podName, sandBoxId, err)
		return nil
	}
	// podIP may be deleted in previous CNI DEL action
	if pNet != nil && len(pNet.VPCIP) > 0 {
		ulog.Infof("Pod network info %+v", pNet)
		err = releasePodIp(podName, podNS, sandBoxId, pNet)
		if err != nil {
			return fmt.Errorf("failed to release pod ip %v, %v", pNet.VPCIP, err)
		}
		err = delPodNetworkRecord(podName, podNS, sandBoxId, pNet)
		if err != nil {
			ulog.Warnf("Delete pod network record of %s/%s error: %v", podName, podNS, err)
		}
	}

	err = portmap.CmdDel(args, conf)
	if err != nil {
		return err
	}

	ifname := os.Getenv("CNI_IFNAME")
	if netNS != "" && ifname != "" {
		// The container manager can delete the interface for us, but this is unreliable that
		// sometimes the container manager will fail to delete the interface for various reasons.
		// So we need to manually perform a cleanup here to avoid interface leaks.
		err = ns.WithNetNSPath(netNS, func(_ ns.NetNS) error {
			iface, err := netlink.LinkByName(ifname)
			if err != nil {
				// The interface might be deleted by container manager, we can skip
				// deleting safely.
				if _, ok := err.(netlink.LinkNotFoundError); ok {
					return nil
				}
				return fmt.Errorf("failed to get netlink %s: %v", ifname, err)
			}
			err = netlink.LinkDel(iface)
			if err != nil && err == ip.ErrLinkNotFound {
				return nil
			}
			return err
		})
		if err != nil {
			return fmt.Errorf("failed to delete interface %s in %s: %v", ifname, netNS, err)
		}
	}
	return err
}

func parseDNSConfig() types.DNS {
	dns := dnsReadConfig("/etc/resolve.conf")
	result := types.DNS{}
	result.Nameservers = dns.Servers
	result.Search = dns.Search
	return result
}

func getRoutes() (routes []*types.Route, err error) {
	links, err := netlink.LinkList()
	if err != nil {
		return
	}
	for _, link := range links {
		rs, e := netlink.RouteList(link, netlink.FAMILY_V4)
		if e != nil {
			err = e
			return
		}

		for _, r := range rs {
			if r.Dst != nil {
				routes = append(routes, &types.Route{Dst: *r.Dst, GW: r.Gw})
			} else {
				dst := net.IPNet{IP: nil, Mask: nil}
				routes = append(routes, &types.Route{Dst: dst, GW: r.Gw})
			}
		}
	}
	return
}

// Process exit itself in case of hang for a long time
func tickSuicide(done chan bool) {
	tick := time.NewTimer(180 * time.Second)
	select {
	case <-tick.C:
		{
			stackRecord := make([]byte, 8192)
			stackLen := runtime.Stack(stackRecord, true)
			ulog.Fatalf("cnivpc process(%d) has been running over a long time, will exit myself\n%s",
				os.Getpid(), stackRecord[:stackLen-1])
		}
	case <-done:
		return
	}
}

func main() {
	// Print version
	if len(os.Args) == 2 && os.Args[1] == "version" {
		vs.Show()
		os.Exit(0)
	}

	ulog.BinaryMode("/var/log/cnivpc.log")

	about := fmt.Sprintf("ucloud-uk8s-cnivpc version %s", vs.CNIVersion)

	done := make(chan bool, 1)
	go tickSuicide(done)
	skel.PluginMain(cmdAdd, cmdVersion, cmdDel, version.All, about)
	done <- true
}
