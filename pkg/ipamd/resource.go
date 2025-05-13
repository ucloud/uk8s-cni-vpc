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

	"github.com/ucloud/uk8s-cni-vpc/pkg/database"
	"github.com/ucloud/uk8s-cni-vpc/pkg/deviceplugin"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	"github.com/ucloud/ucloud-sdk-go/ucloud/metadata"
)

// convert vCPU to uni limits according to
// https://docs.ucloud.cn/vpc/guide/uni
func GetNodeUNILimits() int {
	//	http://100.80.80.80/meta-data/v1/uhost/cpu
	mdcli := metadata.NewClient()
	cpu, err := mdcli.GetMetadata("/uhost/cpu")
	if err != nil {
		return 1
	}
	cpuNo, err := strconv.Atoi(cpu)
	if err != nil {
		cpuNo = runtime.NumCPU()
	}
	if cpuNo > 64 {
		return 15
	} else if cpuNo > 32 {
		return 12
	} else if cpuNo > 8 {
		return 8
	} else if cpuNo > 4 {
		return 4
	} else if cpuNo > 2 {
		return 3
	} else {
		return 2
	}
}

func startDevicePlugin() error {
	// Init deviceplugin daemon for UNI
	s := deviceplugin.NewUNIDevicePlugin(GetNodeUNILimits())
	err := s.Serve(deviceplugin.ResourceName)
	if err != nil {
		return fmt.Errorf("failed to set deviceplugin on node, %v", err)
	}
	return nil
}

// Check any remaining resources(UNI, EIP) that shouldn't exist any more, delete or release them in case of any leakage.
func (s *ipamServer) reconcile() {
	ulog.Infof("Start reconcile loop")
	tk := time.NewTicker(3 * time.Minute)
	for range tk.C {
		s.doReconcile()
	}
}

func (s *ipamServer) doReconcile() {
	// Get local pods
	folks, err := s.getLocalPods()
	if err != nil {
		ulog.Errorf("Get local pods list error: %v", err)
		return
	}

	kvs, err := s.networkDB.List()
	if err != nil {
		ulog.Errorf("List all local pod network information error: %v", err)
		return
	}

	orphans := make([]*database.KeyValue[rpc.PodNetwork], 0)
	for _, kv := range kvs {
		pn := kv.Value
		for idx, p := range folks.Items {
			if p.Name == pn.PodName && p.Namespace == pn.PodNS {
				if string(p.UID) == pn.PodUID {
					break
				}
				if len(pn.PodUID) == 0 {
					ulog.Infof("Complete PodUID field for record %+v", pn)
					pn.PodUID = string(p.UID)
					s.networkDB.Put(kv.Key, pn)
					break
				}
			}
			if idx == len(folks.Items)-1 {
				orphans = append(orphans, kv)
			}
		}
	}

	// Do garbage clean
	for _, kv := range orphans {
		pn := kv.Value
		ulog.Infof("Delete local storage for orphan pod: %+v", pn)
		err = s.networkDB.Delete(kv.Key)
		if err != nil {
			ulog.Errorf("Delete local network storage for %s/%s error: %v", pn.PodName, pn.PodNS, err)
		}
	}
}
