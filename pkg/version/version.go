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

package version

import (
	"fmt"
	"runtime"
)

// Build and version information
var (
	BuildTime       = ""
	ProgramCommitID = ""
	CNIVersion      = ""
)

func Show() {
	fmt.Println("CNI Version: \t" + CNIVersion)
	fmt.Println("Go Version: \t" + runtime.Version())
	fmt.Printf("Go OS/Arch: \t%s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println("Build Time: \t" + BuildTime)
	fmt.Println("Git Commit ID: \t" + ProgramCommitID)
}
