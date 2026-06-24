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
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/coreos/go-iptables/iptables"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"

	"github.com/j-keck/arping"
	"github.com/vishvananda/netlink"
)

const (
	MainTableId      = 254
	DstRulePriority  = 512
	MarkRulePriority = 1024
	SrcRulePriority  = 2048

	// defaultConnmark is the default value for the connmark described above.  Note: the mark space is a little crowded,
	// - kube-proxy uses 0x0000c000
	// - Calico uses 0xffff0000.
	defaultConnmark = 0x80

	rpFilterOff    = "0"
	rpFilterStrict = "1"
	rpFilterLoose  = "2"
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

// ip rule add from 10.0.2.51 to <private-cidr> table 1002
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
		return fmt.Errorf("list ip routes error: %v", err)
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

	// Delete uni routes in other tables
	routes, err = netlink.RouteList(link, netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if route.Table == tableId {
			continue
		}
		netlink.RouteDel(&route)
	}

	// ensure rp_filter=1
	if err := ensureUNIRpFilter(linkName); err != nil {
		return fmt.Errorf("failed to ensure %s rp_filter: %v", linkName, err)
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

func ensureAllRPFilterOff() error {
	// Turn off reverse path filter according to https://docs.ucloud.cn/vpc/guide/uni
	// Seemingly useless setting
	rpFilterCnfFile := "/proc/sys/net/ipv4/conf/all/rp_filter"
	return setProcSys(rpFilterCnfFile, rpFilterOff)
}

func ensureUNIRpFilter(ifName string) error {
	// on ubuntu 20/22/24 default value is 2, change it to 1
	// otherwise garp ip conflict detection(rfc5227) would fail
	// on centos 7 default value is already 1
	rpFilterKey := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", ifName)
	return setProcSys(rpFilterKey, rpFilterStrict)
}

func ensureMasterInterfaceRpFilter(masterInterface string) error {
	// This is required because
	// NodePorts are exposed on eth0
	// The kernel's RPF check happens after incoming packets to NodePorts are DNATted to
	// the pod IP. For pods assigned to UNIs, the routing table includes source-based
	// routing.  When the kernel does the RPF check, it looks up the route using the pod
	// IP as the source. Thus, it finds the source-based route that leaves via the UNI.
	// In "strict" mode, the RPF check fails because the return path uses a different
	// interface to the incoming packet.  In "loose" mode, the check passes because some
	// route was found.
	rpFilterKey := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", masterInterface)
	return setProcSys(rpFilterKey, rpFilterLoose)
}

const (
	connmarkChainName = "UCLOUD-CONNMARK-CHAIN-0"
	snatChainName     = "UCLOUD-SNAT-CHAIN-0"
)

type iptablesRule struct {
	name        string
	shouldExist bool
	table       string
	chain       string
	rule        []string
}

func (r iptablesRule) String() string {
	return fmt.Sprintf("IptablesRule {name: %s, shouldExist: %t, table: %s, chain: %s, rule: %v}", r.name, r.shouldExist, r.table, r.chain, r.rule)
}

func updateIptablesRules(rules []iptablesRule, ipt *iptables.IPTables) error {
	for _, rule := range rules {
		exists, err := ipt.Exists(rule.table, rule.chain, rule.rule...)
		ulog.Infof("Execute iptable rule: %v, exists: %v, err: %v", rule, exists, err)
		if err != nil {
			return fmt.Errorf("failed to check iptables rule %q exists: %v", rule.name, err)
		}

		if !exists && rule.shouldExist {
			if rule.name == connmarkChainName || rule.name == snatChainName {
				// All CIDR rules must go before the SNAT/Mark rule
				err = ipt.Insert(rule.table, rule.chain, 1, rule.rule...)
				if err != nil {
					return fmt.Errorf("failed to insert iptables rule %q: %v", rule.name, err)
				}
				continue
			}

			err = ipt.Append(rule.table, rule.chain, rule.rule...)
			if err != nil {
				return fmt.Errorf("failed to append iptables rule %q: %v", rule.name, err)
			}
			continue
		}

		if exists && !rule.shouldExist {
			err = ipt.Delete(rule.table, rule.chain, rule.rule...)
			if err != nil {
				return fmt.Errorf("failed to delete iptables rule %q: %v", rule.name, err)
			}
		}

	}
	return nil
}

// RFC 1918
var privateNetworks = []string{
	"10.0.0.0/8",     // A class private network
	"172.16.0.0/12",  // B class private network
	"192.168.0.0/16", // C class private network
}

type iptablesRulesManager struct {
	ipt *iptables.IPTables

	vpcCIDRs        []string
	primaryIP       string
	primayInterface string
}

func newIptablesRulesManager(primaryIP, primaryInterface string) (*iptablesRulesManager, error) {
	ipt, err := iptables.New()
	if err != nil {
		return nil, err
	}

	return &iptablesRulesManager{
		ipt:             ipt,
		vpcCIDRs:        privateNetworks,
		primaryIP:       primaryIP,
		primayInterface: primaryInterface,
	}, nil
}

func (m *iptablesRulesManager) updateRules() error {
	snatRules, err := m.buildSNATRules()
	if err != nil {
		return err
	}
	err = updateIptablesRules(snatRules, m.ipt)
	if err != nil {
		return err
	}

	connmarkRules, err := m.buildConnmarkRules()
	if err != nil {
		return err
	}
	err = updateIptablesRules(connmarkRules, m.ipt)
	if err != nil {
		return err
	}

	return nil
}

func (m *iptablesRulesManager) buildSNATRules() ([]iptablesRule, error) {
	err := m.ensureChain("nat", snatChainName)
	if err != nil {
		return nil, err
	}

	rules := make([]iptablesRule, 0)

	// build SNAT rules for outbound non-VPC traffic
	rules = append(rules, iptablesRule{
		name:        "first SNAT rules for non-VPC outbound traffic",
		shouldExist: true,
		table:       "nat",
		chain:       "POSTROUTING",
		rule: []string{
			"-m", "comment", "--comment", "UCLOUD SNAT CHAIN", "-j", snatChainName,
		},
	})

	// Exclude VPC traffic from SNAT rule
	for _, cidr := range m.vpcCIDRs {
		rules = append(rules, iptablesRule{
			name:        snatChainName,
			shouldExist: true,
			table:       "nat",
			chain:       snatChainName,
			rule: []string{
				"-d", cidr, "-m", "comment", "--comment", "UCLOUD SNAT CHAIN", "-j", "RETURN",
			},
		})
	}

	rules = append(rules, iptablesRule{
		name:        "SNAT rule for non-VPC outbound traffic",
		shouldExist: true,
		table:       "nat",
		chain:       snatChainName,
		rule: []string{
			"-o", m.primayInterface,
			"-m", "comment", "--comment", "UCLOUD SNAT",
			"-m", "addrtype", "!", "--dst-type", "LOCAL",
			"-j", "SNAT", "--to-source", m.primaryIP,
		},
	})

	rules = append(rules, iptablesRule{
		name:        "connmark for primary interface",
		shouldExist: true,
		table:       "mangle",
		chain:       "PREROUTING",
		rule: []string{
			"-m", "comment", "--comment", "UCLOUD primary UNI",
			"-i", m.primayInterface,
			"-m", "addrtype", "--dst-type", "LOCAL", "--limit-iface-in",
			"-j", "CONNMARK", "--set-mark", fmt.Sprintf("%#x/%#x", defaultConnmark, defaultConnmark),
		},
	})

	rules = append(rules, iptablesRule{
		name:        "connmark restore for veth",
		shouldExist: true,
		table:       "mangle",
		chain:       "PREROUTING",
		rule: []string{
			"-m", "comment", "--comment", "UCLOUD primary UNI",
			"-i", "ucni+", "-j", "CONNMARK", "--restore-mark", "--mask", fmt.Sprintf("%#x", defaultConnmark),
		},
	})

	return rules, nil
}

func (m *iptablesRulesManager) buildConnmarkRules() ([]iptablesRule, error) {
	err := m.ensureChain("nat", connmarkChainName)
	if err != nil {
		return nil, err
	}

	rules := make([]iptablesRule, 0)

	rule := iptablesRule{
		name:        "connmark rule for non-VPC outbound traffic",
		shouldExist: true,
		table:       "nat",
		chain:       "PREROUTING",
		rule: []string{
			"-i", "ucni+", "-m", "comment", "--comment", "UCLOUD outbound connections",
			"-m", "state", "--state", "NEW", "-j", connmarkChainName,
		},
	}
	// Force delete legacy rule: the rule was matching on "-m state --state NEW", which is
	// always true for packets traversing the nat table
	deleteRule := rule
	deleteRule.shouldExist = false
	rules = append(rules, deleteRule)
	rules = append(rules, rule)

	for _, cidr := range m.vpcCIDRs {
		rules = append(rules, iptablesRule{
			name:        connmarkChainName,
			shouldExist: true,
			table:       "nat",
			chain:       connmarkChainName,
			rule: []string{
				"-d", cidr, "-m", "comment", "--comment", "UCLOUD CONNMARK CHAIN, VPC CIDR", "-j", "RETURN",
			},
		})
	}

	rules = append(rules, iptablesRule{
		name:        "connmark rule for external outbound traffic",
		shouldExist: true,
		table:       "nat",
		chain:       connmarkChainName,
		rule: []string{
			"-m", "comment", "--comment", "UCLOUD CONNMARK", "-j", "CONNMARK",
			"--set-xmark", fmt.Sprintf("%#x/%#x", defaultConnmark, defaultConnmark),
		},
	})

	// Being in the nat table, this only applies to the first packet of the connection. The mark
	// will be restored in the mangle table for subsequent packets.
	rule = iptablesRule{
		name:        "connmark to fwmark copy",
		shouldExist: true,
		table:       "nat",
		chain:       "PREROUTING",
		rule: []string{
			"-m", "comment", "--comment", "UCLOUD CONNMARK", "-j", "CONNMARK",
			"--restore-mark", "--mask", fmt.Sprintf("%#x", defaultConnmark),
		},
	}
	// Force delete existing restore mark rule so that the subsequent rule gets added to the end
	deleteRule = rule
	deleteRule.shouldExist = false
	rules = append(rules, deleteRule)
	rules = append(rules, rule)

	return rules, nil
}

func (m *iptablesRulesManager) ensureChain(table, chain string) error {
	err := m.ipt.NewChain(table, chain)
	if err != nil {
		if containChainExistErr(err) {
			return nil
		}
		return fmt.Errorf("failed to create iptables chain %s in table %s: %v", chain, table, err)
	}
	return nil
}

func containChainExistErr(err error) bool {
	return strings.Contains(err.Error(), "Chain already exists")
}

func ensureConnmarkRule() error {
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("ip rules list error: %v", err)
	}

	var exists bool
	for _, rule := range rules {
		if rule.Mask == defaultConnmark && rule.Mark == defaultConnmark && rule.Table == MainTableId && rule.Priority == MarkRulePriority {
			exists = true
			break
		}
	}

	if !exists {
		rule := netlink.NewRule()
		rule.Mask = defaultConnmark
		rule.Mark = defaultConnmark
		rule.Table = MainTableId
		rule.Priority = MarkRulePriority

		return netlink.RuleAdd(rule)
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

func setProcSys(key, value string) error {
	return os.WriteFile(key, []byte(value), 0644)
}
