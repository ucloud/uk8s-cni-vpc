package storage

import (
	"errors"
)

var ErrNotFound = errors.New("not found")

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
