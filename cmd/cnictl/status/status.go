package status

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/ucloud/uk8s-cni-vpc/cmd/cnictl/common"
)

var (
	all bool
)

var Command = &cobra.Command{
	Use:   "status",
	Short: "Show status for current node",

	RunE: func(_ *cobra.Command, _ []string) error {
		node := common.Node()

		data, err := json.MarshalIndent(node, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))

		return nil
	},
}

func init() {
	Command.PersistentFlags().BoolVarP(&all, "all", "a", false, "Show all details")
}
