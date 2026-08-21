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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SubnetAllocationStrategy string

const (
	// SubnetAllocationStrategySequential checks subnetIds in order and selects
	// the first subnet with available IPs. It is the default strategy.
	SubnetAllocationStrategySequential SubnetAllocationStrategy = "sequential"
	// SubnetAllocationStrategyBalanced selects the subnet with the most
	// available IPs.
	SubnetAllocationStrategyBalanced SubnetAllocationStrategy = "balanced"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PodNetworking is a specification for a PodNetworking resource
type PodNetworking struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PodNetworkingSpec   `json:"spec"`
	Status PodNetworkingStatus `json:"status"`
}

// PodNetworkingSpec is the spec for a PodNetworking resource
type PodNetworkingSpec struct {
	SecurityGroupIds []string                 `json:"securityGroupIds"`
	SubnetIds        []string                 `json:"subnetIds"`
	Strategy         SubnetAllocationStrategy `json:"strategy,omitempty"`
	NATOutgoing      *bool                    `json:"natOutgoing,omitempty"`
}

// NATOutgoingEnabled returns whether traffic leaving the VPC should be
// masqueraded. NAT is enabled by default for backward compatibility.
func (in PodNetworkingSpec) NATOutgoingEnabled() bool {
	return in.NATOutgoing == nil || *in.NATOutgoing
}

// PodNetworkingSpec is the status for PodNetworking resource
type PodNetworkingStatus struct {
	Status string `json:"status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// PodNetworkingList is a list of PodNetworking resources
type PodNetworkingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []PodNetworking `json:"items"`
}
