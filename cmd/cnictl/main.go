package main

import (
	"github.com/spf13/cobra"
	"github.com/ucloud/uk8s-cni-vpc/cmd/cnictl/common"
	"github.com/ucloud/uk8s-cni-vpc/cmd/cnictl/status"
)

var cmd = &cobra.Command{
	Use:   "cnictl",
	Short: "CNI CLI tools",

	SilenceErrors: true,
	SilenceUsage:  true,

	CompletionOptions: cobra.CompletionOptions{
		HiddenDefaultCmd: true,
	},
}

func main() {
	cmd.AddCommand(status.Command)

	err := cmd.Execute()
	if err != nil {
		common.OnError(err)
	}
}
