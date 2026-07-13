// Copyright UCloud. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
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
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
)

const defaultNetClassPath = "/sys/class/net"

// ensureLinkRPS configures RPS for every RX queue on a host network device.
func ensureLinkRPS(linkName string) error {
	return ensureLinkRPSWithCPUCount(linkName, defaultNetClassPath, runtime.NumCPU())
}

func ensureLinkRPSWithCPUCount(linkName, netClassPath string, cpuCount int) error {
	mask, valid := rpsCPUMask(cpuCount)
	if !valid {
		return errors.Errorf("cnivpc.ensureLinkRPS invalid CPU count %d", cpuCount)
	}

	pattern := filepath.Join(netClassPath, linkName, "queues", "rx-*", "rps_cpus")
	rpsFiles, err := filepath.Glob(pattern)
	if err != nil {
		return errors.Wrapf(err, "cnivpc.ensureLinkRPS glob %s", pattern)
	}
	if len(rpsFiles) == 0 {
		return errors.Errorf("cnivpc.ensureLinkRPS no RX queues for %s", linkName)
	}

	for _, rpsFile := range rpsFiles {
		currentMask, err := os.ReadFile(rpsFile)
		if err != nil {
			return errors.Wrapf(err, "cnivpc.ensureLinkRPS read %s", rpsFile)
		}
		if normalizeRPSMask(string(currentMask)) == normalizeRPSMask(mask) {
			continue
		}

		if err := os.WriteFile(rpsFile, []byte(mask), 0644); err != nil {
			return errors.Wrapf(err, "cnivpc.ensureLinkRPS write %s", rpsFile)
		}
		ulog.Infof("Set RPS CPUs for link %s queue %s to %s", linkName, filepath.Base(filepath.Dir(rpsFile)), mask)
	}
	return nil
}

func rpsCPUMask(cpuCount int) (string, bool) {
	if cpuCount <= 0 {
		return "", false
	}

	// Linux cpumasks use comma-separated 32-bit groups, most significant first.
	groups := make([]string, 0, (cpuCount-1)/32+1)
	if remainder := cpuCount % 32; remainder != 0 {
		mask := (uint64(1) << remainder) - 1
		groups = append(groups, strconv.FormatUint(mask, 16))
	}
	for fullGroups := cpuCount / 32; fullGroups > 0; fullGroups-- {
		groups = append(groups, "ffffffff")
	}

	return strings.Join(groups, ","), true
}

func normalizeRPSMask(mask string) string {
	mask = strings.ToLower(strings.TrimSpace(mask))
	mask = strings.ReplaceAll(mask, ",", "")
	mask = strings.TrimLeft(mask, "0")
	if mask == "" {
		return "0"
	}
	return mask
}
