package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var cmd = &cobra.Command{
	Use:   "cnivpctl",
	Short: "CLI tools for UCloud cnivpc",

	SilenceErrors: true,
	SilenceUsage:  true,

	CompletionOptions: cobra.CompletionOptions{
		HiddenDefaultCmd: true,
	},
}

func main() {
	cmd.AddCommand(getCmd)

	err := cmd.Execute()
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
}
