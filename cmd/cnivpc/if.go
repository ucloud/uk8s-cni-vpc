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
	"io/ioutil"
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

const MainTableId = 254
const DstRulePriority = 512
const SrcRulePriority = 2048

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

func cleanUpIPRoutePolicy(ip string) {
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		ulog.Errorf("ip rules list error: %v", err)
		return
	}
	for _, rule := range rules {
		if rule.Dst != nil && rule.Dst.IP.String() == ip {
			if err = netlink.RuleDel(&rule); err != nil {
				ulog.Errorf("ip rule del from all to %s err: %v", ip, err)
			}
		}
		if rule.Src != nil && rule.Src.IP.String() == ip {
			if err = netlink.RuleDel(&rule); err != nil {
				ulog.Errorf("ip rule del from %s table %d err: %v", ip, rule.Table, err)
			}
		}
	}
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

	// Modify MTU:
	// ip link set dev eth1 mtu 1452
	err = netlink.LinkSetMTU(link, defaultMtu)
	if err != nil {
		ulog.Errorf("Modify mtu for link %v error: %v", linkName, err)
		return err
	}
	h, _ := net.IPMask(net.ParseIP(netmask).To4()).Size()
	addr, err := netlink.ParseAddr(primaryIP + "/" + fmt.Sprintf("%d", h))
	if err != nil {
		return fmt.Errorf("parse addr %s failed, %v", primaryIP, err)
	}
	// Assign primary ip to interface
	// ip addr replace 10.0.2.51/24 dev eth1
	err = netlink.AddrReplace(link, addr)
	if err != nil {
		return fmt.Errorf("cannot add ip %s to dev %s: %v", addr.String(), linkName, err)
	}
	// Set link up (ip link set dev eth1 up)
	err = netlink.LinkSetUp(link)
	if err != nil {
		return fmt.Errorf("cannot set link %s up %v", linkName, err)
	}

	// Establish default route:
	// ip route replace default via 10.0.2.1 dev eth1 src 10.0.2.51 table 1001
	// use `replace` so that command do not fail if `default` route already exists
	err = netlink.RouteReplace(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Scope:     netlink.SCOPE_UNIVERSE,
		Dst:       nil,
		Gw:        net.ParseIP(gateway),
		Src:       net.ParseIP(primaryIP),
		Table:     tableId,
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

func ensureLineInFile(f, line string) error {
	// 读取整个文件内容
	content, err := ioutil.ReadFile(f)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// 检查是否已包含该行
	lines := strings.Split(string(content), "\n")
	for _, l := range lines {
		if strings.TrimSpace(l) == strings.TrimSpace(line) {
			return nil
		}
	}

	// 追加行到文件末尾
	newContent := string(content)
	if len(newContent) > 0 && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	newContent += line + "\n"

	// 写入文件
	return ioutil.WriteFile(f, []byte(newContent), 0644)
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
