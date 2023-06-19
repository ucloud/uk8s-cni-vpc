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
	"encoding/json"
	"fmt"
	"time"

	"github.com/boltdb/bolt"
)

type Bolt[T any] struct {
	db *bolt.DB

	bucket []byte
}

func BoltHandler(path string) (*bolt.DB, error) {
	return bolt.Open(path, 0600, &bolt.Options{Timeout: 10 * time.Second})
}

func NewBolt[T any](bucket string, db *bolt.DB) (Database[T], error) {
	bucketData := []byte(bucket)
	err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketData)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("Boltdb create bucket %q error: %v", bucket, err)
	}
	return &Bolt[T]{
		db:     db,
		bucket: bucketData,
	}, nil
}

func (b *Bolt[T]) Put(key string, value *T) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("Marshal boltdb value error: %v", err)
	}

	err = b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.bucket)
		return bucket.Put([]byte(key), data)
	})
	if err != nil {
		return fmt.Errorf("Write boltdb error: %v", err)
	}

	return nil
}

func (b *Bolt[T]) Get(key string) (*T, error) {
	var data []byte
	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.bucket)
		data = bucket.Get([]byte(key))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Read boltdb error: %v", err)
	}
	if len(data) == 0 {
		return nil, ErrNotFound
	}

	return b.decodeValue(data)
}

func (b *Bolt[T]) Count() (int, error) {
	var length int
	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.bucket)
		length = bucket.Stats().KeyN
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("Read boltdb length error: %v", err)
	}
	return length, nil
}

func (b *Bolt[T]) Delete(key string) error {
	err := b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.bucket)
		return bucket.Delete([]byte(key))
	})
	if err != nil {
		return fmt.Errorf("Delete boltdb error: %v", err)
	}
	return nil
}

func (b *Bolt[T]) Pop() (*KeyValue[T], error) {
	var key []byte
	var data []byte
	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.bucket)
		cursor := bucket.Cursor()
		key, data = cursor.First()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Read boltdb error: %v", err)
	}

	if len(key) == 0 {
		return nil, EOF
	}

	value, err := b.decodeValue(data)
	if err != nil {
		return nil, err
	}

	kv := &KeyValue[T]{Key: string(key), Value: value}
	err = b.Delete(kv.Key)
	if err != nil {
		return nil, fmt.Errorf("Delete kv after pop error: %v", err)
	}

	return kv, nil
}

func (b *Bolt[T]) List() ([]*KeyValue[T], error) {
	var kvs []*KeyValue[T]
	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.bucket)
		cursor := bucket.Cursor()
		for key, data := cursor.First(); key != nil; key, data = cursor.Next() {
			value, err := b.decodeValue(data)
			if err != nil {
				return err
			}
			kv := &KeyValue[T]{Key: string(key), Value: value}
			kvs = append(kvs, kv)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("List boltdb error: %v", err)
	}
	return kvs, nil
}

func (b *Bolt[T]) decodeValue(data []byte) (*T, error) {
	var value T
	err := json.Unmarshal(data, &value)
	if err != nil {
		return nil, fmt.Errorf("Decode boltdb value error: %v", err)
	}
	return &value, nil
}

func (b *Bolt[T]) Close() error {
	return b.db.Close()
}
