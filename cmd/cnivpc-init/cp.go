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
	"errors"
	"fmt"
	"io"
	"os"
)

func copyFile(src, dst string) error {
	dstTmp := dst + ".tmp"
	if err := os.Remove(dstTmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("main.copyFile remove stale destination %s: %w", dstTmp, err)
	}
	if err := cp(src, dstTmp); err != nil {
		return err
	}
	if err := os.Rename(dstTmp, dst); err != nil {
		return fmt.Errorf("main.copyFile rename destination %s: %w", dst, err)
	}
	return nil
}

func cp(src, dst string) (err error) {
	sourceFileInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("main.cp stat source %s: %w", src, err)
	}
	if !sourceFileInfo.Mode().IsRegular() {
		return fmt.Errorf("main.cp source %s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("main.cp open source %s: %w", src, err)
	}
	defer func() {
		if cerr := source.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("main.cp close source: %w", cerr)
		}
	}()

	destination, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, sourceFileInfo.Mode().Perm())
	if err != nil {
		return fmt.Errorf("main.cp create destination %s: %w", dst, err)
	}
	defer func() {
		if cerr := destination.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("main.cp close destination: %w", cerr)
		}
	}()

	if _, err := io.Copy(destination, source); err != nil {
		return fmt.Errorf("main.cp copy: %w", err)
	}
	return nil
}
