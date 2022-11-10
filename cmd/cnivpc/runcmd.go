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
	"os/exec"

	"github.com/containernetworking/plugins/pkg/ns"

	log "github.com/sirupsen/logrus"
	"github.com/ucloud/uk8s-cni-vpc/pkg/rpc"
)

func setNodePortRange(podName, podNS, netNS, sandBoxId string, pNet *rpc.PodNetwork) error {
	netns, err := ns.GetNS(netNS)
	if err != nil {
		log.Errorf("Failed to open netns %q: %v", netNS, err)
		releasePodIp(podName, podNS, netNS, sandBoxId, pNet)
		return fmt.Errorf("Failed to open netns %q: %v", netNS, err)
	}
	defer netns.Close()

	_ = netns.Do(func(_ ns.NetNS) error {
		cmd1 := exec.Command("bash", "-c", "sed -i 's/^.*net.ipv4.ip_local_port_range.*$/net.ipv4.ip_local_port_range = 32768 60999/' /etc/sysctl.conf")
		cmd1.Run()
		cmd2 := exec.Command("bash", "-c", "sysctl -p")
		cmd2.Run()
		return nil
	})
	return nil
}
