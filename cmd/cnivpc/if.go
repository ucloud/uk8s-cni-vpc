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
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"

	"github.com/j-keck/arping"
	"github.com/vishvananda/netlink"
)

const (
	MainTableId = 254

	DstRulePriority  = 512
	HostRulePriority = 1024
	SrcRulePriority  = 2048
)

// ip rule add from all to 10.0.2.51 table main
func ensureDstIPRoutePolicy(ip string) error {
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("ip rules list error: %v", err)
	}
	for _, rule := range rules {
		if rule.Dst != nil && rule.Dst.IP.String() == ip {
			if rule.Table == MainTableId && rule.Src == nil {
				return nil
			} else {
				netlink.RuleDel(&rule)
			}
		}
	}

	rule := netlink.NewRule()
	rule.Priority = DstRulePriority
	rule.Table = MainTableId
	rule.Dst = netlink.NewIPNet(net.ParseIP(ip))
	err = netlink.RuleAdd(rule)
	if err != nil {
		return fmt.Errorf("fail to add ip rule from all to %s table main: %v", ip, err)
	}
	ulog.Infof("Add ip rule from all to %s table main success", ip)
	return nil
}

// ip rule add from 10.0.2.51 table 1002
func ensureSrcIPRoutePolicy(ip, ifname string) error {
	tableId, err := ifNameToTableId(ifname)
	if err != nil {
		return fmt.Errorf("cannot convert link name %s to number: %v", ifname, err)
	}

	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("ip rules list error: %v", err)
	}
	for _, rule := range rules {
		if rule.Src != nil && rule.Src.IP.String() == ip {
			if rule.Table == tableId && rule.Dst == nil {
				return nil
			} else {
				netlink.RuleDel(&rule)
			}
		}
	}

	rule := netlink.NewRule()
	rule.Priority = SrcRulePriority
	rule.Table = tableId
	rule.Src = netlink.NewIPNet(net.ParseIP(ip))
	err = netlink.RuleAdd(rule)
	if err != nil {
		return fmt.Errorf("fail to add ip rule from %s table %d: %v", ip, tableId, err)
	}
	ulog.Infof("Add ip rule from %s table %d success", ip, tableId)
	return nil
}

func cleanUpIPRoutePolicy(ip string) error {
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("ip rules list error: %v", err)
	}
	for _, rule := range rules {
		if rule.Dst != nil && rule.Dst.IP.String() == ip {
			if err = netlink.RuleDel(&rule); err != nil {
				return fmt.Errorf("ip rule del from all to %s err: %v", ip, err)
			}
		}
		if rule.Src != nil && rule.Src.IP.String() == ip {
			if err = netlink.RuleDel(&rule); err != nil {
				return fmt.Errorf("ip rule del from %s table %d err: %v", ip, rule.Table, err)
			}
		}
	}
	return nil
}

