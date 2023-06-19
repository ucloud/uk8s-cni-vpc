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

package database

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/boltdb/bolt"
)

type TestValue struct {
	Name string
	Desc string
}

var boltdb *bolt.DB

const testBoltPath = "/tmp/boltdb.db"

func TestMain(m *testing.M) {
	var err error
	boltdb, err = BoltHandler(testBoltPath)
	if err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

func TestPut(t *testing.T) {
	db, err := NewBolt[TestValue]("test-put", boltdb)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key-%d", i)
		value := &TestValue{
			Name: fmt.Sprintf("name-%d", i),
			Desc: fmt.Sprintf("desc-%d", i),
		}
		err = db.Put(key, value)
		if err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key-%d", i)
		expect := &TestValue{
			Name: fmt.Sprintf("name-%d", i),
			Desc: fmt.Sprintf("desc-%d", i),
		}
		value, err := db.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(value, expect) {
			t.Fatalf("Unexpect value %+v", value)
		}
	}
}

func TestDelete(t *testing.T) {
	db, err := NewBolt[TestValue]("test-delete", boltdb)
	if err != nil {
		t.Fatal(err)
	}

	expect := &TestValue{
		Name: "test-delete",
		Desc: "My name is bolt",
	}

	err = db.Put("to-delete", expect)
	if err != nil {
		t.Fatal(err)
	}

	value, err := db.Get("to-delete")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, expect) {
		t.Fatalf("Unexpect value %+v", value)
	}

	err = db.Delete("to-delete")
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Get("to-delete")
	if !IsNotFound(err) {
		t.Fatalf("Unexpect error: %v", err)
	}
}

func TestPop(t *testing.T) {
	db, err := NewBolt[TestValue]("test-pop", boltdb)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("%d", i)
		value := &TestValue{
			Name: fmt.Sprintf("name-%d", i),
			Desc: fmt.Sprintf("desc-%d", i),
		}
		err = db.Put(key, value)
		if err != nil {
			t.Fatal(err)
		}
	}

	// We should pop 20 items
	var kvs []*KeyValue[TestValue]
	for {
		kv, err := db.Pop()
		if IsEOF(err) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		kvs = append(kvs, kv)
	}

	if len(kvs) != 20 {
		t.Fatalf("Unexpect kvs length: %v", len(kvs))
	}

	for _, kv := range kvs {
		idx, err := strconv.Atoi(kv.Key)
		if err != nil {
			t.Fatal(err)
		}
		expect := &TestValue{
			Name: fmt.Sprintf("name-%d", idx),
			Desc: fmt.Sprintf("desc-%d", idx),
		}
		if !reflect.DeepEqual(expect, kv.Value) {
			t.Fatalf("Unexpect value %+v", kv.Value)
		}
	}
}

func TestList(t *testing.T) {
	db, err := NewBolt[TestValue]("test-list", boltdb)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("%d", i)
		value := &TestValue{
			Name: fmt.Sprintf("name-%d", i),
			Desc: fmt.Sprintf("desc-%d", i),
		}
		err = db.Put(key, value)
		if err != nil {
			t.Fatal(err)
		}
	}

	kvs, err := db.List()
	if err != nil {
		t.Fatal(err)
	}

	if len(kvs) != 20 {
		t.Fatalf("Unexpect kvs length: %v", len(kvs))
	}

	for _, kv := range kvs {
		idx, err := strconv.Atoi(kv.Key)
		if err != nil {
			t.Fatal(err)
		}
		expect := &TestValue{
			Name: fmt.Sprintf("name-%d", idx),
			Desc: fmt.Sprintf("desc-%d", idx),
		}
		if !reflect.DeepEqual(expect, kv.Value) {
			t.Fatalf("Unexpect value %+v", kv.Value)
		}
	}
}

func TestCount(t *testing.T) {
	db, err := NewBolt[TestValue]("test-count", boltdb)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("count-%d", i)
		value := &TestValue{
			Name: fmt.Sprintf("name-%d", i),
			Desc: fmt.Sprintf("desc-%d", i),
		}
		err = db.Put(key, value)
		if err != nil {
			t.Fatal(err)
		}
	}

	size, err := db.Count()
	if err != nil {
		t.Fatal(err)
	}

	if size != 50 {
		t.Fatalf("Unexpect size %v", size)
	}
}
