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
