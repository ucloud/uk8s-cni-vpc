package main

import (
	"flag"
	"os"
	"runtime"

	"github.com/ucloud/uk8s-cni-vpc/pkg/ipamd"
	"github.com/ucloud/uk8s-cni-vpc/pkg/version"

	"k8s.io/klog/v2"
)

func init() {
	flag.Set("alsologtostderr", "true")
	flag.Set("log_dir", "/var/log/ucloud/")
	flag.Parse()
}

func showVersion() {
	klog.Infof("CNI Version: " + version.CNIVersion)
	klog.Infof("Go Version: " + runtime.Version())
	klog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	klog.Info("Build Time: " + version.BuildTime)
	klog.Infof("Git Commit ID: " + version.ProgramCommitID)
}

func main() {
	// Print version
	if len(os.Args) == 2 && os.Args[1] == "version" {
		showVersion()
		os.Exit(0)
	}

	showVersion()
	os.Exit(_main())
}

func _main() int {
	err := ipamd.GenerateConfFile(true)
	if err != nil {
		klog.Fatal(err)
	}
	// Install cni binary and configure file
	err = ipamd.InstallCNIComponent("/app/cnivpc", "/opt/cni/bin/cnivpc")
	if err != nil {
		klog.Errorf("Failed to copy cnivpc, %v", err)
		return 1
	}
	err = ipamd.InstallCNIComponent("/app/10-cnivpc.conf", "/opt/cni/net.d/10-cnivpc.conf")
	if err != nil {
		klog.Errorf("Failed to copy 10-cnivpc.conf, %v", err)
		return 1
	}

	err = startIpamd()
	if err != nil {
		klog.Errorf("Cannot launch ipamd service, %v", err)
		return 1
	}
	return 0
}

func startIpamd() error {
	return ipamd.IpamdServer()
}
