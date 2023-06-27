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

package uapi

import (
	"strings"
	"sync"

	"github.com/ucloud/ucloud-sdk-go/ucloud/metadata"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
)

var (
	meta     *metadata.Metadata
	metaOnce sync.Once
)

func GetMeta() (*metadata.Metadata, error) {
	var err error
	metaOnce.Do(func() {
		client := metadata.NewClient()
		var md metadata.Metadata
		md, err = client.GetInstanceIdentityDocument()
		if err != nil {
			ulog.Errorf("Get instance metadata error: %v", err)
			return
		}
		meta = &md
	})
	return meta, err
}

const (
	instanceTypeCube    = "Cube"
	instanceTypeUHost   = "UHost"
	instanceTypeUPHost  = "UPM"
	instanceTypeUDocker = "UDocker"
	instanceTypeUDHost  = "UDHost"
	instanceTypeUNI     = "UNI"
)

func instanceType(resource string) string {
	if strings.HasPrefix(resource, "uhost-") {
		return instanceTypeUHost
	} else if strings.HasPrefix(resource, "upm-") {
		return instanceTypeUPHost
	} else if strings.HasPrefix(resource, "docker-") {
		return instanceTypeUDocker
	} else if strings.HasPrefix(resource, "udhost-") {
		return instanceTypeUDHost
	} else if strings.HasPrefix(resource, "uni-") {
		return instanceTypeUNI
	} else if strings.HasPrefix(resource, "cube-") {
		return instanceTypeCube
	}

	return "Unknown"
}

func GetObjectIDForSecondaryIP() (string, error) {
	uapi, err := NewClient()
	if err != nil {
		return "", err
	}
	instanceId := uapi.InstanceID()
	if instanceType(instanceId) != instanceTypeUHost {
		return instanceId, nil
	}

	cli, err := uapi.UHostClient()
	if err != nil {
		return "", err
	}

	req := cli.NewDescribeUHostInstanceRequest()
	req.UHostIds = []string{}
	resp, err := cli.DescribeUHostInstance(req)
	if err != nil || len(resp.UHostSet) == 0 {
		ulog.Errorf("DescribeUHostInstance for %v error: %v", instanceId, err)
		return instanceId, nil
	}

	uhostInstance := resp.UHostSet[0]
	for _, ipset := range uhostInstance.IPSet {
		if ipset.Default == "true" {
			if len(ipset.NetworkInterfaceId) > 0 {
				return ipset.NetworkInterfaceId, nil
			}
		}
	}

	return instanceId, nil
}