func ensureUNIPrimaryIPRoute(primaryIP, mac, gateway, netmask string) error {
	link, err := iputils.GetLinkByMac(mac)
	if err != nil {
		return err
	}
	linkName := link.Attrs().Name
	tableId, err := ifNameToTableId(linkName)
	if err != nil {
		return fmt.Errorf("cannot convert link name %s to number: %v", linkName, err)
	}
	if err = ensureSrcIPRoutePolicy(primaryIP, linkName); err != nil {
		return err
	}

	routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("List ip routes error: %v", err)
	}
	// uni primary ip route setup is ok
	for _, route := range routes {
		if route.Gw.String() == gateway && route.Table == tableId {
			return nil
		}
	}

	// Turn off reverse path filter
	if err := ensureRPFilterOff(); err != nil {
		return err
	}
	// Modify MTU:
	// ip link set dev eth1 mtu 1452
	err = netlink.LinkSetMTU(link, defaultMtu)
	if err != nil {
		ulog.Errorf("Modify mtu for link %v error: %v", linkName, err)
		return err
	}

	// Set link up (ip link set dev eth1 up)
	err = netlink.LinkSetUp(link)
	if err != nil {
		return fmt.Errorf("cannot set link %s up %v", linkName, err)
	}

	// Establish gateway route:
	// ip route replace 10.0.2.1 dev eth1 scope link table 1001
	// use `replace` so that command do not fail if `default` route already exists
	err = netlink.RouteReplace(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Scope:     netlink.SCOPE_LINK,
		Dst:       netlink.NewIPNet(net.ParseIP(gateway)),
		Table:     tableId,
	})
	if err != nil {
		return fmt.Errorf("unable to add route to gateway %s on table %d: %v", gateway, tableId, err)
	}

	// Establish default route:
	// ip route replace default via 10.0.2.1 dev eth1 table 1001
	err = netlink.RouteReplace(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Scope:     netlink.SCOPE_UNIVERSE,
		Dst:       nil,
		Gw:        net.ParseIP(gateway),
		// Src:       net.ParseIP(primaryIP),
		Table: tableId,
	})
	if err != nil {
		return fmt.Errorf("unable to add default route to gateway %s on table %d: %v", gateway, tableId, err)
	}
	ulog.Infof("Add route default via %s dev %s table %d success", gateway, linkName, tableId)

	// Send a gratuitous arp, using hardware address of UNI
	for i := 0; i < 3; i++ {
		_ = arping.GratuitousArpOverIfaceByName(net.ParseIP(primaryIP), linkName)
		if i != 2 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return nil
}

func ensureRPFilterOff() error {
	rpFilterCnfFile := "/proc/sys/net/ipv4/conf/all/rp_filter"
	err := os.Truncate(rpFilterCnfFile, 0)
	if err != nil {
		ulog.Errorf("Truncate file %s error: %v", rpFilterCnfFile, err)
		return err
	}
	f, err := os.OpenFile(rpFilterCnfFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	defer f.Close()
	if err != nil {
		ulog.Errorf("Open file %s error: %v", rpFilterCnfFile, err)
		return err
	}
	io.WriteString(f, "0")
	return nil
}

// Host rule is used to traffic outside packets (ip rule not to <VPC's subnet>) to table main
// >> ip rule add not from all to 10.0.0.0/16 lookup main
func ensureHostRulePolicy(networks []string) error {
	networksSet := make(map[string]struct{}, len(networks))
	for _, network := range networks {
		networksSet[network] = struct{}{}
	}

	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return err
	}

	existsNetworks := make(map[string]struct{}, len(networks))
	for _, rule := range rules {
		if rule.Priority != HostRulePriority || rule.Table != MainTableId || rule.Dst == nil {
			// This is not a host rule
			continue
		}
		dst := rule.Dst.String()
		if _, ok := networksSet[dst]; !ok {
			ulog.Infof("Delete host rule for vpc network %q", dst)
			err = netlink.RuleDel(&rule)
			if err != nil {
				return err
			}
			continue
		}
		existsNetworks[dst] = struct{}{}
	}

	for _, network := range networks {
		if _, ok := existsNetworks[network]; ok {
			continue
		}
		rule := netlink.NewRule()
		rule.Priority = HostRulePriority
		rule.Table = MainTableId
		ipnet, err := netlink.ParseIPNet(network)
		if err != nil {
			return fmt.Errorf("parse network %q error: %v", network, err)
		}
		rule.Dst = ipnet
		rule.Invert = true // not from all to <VPC's subnet>

		ulog.Infof("Add host rule for vpc network %q", network)
		err = netlink.RuleAdd(rule)
		if err != nil {
			return fmt.Errorf("add host rule %q error: %v", network, err)
		}
	}

	return nil
}

// eth1 => 1001, eth15 => 1015
func ifNameToTableId(s string) (int, error) {
	var numStr strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			numStr.WriteRune(r)
		}
	}
	num, err := strconv.Atoi(numStr.String())
	if err != nil {
		return 0, err
	}
	return 1000 + num, nil
}
