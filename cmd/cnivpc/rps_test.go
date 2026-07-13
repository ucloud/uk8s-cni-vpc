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
	"testing"
	"time"
)

func TestRPSCPUMask(t *testing.T) {
	tests := []struct {
		name      string
		cpuCount  int
		want      string
		supported bool
	}{
		{name: "one CPU", cpuCount: 1, want: "1", supported: true},
		{name: "two CPUs", cpuCount: 2, want: "3", supported: true},
		{name: "four CPUs", cpuCount: 4, want: "f", supported: true},
		{name: "eight CPUs", cpuCount: 8, want: "ff", supported: true},
		{name: "sixteen CPUs", cpuCount: 16, want: "ffff", supported: true},
		{name: "thirty two CPUs", cpuCount: 32, want: "ffffffff", supported: true},
		{name: "sixty four CPUs", cpuCount: 64, want: "ffffffff,ffffffff", supported: true},
		{name: "unsupported", cpuCount: 3},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, supported := rpsCPUMask(test.cpuCount)
			if got != test.want || supported != test.supported {
				t.Fatalf("rpsCPUMask(%d) = (%q, %t), want (%q, %t)", test.cpuCount, got, supported, test.want, test.supported)
			}
		})
	}
}

func TestEnsureLinkRPSRejectsUnsupportedCPUCount(t *testing.T) {
	if err := ensureLinkRPSWithCPUCount("eth1", t.TempDir(), 3); err == nil {
		t.Fatal("ensureLinkRPSWithCPUCount() error = nil, want error")
	}
}

func TestEnsureLinkRPSConfiguresAllRXQueues(t *testing.T) {
	netClassPath := prepareRPSTestPaths(t, map[string]string{
		"rx-0": "0",
		"rx-1": "000000ff\n",
		"tx-0": "0",
	})
	unchangedPath := filepath.Join(netClassPath, "eth1", "queues", "rx-1", "rps_cpus")
	unchangedTime := time.Unix(100, 0)
	if err := os.Chtimes(unchangedPath, unchangedTime, unchangedTime); err != nil {
		t.Fatalf("os.Chtimes() error = %v", err)
	}

	if err := ensureLinkRPSWithCPUCount("eth1", netClassPath, 8); err != nil {
		t.Fatalf("ensureLinkRPSWithCPUCount() error = %v", err)
	}

	assertFileContent(t, filepath.Join(netClassPath, "eth1", "queues", "rx-0", "rps_cpus"), "ff")
	assertFileContent(t, unchangedPath, "000000ff\n")
	assertFileContent(t, filepath.Join(netClassPath, "eth1", "queues", "tx-0", "rps_cpus"), "0")

	info, err := os.Stat(unchangedPath)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	if !info.ModTime().Equal(unchangedTime) {
		t.Fatalf("unchanged queue modification time = %v, want %v", info.ModTime(), unchangedTime)
	}
}

func TestNormalizeRPSMask(t *testing.T) {
	tests := map[string]string{
		"000000ff\n":        "ff",
		"00000000,ffffffff": "ffffffff",
		"0":                 "0",
	}

	for input, want := range tests {
		if got := normalizeRPSMask(input); got != want {
			t.Errorf("normalizeRPSMask(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEnsureLinkRPSConfiguresWhenRXQueuesMatchCPUs(t *testing.T) {
	netClassPath := prepareRPSTestPaths(t, map[string]string{
		"rx-0": "0",
		"rx-1": "0",
	})

	if err := ensureLinkRPSWithCPUCount("eth1", netClassPath, 2); err != nil {
		t.Fatalf("ensureLinkRPSWithCPUCount() error = %v", err)
	}

	assertFileContent(t, filepath.Join(netClassPath, "eth1", "queues", "rx-0", "rps_cpus"), "3")
	assertFileContent(t, filepath.Join(netClassPath, "eth1", "queues", "rx-1", "rps_cpus"), "3")
}

func TestEnsureLinkRPSReturnsErrorWithoutRXQueues(t *testing.T) {
	netClassPath := prepareRPSTestPaths(t, map[string]string{
		"tx-0": "0",
	})

	if err := ensureLinkRPSWithCPUCount("eth1", netClassPath, 8); err == nil {
		t.Fatal("ensureLinkRPSWithCPUCount() error = nil, want error")
	}
}

func prepareRPSTestPaths(t *testing.T, queues map[string]string) string {
	t.Helper()
	root := t.TempDir()
	netClassPath := filepath.Join(root, "sys", "class", "net")
	for queue, value := range queues {
		queuePath := filepath.Join(netClassPath, "eth1", "queues", queue)
		if err := os.MkdirAll(queuePath, 0755); err != nil {
			t.Fatalf("os.MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(queuePath, "rps_cpus"), []byte(value), 0644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
	}
	return netClassPath
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", path, err)
	}
	if string(content) != want {
		t.Fatalf("os.ReadFile(%q) = %q, want %q", path, content, want)
	}
}
