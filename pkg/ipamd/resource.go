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

package ipamd

import (
	"fmt"
	"runtime"
	"strconv"
	"time"

	"github.com/ucloud/uk8s-cni-vpc/pkg/deviceplugin"
	"github.com/ucloud/uk8s-cni-vpc/pkg/storage"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	"github.com/ucloud/ucloud-sdk-go/ucloud/metadata"
	"k8s.io/klog/v2"
)

// UNI limit equals to vCPU number
func getNodeUNILimits() int {
	//	http://100.80.80.80/meta-data/v1/uhost/cpu
	mdcli := metadata.NewClient()
	cpu, err := mdcli.GetMetadata("/uhost/cpu")
	if err != nil {
		return runtime.NumCPU()
	} else {
		cpuNo, err := strconv.Atoi(cpu)
		if err != nil {
			return runtime.NumCPU()
		} else {
			return cpuNo
		}
	}
}

func startDevicePlugin() error {
	// Init deviceplugin daemon for UNI
	s := deviceplugin.NewUNIDevicePlugin(getNodeUNILimits())
	err := s.Serve(deviceplugin.ResourceName)
	if err != nil {
		return fmt.Errorf("failed to set deviceplugin on node, %v", err)
	}
	return nil
}

// Check any remaining resources(UNI, EIP) that shouldn't exist any more, delete or release them in case of any leakage.
func (s *ipamServer) reconcile() {
	klog.Infof("Start reconcile loop")
	tk := time.Tick(3 * time.Minute)
	for {
		select {
		case <-tk:
			s.doReconcile()
		}
	}
}

func (s *ipamServer) doReconcile() {
	// Get local pods
	folks, err := s.getLocalPods()
	if err != nil {
		klog.Errorf("Cannot get local pods list, %v", err)
		return
	}

	pNets, err := s.store.List()
	if err != nil {
		klog.Errorf("Cannot list all local pod network information, %v", err)
		return
	}

	orphans := make([]*rpc.PodNetwork, 0)
	for _, pNet := range pNets {
		for idx, p := range folks.Items {
			if p.Name == pNet.PodName && p.Namespace == pNet.PodNS {
				if string(p.UID) == pNet.PodUID {
					break
				}
				if len(pNet.PodUID) == 0 {
					klog.Infof("Complete PodUID field for record %+v", pNet)
					pNet.PodUID = string(p.UID)
					s.store.Set(storage.GetKey(pNet.PodName, pNet.PodNS, pNet.SandboxID), pNet)
					break
				}
			}
			if idx == len(folks.Items)-1 {
				orphans = append(orphans, pNet)
			}
		}
	}

	// Do garbage clean
	for _, pNet := range orphans {
		if pNet.DedicatedUNI && len(pNet.InterfaceID) > 0 {
			klog.Infof("Start garbage clean for %s/%s, UID:%s, UNI:%s", pNet.PodName, pNet.PodNS, pNet.PodUID, pNet.InterfaceID)
			err = s.releaseUNI(pNet.PodUID, pNet.InterfaceID)
			if err != nil {
				klog.Errorf("Failed to do garbage clean for %s/%s, %v", pNet.PodName, pNet.PodNS, err)
			}
		}
		klog.Infof("Delete local storage for orphan pod: %+v", pNet)
		err = s.store.Delete(storage.GetKey(pNet.PodName, pNet.PodNS, pNet.SandboxID))
		if err != nil {
			klog.Errorf("Failed to do delete local network storage of %s/%s, %v", pNet.PodName, pNet.PodNS, err)
		}
	}
	return
}
