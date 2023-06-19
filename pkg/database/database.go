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

import "errors"

var (
	EOF         = errors.New("No remain item to pop")
	ErrNotFound = errors.New("Could not find this key in database")
)

// Database is a Key-Value based database interface. It is used to store data
// that requires persistence.
// Implementations may be disk-based, or other independent databases.
type Database[T any] interface {
	// Put a key-value. If the key is already exists, it will be replaced.
	Put(key string, value *T) error

	// Get a key-value. If the key is not exists, we will return `ErrNotFound`.
	// You can use `database.IsNotFound` to check.
	Get(key string) (*T, error)

	// Delete a key-value. If the key is not exists, we won't return error.
	Delete(key string) error

	// Pop return the first key-value, and delete it. The pop order is not
	// guaranteed (unlike traditional stack).
	// If the database is empty and no key-value to pop, this will return `EOF`
	// error, you can use `database.IsEOF` to check.
	Pop() (*KeyValue[T], error)

	// List returns all key-values in database.
	List() ([]*KeyValue[T], error)

	// Count returns the number of key-values in database.
	Count() (int, error)

	// Close closes the database.
	Close() error
}

type KeyValue[T any] struct {
	Key   string
	Value *T
}

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

func IsEOF(err error) bool {
	return errors.Is(err, EOF)
}

func PodKey(podName, podNS, sandboxId string) string {
	return podName + "-" + podNS + "-" + sandboxId
}
