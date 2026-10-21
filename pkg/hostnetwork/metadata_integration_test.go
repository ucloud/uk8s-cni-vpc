//go:build linux

package hostnetwork

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Run only inside a disposable privileged container: this test changes its
// network namespace, iptables and /opt/cni, never the production host.
func TestMetadataAccess(t *testing.T) {
	if os.Getenv("UK8S_METADATA_INTEGRATION") != "1" {
		t.Skip("requires disposable privileged Linux container")
	}
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v: %s", name, args, err, out)
		}
		return string(out)
	}
	if err := os.MkdirAll("/opt/cni", 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(configPath) })
	run("ip", "netns", "add", "metadata-pod")
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", "metadata-pod").Run() })
	run("ip", "link", "add", "ucni-test", "type", "veth", "peer", "name", "pod-eth")
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", "ucni-test").Run() })
	run("ip", "link", "set", "pod-eth", "netns", "metadata-pod")
	run("ip", "addr", "add", "10.200.1.1/24", "dev", "ucni-test")
	run("ip", "link", "set", "ucni-test", "up")
	run("ip", "-n", "metadata-pod", "addr", "add", "10.200.1.2/24", "dev", "pod-eth")
	run("ip", "-n", "metadata-pod", "link", "set", "pod-eth", "up")
	run("ip", "-n", "metadata-pod", "route", "add", "default", "via", "10.200.1.1")
	run("ip", "netns", "add", "metadata-upstream")
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", "metadata-upstream").Run() })
	run("ip", "link", "add", "uplink-test", "type", "veth", "peer", "name", "upstream-eth")
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", "uplink-test").Run() })
	run("ip", "link", "set", "upstream-eth", "netns", "metadata-upstream")
	run("ip", "addr", "add", "10.200.2.1/24", "dev", "uplink-test")
	run("ip", "link", "set", "uplink-test", "up")
	run("ip", "-n", "metadata-upstream", "addr", "add", "10.200.2.2/24", "dev", "upstream-eth")
	run("ip", "-n", "metadata-upstream", "link", "set", "upstream-eth", "up")
	run("ip", "-n", "metadata-upstream", "link", "set", "lo", "up")
	run("ip", "-n", "metadata-upstream", "addr", "add", "100.80.80.80/32", "dev", "lo")
	run("ip", "-n", "metadata-upstream", "route", "add", "10.200.1.0/24", "via", "10.200.2.1")
	run("ip", "route", "add", "100.80.80.80/32", "via", "10.200.2.2")
	run("sysctl", "-w", "net.ipv4.ip_forward=1")
	webroot := t.TempDir()
	if err := os.WriteFile(webroot+"/index.html", []byte("metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	upstream := exec.Command("ip", "netns", "exec", "metadata-upstream", "busybox-extras", "httpd", "-f", "-p", "18080", "-h", webroot)
	if err := upstream.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upstream.Process.Kill(); _ = upstream.Wait() })
	listener, err := net.Listen("tcp4", ":18080")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }), ReadHeaderTimeout: time.Second}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })
	curl := func(pod bool, ip string) error {
		args := []string{"--noproxy", "*", "-fsS", "--max-time", "1", "http://" + ip + ":18080"}
		if pod {
			args = append([]string{"netns", "exec", "metadata-pod", "curl"}, args...)
			return exec.Command("ip", args...).Run()
		}
		return exec.Command("curl", args...).Run()
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := curl(true, "100.80.80.80"); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("baseline: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := Sync(); err != nil {
		t.Fatal(err)
	}
	if err := curl(true, "100.80.80.80"); err == nil {
		t.Fatal("pod metadata access allowed")
	}
	if err := curl(false, "100.80.80.80"); err != nil {
		t.Fatalf("host metadata blocked: %v", err)
	}
	if err := curl(true, "10.200.1.1"); err != nil {
		t.Fatalf("ordinary pod traffic blocked: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Sync(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	backends := []string{"iptables-nft", "iptables-legacy"}
	for _, bin := range backends {
		rules := run(bin, "-t", "raw", "-S", "PREROUTING")
		if strings.Count(rules, ruleComment) != 1 {
			t.Fatalf("not idempotent: %s", rules)
		}
		run(bin, append([]string{"-t", "raw", "-D", "PREROUTING"}, metadataRule...)...)
	}
	if err := Sync(); err != nil {
		t.Fatal(err)
	}
	if err := curl(true, "100.80.80.80"); err == nil {
		t.Fatal("deleted rules not restored")
	}
	for _, bin := range backends {
		run(bin, "-t", "raw", "-I", "PREROUTING", "1", "-i", "ucni+", "-j", "ACCEPT")
	}
	if err := Sync(); err != nil {
		t.Fatal(err)
	}
	if err := curl(true, "100.80.80.80"); err == nil {
		t.Fatal("rule priority not restored")
	}
	if err := os.WriteFile(configPath, []byte(`{"blockPodMetadata":"bad"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Sync(); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if err := curl(true, "100.80.80.80"); err == nil {
		t.Fatal("invalid policy removed protection")
	}
	if err := os.WriteFile(configPath, []byte(`{"blockPodMetadata":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Sync(); err != nil {
		t.Fatal(err)
	}
	if err := curl(true, "100.80.80.80"); err != nil {
		t.Fatalf("disable did not restore access: %v", err)
	}
	for _, bin := range backends {
		rules := run(bin, "-t", "raw", "-S", "PREROUTING")
		if strings.Contains(rules, ruleComment) || !strings.Contains(rules, "-j ACCEPT") {
			t.Fatalf("incorrect cleanup: %s", rules)
		}
	}
}
