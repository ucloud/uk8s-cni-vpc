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

type Database[T any] interface {
	Put(key string, value *T) error
	Get(key string) (*T, error)

	Delete(key string) error

	Pop() (*KeyValue[T], error)

	List() ([]*KeyValue[T], error)

	Count() (int, error)

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
