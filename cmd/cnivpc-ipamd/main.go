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
	"flag"
	"os"
	"runtime"

	"github.com/ucloud/uk8s-cni-vpc/pkg/ipamd"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/pkg/version"
)

func init() {
	flag.Set("alsologtostderr", "true")
	flag.Set("log_dir", "/var/log/ucloud/")
	flag.Parse()
}

func showVersion() {
	ulog.Infof("CNI Version: " + version.CNIVersion)
	ulog.Infof("Go Version: " + runtime.Version())
	ulog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	ulog.Infof("Build Time: " + version.BuildTime)
	ulog.Infof("Git Commit ID: " + version.ProgramCommitID)
}

func main() {
	// Print version
	if len(os.Args) == 2 && os.Args[1] == "version" {
		showVersion()
		os.Exit(0)
	}

	showVersion()

	err := ipamd.Start()
	if err != nil {
		ulog.Errorf("Failed to launch ipamd service: %v", err)
		os.Exit(1)
	}
}
