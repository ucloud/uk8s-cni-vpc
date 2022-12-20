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

package storage

import (
	"errors"
)

var ErrEmpty = errors.New("storage is empty")

// Storage persistent storage on disk
// go get github.com/br0xen/boltbrowser
// boltbrowser <filename>
type Storage[T any] interface {
	Set(key string, value *T) error
	Get(key string) (*T, error)
	List() ([]*T, error)
	Delete(key string) error
	Pop() (*T, error)
	Len() int
	Close() error
}

type Item[T any] struct {
	Key   string
	Value *T
}

func GetKey(podName, podNS, sandboxId string) string {
	return podName + "-" + podNS + "-" + sandboxId
}
