package main

import (
	"fmt"
	"os"

	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
)

func releaseIp(ip string) error {
	macAddr, err := iputils.GetNodeMacAddress("")
	if err != nil {
		return fmt.Errorf("failed to get mac addr: %w", err)
	}

	uapiClient, err := uapi.NewClient()
	if err != nil {
		return fmt.Errorf("failed to init uapi client: %w", err)
	}

	vpcClient, err := uapiClient.VPCClient()
	if err != nil {
		return fmt.Errorf("failed to init vpc client: %w", err)
	}

	req := vpcClient.NewDeleteSecondaryIpRequest()
	req.VPCId = ucloud.String(uapiClient.VPCID())
	req.SubnetId = ucloud.String(uapiClient.SubnetID())
	req.Ip = ucloud.String(ip)
	req.Mac = ucloud.String(macAddr)

	_, err = vpcClient.DeleteSecondaryIp(req)
	return err
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: release-ip <ip>")
		os.Exit(1)
	}

	ip := os.Args[1]
	err := releaseIp(ip)
	if err != nil {
		fmt.Printf("failed to release ip %v: %v\n", ip, err)
		os.Exit(1)
	}

	fmt.Printf("released ip %v successfully\n", ip)
}
