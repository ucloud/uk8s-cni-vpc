package snapshot

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/pkg/version"
)

const baseDir = "/opt/cni/snapshot"

type Snapshot struct {
	name string
	cmds []command
	err  error
	desc string
}

type command struct {
	name string
	args []string
}

func New(name string) *Snapshot {
	return &Snapshot{
		name: name,
	}
}

func (s *Snapshot) Add(name string, args ...string) {
	s.cmds = append(s.cmds, command{
		name: name,
		args: args,
	})
}

func (s *Snapshot) SetError(err error) {
	s.err = err
}

func (s *Snapshot) SetDesc(desc string) {
	s.desc = desc
}

func (s *Snapshot) Save() {
	if err := s.save(); err != nil {
		ulog.Errorf("Take snapshot for %q error: %v", s.name, err)
		return
	}
	ulog.Infof("Take snapshot for %q, with %d command(s), to %s", s.name, len(s.cmds), filepath.Join(baseDir, s.name))
}

func (s *Snapshot) save() error {
	stat, err := os.Stat(baseDir)
	switch {
	case os.IsNotExist(err):
		err = os.MkdirAll(baseDir, os.ModePerm)
		if err != nil {
			return fmt.Errorf("Create directory for snapshot error: %v", err)
		}

	case err == nil:
		if !stat.IsDir() {
			return fmt.Errorf("Ensure snapshot directory: %s is a file", baseDir)
		}

	default:
		return fmt.Errorf("Read directory for snapshot error: %v", err)
	}

	if len(s.cmds) == 0 {
		return errors.New("No command to take snapshot")
	}

	var buffer bytes.Buffer
	addLine := func(line string, args ...any) {
		if line != "" {
			line = fmt.Sprintf(line, args...)
			buffer.WriteString(line)
		}
		buffer.WriteRune('\n')
	}

	addLine("UCloud VPC CNI Snapshot - version %s", version.CNIVersion)
	addLine("Name: %s", s.name)

	now := time.Now().Local().Format("2006-01-02 15:04:05")
	addLine("Time: %s", now)
	if s.desc != "" {
		addLine("Description: %s", s.desc)
	}
	if s.err != nil {
		addLine("Error: %v", s.err)
	}
	addLine("")

	for _, cmd := range s.cmds {
		addLine(">>> %s %s", cmd.name, strings.Join(cmd.args, " "))
		execCmd := exec.Command(cmd.name, cmd.args...)
		execCmd.Stdout = &buffer
		execCmd.Stderr = &buffer

		err = execCmd.Run()
		if err != nil {
			addLine(">>> Bad exec: %v", err)
		}
		addLine("")
	}

	path := filepath.Join(baseDir, s.name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("Open snapshot file error: %v", err)
	}
	defer file.Close()

	_, err = io.Copy(file, &buffer)
	if err != nil {
		return fmt.Errorf("Write snapshot file error: %v", err)
	}

	return nil
}
