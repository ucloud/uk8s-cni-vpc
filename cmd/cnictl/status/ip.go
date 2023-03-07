package status

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/ucloud/uk8s-cni-vpc/cmd/cnictl/common"
)

func init() {
	Command.AddCommand(ip)
}

var ip = &cobra.Command{
	Use:   "ip [-a]",
	Short: "Show ip status",

	RunE: func(_ *cobra.Command, _ []string) error {
		podList, err := common.ListPods()
		if err != nil {
			return err
		}

		allocatedIPs, err := common.ListSecondaryIP()
		if err != nil {
			return err
		}

		fmt.Printf("Allocated: %d\n", len(allocatedIPs))
		fmt.Printf("Pods:      %d\n", len(podList.Items))

		return nil
	},
}
