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
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/ucloud/ucloud-sdk-go/services/vpc"
	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
	"github.com/ucloud/uk8s-cni-vpc/rpc"
	"google.golang.org/grpc"
)

var (
	addNum  int
	addPool bool
)

var addCmd = &cobra.Command{
	Use:   "add [IP]",
	Short: "Allocate Secondary IP",

	Args: cobra.MaximumNArgs(1),

	RunE: func(_ *cobra.Command, args []string) error {
		ip := ""
		if len(args) >= 1 {
			ip = args[0]
		}
		if addNum <= 0 {
			return fmt.Errorf("Invalid add num %d", addNum)
		}

		ips, err := allocateIP(ip, addNum)
		if err != nil {
			return err
		}

		if addPool {
			conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
			if err != nil {
				return fmt.Errorf("Dial to ipamd error: %v", err)
			}
			defer conn.Close()
			client := rpc.NewCNIIpamClient(conn)
			ctx := context.Background()
			req := &rpc.AddPoolRecordRequest{
				Records: make([]*rpc.AddPoolRecord, len(ips)),
			}
			for i, ip := range ips {
				req.Records[i] = &rpc.AddPoolRecord{
					Gateway:  ip.Gateway,
					IP:       ip.Ip,
					Mac:      ip.Mac,
					Mask:     ip.Mask,
					SubnetID: ip.SubnetId,
					VPCID:    ip.VPCId,
				}
			}
			_, err = client.AddPoolRecord(ctx, req)
			if err != nil {
				return fmt.Errorf("Call ipamd to add pool record error: %v", err)
			}
			fmt.Println("Add ip to pool done")
		}

		return nil
	},
}

func init() {
	addCmd.PersistentFlags().IntVarP(&addNum, "num", "n", 1, "The number of IP to add")
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

	objectID, err := uapi.GetObjectIDForSecondaryIP()
	if err != nil {
		return nil, fmt.Errorf("Get object id error: %v", err)
	}

	req := vpcClient.NewAllocateSecondaryIpRequest()
	req.Mac = ucloud.String(mac)
	req.VPCId = ucloud.String(client.VPCID())
	req.SubnetId = ucloud.String(client.SubnetID())
	req.ObjectId = ucloud.String(objectID)
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
