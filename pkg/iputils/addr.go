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

package iputils

import (
	"fmt"
	"net"
	"strings"

	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/vishvananda/netlink"
)

const (
	UHostMasterInterface  = "eth0"
	UPHostMasterInterface = "eth2"
)

func GetMasterInterface() string {
	list, err := net.Interfaces()
	if err != nil {
		ulog.Errorf("List interfaces in root network namespace error: %v", err)
		return UHostMasterInterface
	}

	meta, err := uapi.GetMeta()
	if err != nil {
		ulog.Errorf("Get uhost metadata error: %v", err)
		return UHostMasterInterface
	}

	var targetMac string
	// 裸金属根据mac地址匹配网卡
	if strings.HasPrefix(meta.InstanceId, "upm") && len(meta.UPHost.NetworkInterfaces) > 0 {
		mac := meta.UPHost.NetworkInterfaces[0].Mac
		targetMac = strings.ToLower(mac)
	} else {
		// 云主机直接返回eth0
		return UHostMasterInterface
	}

	for _, iface := range list {
		if iface.HardwareAddr.String() == targetMac {
			return iface.Name
		}
	}
	return UPHostMasterInterface
}

func GetLinkByMac(mac string) (netlink.Link, error) {
	var links []netlink.Link
	var err error
	links, err = netlink.LinkList()
	if err != nil {
		ulog.Errorf("List all interfaces error: %v", err)
		return nil, err
	}

	for _, link := range links {
		if strings.ToLower(link.Attrs().HardwareAddr.String()) == strings.ToLower(mac) {
			return link, nil
		}
	}

	return nil, fmt.Errorf("cannot find interface of mac %s", mac)
}

// Get node master network interface ip and mac address
func GetNodeAddress(dev string) (string, string, error) {
	if dev == "" {
		dev = GetMasterInterface()
	}
	iface, err := net.InterfaceByName(dev)
	if err != nil {
		return "", "", err
	}
	mac := strings.ToUpper(iface.HardwareAddr.String())
	addrs, err := iface.Addrs()
	if err != nil {
		return "", "", fmt.Errorf("failed to get addrs for iface: %v", err)
	}
	if len(addrs) != 1 {
		return "", "", fmt.Errorf("invalid iface addr count, expect 1, found %d", len(addrs))
	}
	ip, _, err := net.ParseCIDR(addrs[0].String())
	if err != nil {
		return "", "", fmt.Errorf("failed to parse CIDR: %v", err)
	}
	return ip.String(), mac, nil
}

func GetNodeIPAddress(dev string) (*netlink.Addr, error) {
	if dev == "" {
		dev = GetMasterInterface()
	}
	hface, err := netlink.LinkByName(dev)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup %s: %v", dev, err)
	}
	hostAddrs, err := netlink.AddrList(hface, netlink.FAMILY_V4)
	if err != nil {
		return nil, fmt.Errorf("failed to get host ip addresses for %q: %v", hface, err)
	}
	if len(hostAddrs) == 0 {
		return nil, fmt.Errorf("host ip addresses empty")
	}

	return &(hostAddrs[0]), nil
}

func GetNodeMacAddress(dev string) (string, error) {
	if dev == "" {
		dev = GetMasterInterface()
	}
	iface, err := net.InterfaceByName(dev)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(iface.HardwareAddr.String()), nil
}
