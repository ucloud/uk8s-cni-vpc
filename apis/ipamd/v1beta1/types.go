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

const (
	StatusDry      = "dry"
	StatusHungry   = "hungry"
	StatusNormal   = "normal"
	StatusOverflow = "overflow"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Ipamd is a specification for a Ipamd resource
type Ipamd struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IpamdSpec   `json:"spec"`
	Status IpamdStatus `json:"status"`
}

// IpamdSpec is the spec for a Ipamd resource
type IpamdSpec struct {
	Node string `json:"node"`
	Addr string `json:"addr"`
}

// IpamdSpec is the status for Ipamd resource
type IpamdStatus struct {
	Current int `json:"current"`

	High int `json:"high"`
	Low  int `json:"low"`

	Status string `json:"status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// IpamdList is a list of Ipamd resources
type IpamdList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Ipamd `json:"items"`
}
