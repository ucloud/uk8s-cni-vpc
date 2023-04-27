package iputils

import (
	"fmt"
	"net"
	"strings"

	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/vishvananda/netlink"
)

const (
	UHostMasterInterface  = "eth0"
	UPHostMasterInterface = "net1"
)

func GetMasterInterface() string {
	list, err := net.Interfaces()
	if err != nil {
		ulog.Errorf("Unable to list interfaces in root network namespace, %v", err)
		return UHostMasterInterface
	}

	for _, iface := range list {
		if iface.Name == UPHostMasterInterface {
			return UPHostMasterInterface
		}
	}
	return UHostMasterInterface
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
