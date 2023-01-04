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

package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/containernetworking/cni/pkg/types"
)

const cnivpcPath = "/opt/cni/net.d/10-cnivpc.conf"

// PortMapEntry corresponds to a single entry in the port_mappings argument,
// see CONVENTIONS.md
type PortMapEntry struct {
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"hostIP,omitempty"`
}

// Plugin contains configuration parameters
type Plugin struct {
	types.NetConf
	MasterInterface      string    `json:"masterInterface"`
	SNAT                 *bool     `json:"snat,omitempty"`
	ConditionsV4         *[]string `json:"conditionsV4"`
	ConditionsV6         *[]string `json:"conditionsV6"`
	MarkMasqBit          *int      `json:"markMasqBit"`
	ExternalSetMarkChain *string   `json:"externalSetMarkChain"`
	RuntimeConfig        struct {
		PortMaps []PortMapEntry `json:"portMappings,omitempty"`
	} `json:"runtimeConfig,omitempty"`

	// These are fields parsed out of the config or the environment;
	// included here for convenience
	ContainerID string    `json:"-"`
	ContIPv4    net.IPNet `json:"-"`
	ContIPv6    net.IPNet `json:"-"`
}

func ParsePlugin(data []byte) (*Plugin, error) {
	var cni Plugin
	if err := json.Unmarshal(data, &cni); err != nil {
		return nil, fmt.Errorf("failed to parse cnivpc config: %v", err)
	}
	return &cni, nil
}

func LoadPlugin() (*Plugin, error) {
	data, err := os.ReadFile(cnivpcPath)
	if err != nil {
		return nil, err
	}
	return ParsePlugin(data)
}
