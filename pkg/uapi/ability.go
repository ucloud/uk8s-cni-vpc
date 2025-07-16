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
	"fmt"
	"strings"
)

type Ability struct {
	InstanceID string

	SupportUNI bool
	SecGroup   bool
}

func GetAbility() (*Ability, error) {
	client, err := NewClient()
	if err != nil {
		return nil, err
	}

	instanceID := client.InstanceID()
	if !strings.HasPrefix(instanceID, "uhost-") {
		return &Ability{InstanceID: instanceID}, nil
	}

	uhostcli, err := client.UHostClient()
	if err != nil {
		return nil, err
	}

	uhostReq := uhostcli.NewDescribeUHostInstanceRequest()
	uhostReq.UHostIds = []string{instanceID}

	uhostResp, err := uhostcli.DescribeUHostInstance(uhostReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call DescribeUHostInstance: %v", err)
	}

	if len(uhostResp.UHostSet) == 0 {
		return nil, fmt.Errorf("cannot find uhost %s", instanceID)
	}

	uhostInfo := uhostResp.UHostSet[0]
	supportUNI := false
	for _, ipset := range uhostInfo.IPSet {
		// When NetworkInterfaceId is not empty and starts with 'uni-', it means that this
		// IP is an UNI, and current uhost support adding UNI.
		if strings.HasPrefix(ipset.NetworkInterfaceId, "uni-") {
			supportUNI = true
			break
		}
	}

	return &Ability{
		InstanceID: instanceID,
		SupportUNI: supportUNI,
		SecGroup:   strings.EqualFold(uhostInfo.NetFeatureTag, "SecGroup"),
	}, nil
}
