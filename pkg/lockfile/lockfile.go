package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/containernetworking/cni/pkg/types"
	log "github.com/sirupsen/logrus"
	"github.com/ucloud/go-lockfile"
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
			return nil, fmt.Errorf("Lockfile not acquired, aborting")
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
