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
	"os"

	"github.com/spf13/cobra"
	"github.com/ucloud/uk8s-cni-vpc/pkg/version"
)

var cmd = &cobra.Command{
	Use:   "cnivpctl",
	Short: "CLI tools for UCloud cnivpc",

	SilenceErrors: true,
	SilenceUsage:  true,

	Version: version.CNIVersion,

	CompletionOptions: cobra.CompletionOptions{
		HiddenDefaultCmd: true,
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show cnivpctl version",

	Run: func(_ *cobra.Command, _ []string) {
		version.Show()
	},
}

func main() {
	cmd.AddCommand(versionCmd)

	err := cmd.Execute()
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
}
