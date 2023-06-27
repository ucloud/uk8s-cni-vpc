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

	"github.com/spf13/cobra"
	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
)

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Delete secondary ips that are not used",

	Args: cobra.MaximumNArgs(1),

	RunE: func(_ *cobra.Command, args []string) error {
		if len(args) >= 1 {
			ip := args[0]
			return releaseIP(ip)
		}
		ips, err := getSecondaryIP()
		if err != nil {
			return err
		}

		unused := make([]string, 0)
		for _, ip := range ips {
			if ip.Unused {
				unused = append(unused, ip.IP)
			}
		}
		if len(unused) == 0 {
			fmt.Println("No IP to release")
			return nil
		}

		fmt.Println("Unused IP:")
		for _, ip := range unused {
			fmt.Println(ip)
		}

		var desc string
		if len(unused) == 1 {
			desc = "this 1 IP"
		} else {
			desc = fmt.Sprintf("these %v IPs", len(unused))
		}

		fmt.Printf("Do you really want to release %s (y/n) ", desc)
		var confirm string
		fmt.Scanf("%s", &confirm)
		if confirm != "y" {
			return nil
		}

		for _, ip := range unused {
			err = releaseIP(ip)
			if err != nil {
				return err
			}
		}

		return nil
	},
}

func releaseIP(ip string) error {
	client, err := uapi.NewClient()
	if err != nil {
		return fmt.Errorf("Create ucloud client error: %v", err)
	}
	vpcClient, err := client.VPCClient()
	if err != nil {
		return fmt.Errorf("Create vpc client error: %v", err)
	}

	mac, err := iputils.GetNodeMacAddress("")
	if err != nil {
		return fmt.Errorf("Get mac address error: %v", err)
	}

	req := vpcClient.NewDeleteSecondaryIpRequest()
	req.VPCId = ucloud.String(client.VPCID())
	req.SubnetId = ucloud.String(client.SubnetID())
	req.Mac = ucloud.String(mac)
	req.Ip = ucloud.String(ip)

	_, err = vpcClient.DeleteSecondaryIp(req)
	if err != nil {
		return fmt.Errorf("Call UCloud API to release secondary ip error: %v", err)
	}
	fmt.Printf("Release IP: %s\n", ip)

	return nil
}
