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
	"github.com/ucloud/uk8s-cni-vpc/rpc"
)

var releaseCmd = &cobra.Command{
	Use:   "release <NODE> [IP]",
	Short: "Release unused IP (dangerous operation!!!)",

	Args: cobra.RangeArgs(1, 2),

	RunE: func(_ *cobra.Command, args []string) error {
		name := args[0]

		node, err := GetNode(name)
		if err != nil {
			return err
		}

		client, err := node.Dial()
		if err != nil {
			return err
		}
		defer node.Close()

		ctx := context.Background()
		if len(args) >= 2 {
			ip := args[1]
			Confirm("Are you sure to release %q", ip)
			_, err = client.ReleaseIP(ctx, &rpc.ReleaseIPRequest{
				IP: []string{ip},
			})
			return err
		}

		resp, err := client.ListUnuse(ctx, &rpc.ListUnuseRequest{})
		if err != nil {
			return err
		}
		if len(resp.Unuse) == 0 {
			fmt.Println("No IP to release")
			return nil
		}

		fmt.Println("About to release:")
		ips := make([]string, len(resp.Unuse))
		for i, ip := range resp.Unuse {
			fmt.Println(ip.VPCIP)
			ips[i] = ip.VPCIP
		}
		Confirm("Continue")

		_, err = client.ReleaseIP(ctx, &rpc.ReleaseIPRequest{
			IP: ips,
		})
		if err != nil {
			return err
		}

		fmt.Printf("Done, released %d IP(s)\n", len(ips))
		return nil
	},
}
