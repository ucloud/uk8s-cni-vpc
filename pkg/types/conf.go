package types

import (
	"net"

	"github.com/containernetworking/cni/pkg/types"
)

// PortMapEntry corresponds to a single entry in the port_mappings argument,
// see CONVENTIONS.md
type PortMapEntry struct {
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"hostIP,omitempty"`
}

// PluginConf contains configuration parameters
type PluginConf struct {
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
