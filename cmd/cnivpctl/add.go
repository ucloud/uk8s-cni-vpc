package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/ucloud/ucloud-sdk-go/services/vpc"
	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
)

var (
	addCount int
	addPool  bool
)

var addCmd = &cobra.Command{
	Use:   "add [IP]",
	Short: "Allocate Secondary IP",

	Args: cobra.MaximumNArgs(1),

	RunE: func(_ *cobra.Command, args []string) error {
		return nil
	},
}

func init() {
	addCmd.PersistentFlags().IntVarP(&addCount, "num", "n", 1, "The number of IP to add")
	addCmd.PersistentFlags().BoolVarP(&addPool, "pool", "p", false, "Add IP to ipamd pool")
}

func allocateIP(ip string, n int) ([]*vpc.IpInfo, error) {
	client, err := uapi.NewClient()
	if err != nil {
		return nil, fmt.Errorf("Create ucloud client error: %v", err)
	}
	vpcClient, err := client.VPCClient()
	if err != nil {
		return nil, fmt.Errorf("Create vpc client error: %v", err)
	}

	mac, err := iputils.GetNodeMacAddress("")
	if err != nil {
		return nil, fmt.Errorf("Get mac address error: %v", err)
	}

	req := vpcClient.NewAllocateSecondaryIpRequest()
	req.Mac = ucloud.String(mac)
	req.VPCId = ucloud.String(client.VPCID())
	req.SubnetId = ucloud.String(client.SubnetID())
	if ip != "" {
		req.Ip = ucloud.String(ip)
		n = 1
	}

	ips := make([]*vpc.IpInfo, 0, n)
	for i := 0; i < n; i++ {
		resp, err := vpcClient.AllocateSecondaryIp(req)
		if err != nil {
			return nil, fmt.Errorf("Call UCloud API to allocate secondary ip error: %v", err)
		}

		fmt.Printf("Allocate IP: %s\n", resp.IpInfo.Ip)
		ips = append(ips, &(resp.IpInfo))
	}

	return ips, nil
}
