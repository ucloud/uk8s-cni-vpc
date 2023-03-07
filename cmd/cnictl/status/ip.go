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
		sum, err := common.SummarizeIP()
		if err != nil {
			return err
		}

		if all {
			common.ShowObject(sum)
			return nil
		}

		fmt.Printf("Allocated: %d\n", len(sum.Allocated))
		fmt.Printf("Pods:      %d\n", sum.PodIPCount)
		fmt.Printf("Pool:      %d\n", len(sum.Pool))
		fmt.Printf("Unused:    %d\n", len(sum.Unused))

		return nil
	},
}
