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
	"io"
	"net"
	"os"
	"strings"

	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
	"github.com/vishvananda/netlink"
	"k8s.io/klog/v2"
)

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

func getMasterInterface() string {
	list, err := net.Interfaces()
	if err != nil {
		klog.Errorf("Unable to list interfaces in root network namespace, %v", err)
		return UHostMasterInterface
	}

	uapiClient, _ := uapi.NewClient()
	resource := uapiClient.InstanceID()
	if strings.HasPrefix(resource, "upm-") {
		for _, iface := range list {
			if iface.Name == UPHostMasterInterface {
				return UPHostMasterInterface
			}
		}
	}
	return UHostMasterInterface
}

func pathExist(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil || os.IsExist(err)
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
