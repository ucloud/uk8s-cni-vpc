// Copyright UCloud. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/cockroachdb/errors"
)

const (
	cniBinarySourcePath = "/app/cnivpc"
	cniBinaryTargetPath = "/opt/cni/bin/cnivpc"
	envCNIInitOverwrite = "CNI_INIT_OVERWRITE"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "CNI init failed: %+v\n", err)
		os.Exit(1)
	}
}

func run() error {
	same, err := checksame(cniBinarySourcePath, cniBinaryTargetPath)
	if err != nil {
		return err
	}
	if same {
		return nil
	}

	needReplace, err := cniInitModeFromEnv()
	if err != nil {
		return err
	}
	if needReplace {
		return copyCNIBinary(cniBinarySourcePath, cniBinaryTargetPath)
	}

	return installCNIBinaryIfMissing(cniBinarySourcePath, cniBinaryTargetPath)
}

func checksame(sourcePath, targetPath string) (bool, error) {
	sourceVersion, err := exec.Command(sourcePath, "--version").CombinedOutput()
	if err != nil {
		return false, errors.Wrapf(
			err,
			"main.checksame get source version from %s",
			errors.Safe(sourcePath),
		)
	}

	targetVersion, err := exec.Command(targetPath, "--version").CombinedOutput()
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.Wrapf(
			err,
			"main.checksame get target version from %s",
			errors.Safe(targetPath),
		)
	}

	return strings.TrimSpace(string(sourceVersion)) == strings.TrimSpace(string(targetVersion)), nil
}

func cniInitModeFromEnv() (bool, error) {
	value, exists := os.LookupEnv(envCNIInitOverwrite)
	if !exists {
		return false, nil
	}

	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.Errorf(
			"main.cniInitModeFromEnv parse %s value %q: expected true or false",
			errors.Safe(envCNIInitOverwrite),
			errors.Safe(value),
		)
	}
}

func installCNIBinaryIfMissing(sourcePath, targetPath string) error {
	_, err := os.Lstat(targetPath)
	switch {
	case err == nil:
		fmt.Printf("CNI binary already exists at %s; skipping\n", targetPath)
		return nil
	case !errors.Is(err, os.ErrNotExist):
		return errors.Wrapf(
			err,
			"main.installCNIBinaryIfMissing stat target %s",
			errors.Safe(targetPath),
		)
	}
	return copyCNIBinary(sourcePath, targetPath)
}

func copyCNIBinary(sourcePath, targetPath string) error {
	if err := copyFile(sourcePath, targetPath); err != nil {
		return errors.Wrap(err, "main.copyCNIBinary copy")
	}
	fmt.Printf("Installed CNI binary at %s\n", targetPath)
	return nil
}
