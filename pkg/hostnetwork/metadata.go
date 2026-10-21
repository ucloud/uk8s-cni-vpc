//go:build linux

// Package hostnetwork manages node-wide rules shared by CNI and IPAMD.
package hostnetwork

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"golang.org/x/sys/unix"
)

const (
	configPath  = "/opt/cni/metadata-access.json"
	lockPath    = "/opt/cni/.metadata-access.lock"
	ruleComment = "UK8S block pod metadata access"
)

var metadataRule = []string{"-i", "ucni+", "-d", "100.80.80.80/32", "-m", "comment", "--comment", ruleComment, "-j", "DROP"}

// Sync applies the node's policy to existing and future ucni interfaces. A missing
// config enables blocking. Explicit false removes only rules owned by this feature.
// Both processes must share /opt/cni; CNI DEL must not remove these node-wide rules.
func Sync() (err error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return errors.Wrap(err, "hostnetwork.Sync open lock")
	}
	defer func() { err = errors.CombineErrors(err, errors.Wrap(f.Close(), "hostnetwork.Sync close lock")) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if e == nil {
			break
		}
		if !errors.Is(e, unix.EWOULDBLOCK) || time.Now().After(deadline) {
			return errors.Wrap(e, "hostnetwork.Sync acquire lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer func() {
		err = errors.CombineErrors(err, errors.Wrap(unix.Flock(int(f.Fd()), unix.LOCK_UN), "hostnetwork.Sync unlock"))
	}()
	enabled, err := loadPolicy()
	if err != nil {
		return err
	}
	// Different binaries can use different kernel backends. Reconcile every
	// installed backend, including when disabling, so a switch cannot leave an
	// old blocking rule behind. Never silently ignore an unusable backend.
	seen := map[string]bool{}
	for _, name := range []string{"iptables", "iptables-legacy", "iptables-nft"} {
		path, e := exec.LookPath(name)
		if e != nil {
			if errors.Is(e, exec.ErrNotFound) {
				continue
			}
			return errors.Wrap(e, "hostnetwork.Sync find iptables")
		}
		version, e := command(path, "--version")
		if e != nil {
			return e
		}
		backend := "legacy"
		if strings.Contains(version, "nf_tables") {
			backend = "nft"
		}
		if seen[backend] {
			continue
		}
		seen[backend] = true
		if err := syncBackend(path, enabled); err != nil {
			return err
		}
	}
	if len(seen) == 0 {
		return errors.New("hostnetwork.Sync no iptables executable installed")
	}
	return nil
}

func loadPolicy() (bool, error) {
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, errors.Wrap(err, "hostnetwork.loadPolicy read config")
	}
	var cfg struct {
		BlockPodMetadata *bool `json:"blockPodMetadata"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false, errors.Wrap(err, "hostnetwork.loadPolicy decode config")
	}
	if cfg.BlockPodMetadata == nil {
		return false, errors.New("hostnetwork.loadPolicy blockPodMetadata is required")
	}
	return *cfg.BlockPodMetadata, nil
}

func command(path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil {
		return "", errors.Wrapf(err, "hostnetwork.command %s %v: %s", path, args, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func syncBackend(path string, enabled bool) error {
	output, err := command(path, "-w", "5", "-t", "raw", "-S", "PREROUTING")
	if err != nil {
		return err
	}
	expected := "-A PREROUTING -d 100.80.80.80/32 -i ucni+ -m comment --comment \"" + ruleComment + "\" -j DROP"
	count, first := 0, ""
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if !strings.HasPrefix(line, "-A ") {
			continue
		}
		if first == "" {
			first = line
		}
		if line == expected {
			count++
		}
	}
	if enabled {
		if first == expected {
			return nil
		}
		// Insert ahead of other raw-table rules. Keep any older copy rather
		// than deleting by index: another firewall manager may change indices.
		args := append([]string{"-w", "5", "-t", "raw", "-I", "PREROUTING", "1"}, metadataRule...)
		_, err := command(path, args...)
		return err
	}
	// Delete only our exact rule, including copies left by position repair.
	for i := 0; i < count; i++ {
		args := append([]string{"-w", "5", "-t", "raw", "-D", "PREROUTING"}, metadataRule...)
		if _, err := command(path, args...); err != nil {
			return err
		}
	}
	return nil
}
