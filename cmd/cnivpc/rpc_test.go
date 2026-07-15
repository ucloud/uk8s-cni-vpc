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
	"testing"

	podnetworkingv1beta1 "github.com/ucloud/uk8s-cni-vpc/kubernetes/apis/podnetworking/v1beta1"
)

func TestValidateSubnetAllocationStrategy(t *testing.T) {
	tests := []struct {
		name      string
		strategy  podnetworkingv1beta1.SubnetAllocationStrategy
		want      podnetworkingv1beta1.SubnetAllocationStrategy
		wantError bool
	}{
		{
			name:     "empty is allowed",
			strategy: "",
			want:     "",
		},
		{
			name:     "sequential",
			strategy: "sequential",
			want:     podnetworkingv1beta1.SubnetAllocationStrategySequential,
		},
		{
			name:      "surrounding whitespace is unsupported",
			strategy:  " balanced ",
			wantError: true,
		},
		{
			name:      "unsupported",
			strategy:  "random",
			wantError: true,
		},
		{
			name:      "uppercase is unsupported",
			strategy:  "BALANCED",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSubnetAllocationStrategy(tt.strategy)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tt.strategy != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, tt.strategy)
			}
		})
	}
}

func TestSelectSubnetByAllocationStrategy(t *testing.T) {
	subnets := []subnetAvailableIP{
		{id: "subnet-b", availableIPs: 3},
		{id: "subnet-c", availableIPs: 10},
	}

	tests := []struct {
		name     string
		strategy podnetworkingv1beta1.SubnetAllocationStrategy
		subnets  []subnetAvailableIP
		wantID   string
		wantOK   bool
	}{
		{
			name:     "default uses first subnet with available ips",
			strategy: "",
			subnets:  subnets,
			wantID:   "subnet-b",
			wantOK:   true,
		},
		{
			name:     "sequential uses first subnet with available ips",
			strategy: podnetworkingv1beta1.SubnetAllocationStrategySequential,
			subnets:  subnets,
			wantID:   "subnet-b",
			wantOK:   true,
		},
		{
			name:     "balanced uses subnet with most available ips",
			strategy: podnetworkingv1beta1.SubnetAllocationStrategyBalanced,
			subnets:  subnets,
			wantID:   "subnet-c",
			wantOK:   true,
		},
		{
			name:     "no available subnet",
			strategy: podnetworkingv1beta1.SubnetAllocationStrategyBalanced,
			subnets:  []subnetAvailableIP{},
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := selectSubnetByAllocationStrategy(tt.strategy, tt.subnets)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("expected ok %v, got %v", tt.wantOK, ok)
			}
			if !tt.wantOK {
				return
			}
			if got.id != tt.wantID {
				t.Fatalf("expected subnet %q, got %q", tt.wantID, got.id)
			}
		})
	}
}

func TestSelectSubnetByAllocationStrategyUnsupported(t *testing.T) {
	_, _, err := selectSubnetByAllocationStrategy(podnetworkingv1beta1.SubnetAllocationStrategy("random"), []subnetAvailableIP{
		{id: "subnet-a", availableIPs: 1},
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}
