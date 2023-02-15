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
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/containernetworking/cni/pkg/types"
	log "github.com/sirupsen/logrus"
	"github.com/ucloud/go-lockfile"
)

func init() {
	rand.Seed(time.Now().Unix())
}

func randInt(min, max int) int {
	return rand.Intn(max-min) + min
}

func MustAcquire() func() {
	lock, err := acquireLock()
	handleErr(err)
	return func() {
		err = lock.Unlock()
		handleErr(err)
	}
}

const (
	lockMaxWaitTime = time.Minute * 2

	sleepMaxFactor = 20
	sleepMinFactor = 30
)

func acquireLock() (*lockfile.Lockfile, error) {
	lock := lockfile.New(filepath.Join(os.TempDir(), "cni-vpc-uk8s.lock"))
	timer := time.NewTimer(lockMaxWaitTime)
	for {
		select {
		case <-timer.C:
			return nil, fmt.Errorf("lockfile not acquired, aborting")

		default:
			err := lock.TryLock()
			if err == nil {
				break
			} else if err == lockfile.ErrBusy {
				sleepFactor := randInt(sleepMinFactor, sleepMaxFactor)
				time.Sleep(time.Duration(sleepFactor) * time.Millisecond)
			} else {
				return nil, err
			}
		}
	}
}

func handleErr(err error) {
	if err == nil {
		return
	}
	log.Errorf("LockfileRun Error %+v %v", err, os.Args)
	e := types.Error{
		Code:    types.ErrInternal,
		Msg:     "LockfileRun",
		Details: "failed",
	}
	ne := e.Print()
	if ne != nil {
		log.Errorf("LockfileRun print Error %+v %v", ne, os.Args)
	}
	os.Exit(1)
}
