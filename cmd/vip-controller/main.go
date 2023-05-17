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

package main

import (
	"context"
	"flag"
	"os"
	"runtime"
	"time"

	"github.com/ucloud/ucloud-sdk-go/ucloud"
	v1beta1 "github.com/ucloud/uk8s-cni-vpc/kubernetes/apis/vipcontroller/v1beta1"
	crdinformers "github.com/ucloud/uk8s-cni-vpc/kubernetes/generated/informers/externalversions"
	"github.com/ucloud/uk8s-cni-vpc/pkg/kubeclient"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/pkg/version"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
)

const (
	defaultListCRDLimit int64 = 128
	UAPIErrorIPNotExst        = 58221

	minStaticIpGcInterval = 5 * time.Minute
)

var staticIpGcInterval = "1h"

func init() {
	flag.StringVar(&staticIpGcInterval, "static-ip-gc-interval", "1h", "interval between check detached vpcipclaims has reached release time")
}

func showVersion() {
	ulog.Infof("Controller Version: " + version.CNIVersion)
	ulog.Infof("Go Version: " + runtime.Version())
	ulog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	ulog.Infof("Build Time: " + version.BuildTime)
	ulog.Infof("Git Commit ID: " + version.ProgramCommitID)
}

func main() {
	// Print version
	if len(os.Args) == 2 && os.Args[1] == "version" {
		showVersion()
		os.Exit(0)
	}

	flag.Parse()
	showVersion()
	// Set up signals so we handle the first shutdown signal gracefully
	stopCh := setupSignalHandler()

	// VpcIpclaim garbage clean
	go reconcileDetachedVip()

	// Set up sts controller
	kubeClient, err := kubeclient.Get()
	if err != nil {
		ulog.Fatalf("Failed to create kubernetes clientsets, %v", err)
	}

	vipClient, err := kubeclient.GetCRD()
	if err != nil {
		ulog.Fatalf("Failed to create crd vpcipclaim clientsets, %v", err)
	}

	stsInformer := informers.NewSharedInformerFactory(kubeClient, 0)
	stsController := NewStsController(stsInformer.Apps().V1().StatefulSets(), kubeClient, vipClient)
	stsInformer.Start(stopCh)
	// Sts controller
	go stsController.Run(4, stopCh)
	// VpcIpClaim controller
	vpcIpClaimInformer := crdinformers.NewSharedInformerFactory(vipClient, 0)
	vipController := NewVipController(vpcIpClaimInformer.Vipcontroller().V1beta1().VpcIpClaims(), vipClient, kubeClient)
	vpcIpClaimInformer.Start(stopCh)
	vipController.Run(2, stopCh)
}

func reconcileDetachedVip() {
	ulog.Infof("Start vpcipclaim garbage clean loop")
	duration, err := time.ParseDuration(staticIpGcInterval)
	if err != nil {
		ulog.Warnf("Failed to parse --static-ip-gc-interval, %v, set gc interval 1h as default", err)
		duration = 1 * time.Hour
	}
	if duration < minStaticIpGcInterval {
		duration = minStaticIpGcInterval
	}

	tk := time.Tick(duration)
	for {
		select {
		case <-tk:
			vipCheckAndClean()
		}
	}
}

func vipCheckAndClean() {
	vipClient, err := kubeclient.GetCRD()
	if err != nil {
		ulog.Errorf("Get clientset for crd vpcipclaim error: %v", err)
		return
	}

	// List all detached vpcipclaims
	vipList, err := vipClient.VipcontrollerV1beta1().VpcIpClaims(v1.NamespaceAll).List(context.TODO(),
		metav1.ListOptions{LabelSelector: "attached=false", Limit: defaultListCRDLimit})
	if err != nil {
		ulog.Errorf("List all detached vpcipclaims error: %v", err)
	}

	for _, vpcip := range vipList.Items {
		if vpcip.Status.ReleaseTime != "Never" {
			if release, err := time.ParseDuration(vpcip.Status.ReleaseTime); err == nil {
				detachTime, _ := time.Parse("2006-01-02 15:04:05", vpcip.Status.LastDetachTime)
				if detachTime.Add(release).Before(time.Now()) {
					kubeClient, err := kubeclient.Get()
					if err != nil {
						ulog.Errorf("Get kube client to check pod error: %v", err)
					}
					notRunning, err := ensureStaticIpPodNotRunning(kubeClient, vpcip.Namespace, vpcip.Name)
					if err == nil && notRunning {
						ulog.Infof("VpcIpclaim %s/%s %s has reached release time, will be deleted", vpcip.Namespace, vpcip.Name, vpcip.Spec.Ip)
						err = vipClient.VipcontrollerV1beta1().VpcIpClaims(vpcip.Namespace).Delete(context.TODO(), vpcip.Name, metav1.DeleteOptions{})
						if err != nil {
							ulog.Errorf("Delete vpcipclaim %s/%s %s error: %v", vpcip.Namespace, vpcip.Name, vpcip.Spec.Ip, err)
						}
					}
				}
			}
		}
	}
}

func releaseVPCIp(vpcip v1beta1.VpcIpClaim) error {
	uapi, err := uapi.NewClient()
	if err != nil {
		return nil
	}
	cli, err := uapi.VPCClient()
	if err != nil {
		return err
	}

	req := cli.NewDeleteSecondaryIpRequest()
	req.Zone = ucloud.String(vpcip.Status.Zone)
	req.Mac = ucloud.String(vpcip.Status.Mac)
	req.Ip = ucloud.String(vpcip.Spec.Ip)
	req.VPCId = ucloud.String(vpcip.Spec.VpcId)
	req.SubnetId = ucloud.String(vpcip.Spec.SubnetId)
	resp, err := cli.DeleteSecondaryIp(req)
	if err == nil {
		ulog.Infof("Secondary ip %v deleted by unetwork api service", vpcip.Spec.Ip)
	}
	if resp.RetCode == UAPIErrorIPNotExst {
		ulog.Warnf("Secondary ip %s has been deleted before", vpcip.Spec.Ip)
		return nil
	}
	return err
}
