package ipamd

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"strings"

	"github.com/vishvananda/netlink"
	"k8s.io/klog/v2"
)

type CNIVPCConf struct {
	CNIVersion           string          `json:"cniVersion"`
	Name                 string          `json:"name"`
	Type                 string          `json:"type"`
	MasterInterface      string          `json:"masterInterface"`
	Capabilities         map[string]bool `json:"capabilities"`
	ExternalSetMarkChain string          `json:"externalSetMarkChain"`
	AllocateIpByIpamd    string          `json:"allocateIpByIpamd"`
}

// Get node master network interface mac address.
// By setting pod spec hostNetwork:true, we can get mac addresses in host network namespace.
func getNodeMacAddress(dev string) (string, error) {
	i, e := net.InterfaceByName(dev)
	if e != nil {
		return "", e
	}
	return strings.ToUpper(i.HardwareAddr.String()), nil
}

func getNodeIPAddress(dev string) (*netlink.Addr, error) {
	hface, err := netlink.LinkByName(dev)
	if err != nil {
		klog.Errorf("Failed to lookup %s: %v", dev, err)
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

func hostType(resourceId string) string {
	if strings.HasPrefix(resourceId, "uhost-") {
		return "UHost"
	} else if strings.HasPrefix(resourceId, "upm-") {
		return "UPM"
	} else if strings.HasPrefix(resourceId, "docker-") {
		return "UDocker"
	} else if strings.HasPrefix(resourceId, "udhost-") {
		return "UDHost"
	}
	return "UHost"
}

// Generate conf file 10-cnivpc.conf, if node is UPHost, master network interface will be net1 instead of eth0.
func GenerateConfFile(ipamd bool) error {
	allocateIpByIpamd := "false"
	if ipamd {
		allocateIpByIpamd = "true"
	}
	conf := &CNIVPCConf{
		CNIVersion:           "0.3.1",
		Name:                 "cni-vpc-uk8s",
		Type:                 "cnivpc",
		MasterInterface:      getMasterInterface(),
		Capabilities:         map[string]bool{"portMappings": true},
		ExternalSetMarkChain: "KUBE-MARK-MASQ",
		AllocateIpByIpamd:    allocateIpByIpamd,
	}

	content, err := json.Marshal(conf)
	if err != nil {
		klog.Errorf("Unable to marshal 10-cnivpc.conf json, %v", err)
		return err
	}
	return ioutil.WriteFile("/app/10-cnivpc.conf", content, 0644)
}

func getMasterInterface() string {
	list, err := net.Interfaces()
	if err != nil {
		klog.Errorf("Unable to list interfaces in root network namespace, %v", err)
		return UHostMasterInterface
	}

	for _, iface := range list {
		if iface.Name == UPHostMasterInterface {
			return UPHostMasterInterface
		}
	}
	return UHostMasterInterface
}

func LoadCNIVPCConf() (*CNIVPCConf, error) {
	file := "/opt/cni/net.d/10-cnivpc.conf"
	filePtr, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer filePtr.Close()
	var conf CNIVPCConf
	decoder := json.NewDecoder(filePtr)
	err = decoder.Decode(&conf)
	if err != nil {
		return nil, err
	}
	return &conf, nil
}

func pathExist(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil || os.IsExist(err)
}

func InstallCNIComponent(src, dst string) error {
	klog.Infof("Copy %s to %s", src, dst)
	return copyFileContents(src, dst)
}

// copyFileContents copies a file
func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		e := out.Close()
		if err == nil {
			err = e
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	err = out.Sync()
	if err != nil {
		return err
	}
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	err = os.Chmod(dst, si.Mode())
	if err != nil {
		return err
	}
	return err
}
