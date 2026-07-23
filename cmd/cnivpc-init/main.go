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
	"io"
	"os"
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
	needReplace, err := cniInitModeFromEnv()
	if err != nil {
		return err
	}
	if needReplace {
		return copyCNIBinary(cniBinarySourcePath, cniBinaryTargetPath)
	}

	return installCNIBinaryIfMissing(cniBinarySourcePath, cniBinaryTargetPath)
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

func copyFile(src, dst string) error {
	dstTmp := dst + ".tmp"
	if err := os.Remove(dstTmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Wrapf(err, "main.copyFile remove stale destination %s", errors.Safe(dstTmp))
	}
	if err := cp(src, dstTmp); err != nil {
		return err
	}
	if err := os.Rename(dstTmp, dst); err != nil {
		return errors.Wrapf(err, "main.copyFile rename destination %s", errors.Safe(dst))
	}
	return nil
}

func cp(src, dst string) (err error) {
	sourceFileInfo, err := os.Stat(src)
	if err != nil {
		return errors.Wrapf(err, "main.cp stat source %s", errors.Safe(src))
	}
	if !sourceFileInfo.Mode().IsRegular() {
		return errors.Errorf("main.cp source %s is not a regular file", errors.Safe(src))
	}

	source, err := os.Open(src)
	if err != nil {
		return errors.Wrapf(err, "main.cp open source %s", errors.Safe(src))
	}
	defer func() {
		err = errors.CombineErrors(err, errors.Wrap(source.Close(), "main.cp close source"))
	}()

	destination, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, sourceFileInfo.Mode().Perm())
	if err != nil {
		return errors.Wrapf(err, "main.cp create destination %s", errors.Safe(dst))
	}
	defer func() {
		err = errors.CombineErrors(err, errors.Wrap(destination.Close(), "main.cp close destination"))
	}()

	if _, err := io.Copy(destination, source); err != nil {
		return errors.Wrap(err, "main.cp copy")
	}
	return nil
}
