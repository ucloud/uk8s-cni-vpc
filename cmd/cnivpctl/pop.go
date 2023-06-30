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

var popCmd = &cobra.Command{
	Use:   "pop <NODE> [IP]",
	Short: "Pop an ip from node pool",

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

		ip := ""
		if len(args) >= 2 {
			ip = args[1]
		}

		resp, err := client.PopPool(ctx, &rpc.PopPoolRequest{
			IP: ip,
		})
		if err != nil {
			return err
		}

		fmt.Printf("Pop IP: %s\n", resp.IP.VPCIP)
		return nil
	},
}
