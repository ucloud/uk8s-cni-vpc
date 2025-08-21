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

package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/containernetworking/cni/pkg/types"
	"github.com/ucloud/go-lockfile"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
)

func MustAcquire() func() {
	lock, err := acquireLock()
	handleErr(err)
	return func() {
		err = lock.Unlock()
		handleErr(err)
	}
}

func acquireLock() (*lockfile.Lockfile, error) {
	lock := lockfile.New(filepath.Join(os.TempDir(), "cni-vpc-uk8s.lock"))
	tries := 80000

	for {
		tries--
		if tries <= 0 {
			return nil, fmt.Errorf("lockfile not acquired, aborting")
		}

		err := lock.TryLock()
		if err == nil {
			break
		} else if err == lockfile.ErrBusy {
			time.Sleep(30 * time.Millisecond)
		} else {
			return nil, err
		}
	}
	return lock, nil
}

func handleErr(err error) {
	if err == nil {
		return
	}
	ulog.Errorf("LockfileRun error: %+v %v", err, os.Args)
	e := types.Error{
		Code:    types.ErrInternal,
		Msg:     "LockfileRun",
		Details: "failed",
	}
	ne := e.Print()
	if ne != nil {
		ulog.Errorf("LockfileRun print error: %+v %v", ne, os.Args)
	}
	os.Exit(1)
}
