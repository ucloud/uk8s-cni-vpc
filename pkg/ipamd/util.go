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

package ipamd

import (
	"io"
	"os"
	"strings"
)

func hostType(resourceId string) string {
	if strings.HasPrefix(resourceId, "uhost-") {
		return "UHost"
	} else if strings.HasPrefix(resourceId, "upm-") {
		return "UPM"
	} else if strings.HasPrefix(resourceId, "docker-") {
		return "UDocker"
	} else if strings.HasPrefix(resourceId, "udhost-") {
		return "UDHost"
	}
	return "UHost"
}

func pathExist(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil || os.IsExist(err)
}

// copyFileContents copies a file
func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		e := out.Close()
		if err == nil {
			err = e
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	err = out.Sync()
	if err != nil {
		return err
	}
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	err = os.Chmod(dst, si.Mode())
	if err != nil {
		return err
	}
	return err
}
