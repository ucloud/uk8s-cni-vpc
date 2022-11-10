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

/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Vpcip is a specification for a Vpcip resource
type VpcIpClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VpcIpClaimSpec   `json:"spec"`
	Status VpcIpClaimStatus `json:"status"`
}

// VpcipSpec is the spec for a VpcIpClaim resource
type VpcIpClaimSpec struct {
	Ip       string `json:"ip"`
	Mask     string `json:"mask"`
	Gateway  string `json:"gateway"`
	SubnetId string `json:"subnetId"`
	VpcId    string `json:"vpcId"`
}

// VpcIpClaimStatus is the status for VpcIpClaim resource
type VpcIpClaimStatus struct {
	Attached       bool   `json:"attached"`
	Zone           string `json:"zone"`
	LastDetachTime string `json:"lastDetachtime"`
	Mac            string `json:"mac"`
	ReleaseTime    string `json:"releaseTime"`
	SandboxId      string `json:"sandboxId"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// VpcIpClaimList is a list of VpcIpClaim resources
type VpcIpClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []VpcIpClaim `json:"items"`
}
