package storage

import (
	"fmt"
	"os"
	"testing"
)

type testDiskValueType struct {
	Key  string
	Name string
	Desc string
}

var testDiskInstance Storage[testDiskValueType]

func newTestDiskInstance() (Storage[testDiskValueType], error) {
	db, err := NewDBFileHandler("/tmp/test-cni-storage")
	if err != nil {
		return nil, err
	}
	return NewDisk[testDiskValueType]("test-cni-storage", db)
}

func addToTestDiskInstance(key string, val *testDiskValueType, t *testing.T) {
	err := testDiskInstance.Set(key, val)
	if err != nil {
		t.Fatal(err)
	}
}

func deleteTestDiskFile(t *testing.T) {
	err := os.Remove("/tmp/test-cni-storage")
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
}

func TestMain(m *testing.M) {
	var err error
	testDiskInstance, err = newTestDiskInstance()
	if err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func TestDiskPersistence(t *testing.T) {
	defer deleteTestDiskFile(t)
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key%d", i)
		value := fmt.Sprintf("value%d", i)
		err := testDiskInstance.Set(key, &testDiskValueType{
			Name: value,
			Desc: "simple desc",
		})
		if err != nil {
			t.Fatalf("failed to save for %s: %v", key, err)
		}
	}
	err := testDiskInstance.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Ok, let's create another instance to make sure the data was
	// saved to local disk.
	otherIns, err := newTestDiskInstance()
	if err != nil {
		t.Fatal(err)
	}
	if otherIns.Len() != 10 {
		t.Fatalf("unexpected instance len: %d", otherIns.Len())
	}
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key%d", i)
		expectValue := fmt.Sprintf("value%d", i)
		val, err := otherIns.Get(key)
		if err != nil {
			t.Fatalf("failed to get %s: %v", key, err)
		}
		if val.Name != expectValue {
			t.Fatalf("unexpected name %q", val.Name)
		}
		if val.Desc != "simple desc" {
			t.Fatalf("unexpected desc %q", val.Desc)
		}
	}

	// Delete something, it should be removed from disk too.
	err = otherIns.Delete("key0")
	if err != nil {
		t.Fatal(err)
	}
	err = otherIns.Delete("key1")
	if err != nil {
		t.Fatal(err)
	}
	err = otherIns.Delete("key2")
	if err != nil {
		t.Fatal(err)
	}
	err = otherIns.Close()
	if err != nil {
		t.Fatal(err)
	}

	otherIns, err = newTestDiskInstance()
	if err != nil {
		t.Fatal(err)
	}
	for i := 3; i < 10; i++ {
		key := fmt.Sprintf("key%d", i)
		expectValue := fmt.Sprintf("value%d", i)
		val, err := otherIns.Get(key)
		if err != nil {
			t.Fatalf("failed to get %s: %v", key, err)
		}
		if val.Name != expectValue {
			t.Fatalf("unexpected name %q", val.Name)
		}
		if val.Desc != "simple desc" {
			t.Fatalf("unexpected desc %q", val.Desc)
		}
	}
	// recover the origin intsnace
	testDiskInstance = otherIns
	testDiskCleanup(t)
}

type testDiskKeyValue struct {
	Key   string
	Value string
}

func testDiskCheckKeyValues(t *testing.T, kvs []testDiskKeyValue) {
	vals, err := testDiskInstance.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(kvs) != len(vals) {
		t.Fatalf("unpexect vals len %d, expect %d", len(vals), len(kvs))
	}
	for i, kv := range kvs {
		val := vals[i]
		if kv.Key != val.Key {
			t.Fatalf("order %d: unpexect key %s, expect %s", i, val.Key, kv.Key)
		}
		if kv.Value != val.Name {
			t.Fatalf("order %d: unpexect value %s, expect %s", i, val.Name, kv.Value)
		}
	}
}

func testDiskCleanup(t *testing.T) {
	for testDiskInstance.Len() > 0 {
		_, err := testDiskInstance.Pop()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiskList(t *testing.T) {
	testDiskAddKeyValues := func(t *testing.T, kvs []testDiskKeyValue) {
		for _, kv := range kvs {
			err := testDiskInstance.Set(kv.Key, &testDiskValueType{
				Key:  kv.Key,
				Name: kv.Value,
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	defer deleteTestDiskFile(t)
	testCases := []struct {
		adds    []testDiskKeyValue
		expects []testDiskKeyValue
	}{
		{
			adds: []testDiskKeyValue{
				{Key: "key1", Value: "value1"},
				{Key: "key1", Value: "value2"},
				{Key: "key2", Value: "value3"},
				{Key: "key1", Value: "value4"},
			},
			expects: []testDiskKeyValue{
				{Key: "key2", Value: "value3"},
				{Key: "key1", Value: "value4"},
			},
		},
		{
			adds: []testDiskKeyValue{
				{Key: "key1", Value: "value1"},
				{Key: "key2", Value: "value2"},
				{Key: "key3", Value: "value3"},
				{Key: "key1", Value: "value4"},
				{Key: "key2", Value: "value5"},
				{Key: "key1", Value: "value6"},
				{Key: "key3", Value: "value7"},
			},
			expects: []testDiskKeyValue{
				{Key: "key2", Value: "value5"},
				{Key: "key1", Value: "value6"},
				{Key: "key3", Value: "value7"},
			},
		},
	}

	for _, testCase := range testCases {
		testDiskCleanup(t)
		testDiskAddKeyValues(t, testCase.adds)
		testDiskCheckKeyValues(t, testCase.expects)
	}

}
