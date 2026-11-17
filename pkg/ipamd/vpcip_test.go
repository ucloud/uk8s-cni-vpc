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
	"path/filepath"
	"testing"
	"time"

	"github.com/ucloud/uk8s-cni-vpc/pkg/storage"
	"github.com/ucloud/uk8s-cni-vpc/rpc"
)

func TestRecycleCooldownIP(t *testing.T) {
	db, err := storage.NewDBFileHandler(filepath.Join(t.TempDir(), "pool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	pool, err := storage.NewDisk[rpc.PodNetwork]("pool", db)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	cooling := &cooldownIPItem{
		vpcIP:        &rpc.PodNetwork{VPCID: "vpc-test", VPCIP: "192.0.2.1"},
		cooldownOver: now + 60,
	}
	expired := &cooldownIPItem{
		vpcIP:        &rpc.PodNetwork{VPCID: "vpc-test", VPCIP: "192.0.2.2"},
		cooldownOver: now - 60,
	}
	s := &ipamServer{pool: pool, cooldownSet: []*cooldownIPItem{cooling, expired}}

	s.recycleCooldownIP()

	if _, err := pool.Get(getReservedIPKey(expired.vpcIP)); err != nil {
		t.Errorf("expired IP should be back in the pool: %v", err)
	}
	if len(s.cooldownSet) != 1 || s.cooldownSet[0] != cooling || pool.Len() != 1 {
		t.Error("only the expired IP should move from the cooldown queue to the pool")
	}
}
