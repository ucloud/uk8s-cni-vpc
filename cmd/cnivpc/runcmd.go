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
