package storage

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/boltdb/bolt"
)

type Disk[T any] struct {
	db *bolt.DB

	name string

	data []*Item[T]
	lock sync.RWMutex
}

func NewDBFileHandler(path string) (*bolt.DB, error) {
	return bolt.Open(path, 0600, &bolt.Options{Timeout: 10 * time.Second})
}

func NewDisk[T any](name string, db *bolt.DB) (Storage[T], error) {
	d := &Disk[T]{
		db:   db,
		name: name,
	}
	err := d.load()
	if err != nil {
		return nil, fmt.Errorf("failed to load bolt disk: %v", err)
	}
	return d, nil
}

func (d *Disk[T]) load() error {
	d.lock.Lock()
	defer d.lock.Unlock()

	err := d.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(d.name))
		return err
	})
	if err != nil {
		return err
	}

	return d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(d.name))
		cursor := b.Cursor()
		for k, data := cursor.First(); k != nil; k, data = cursor.Next() {
			key := string(k)
			var val T
			err := json.Unmarshal(data, &val)
			if err != nil {
				return err
			}
			d.data = append(d.data, &Item[T]{
				Key:   key,
				Value: &val,
			})
		}
		return nil
	})
}

func (d *Disk[T]) Set(key string, value *T) error {
	d.lock.Lock()
	defer d.lock.Unlock()

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	err = d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(d.name))
		return b.Put([]byte(key), data)
	})
	if err != nil {
		return err
	}
	if len(d.data) > 0 {
		// Delete the old item with same key, to make sure the key is
		// unique and the new item locate at the end of the queue.
		d.deleteItem(key)
	}

	d.data = append(d.data, &Item[T]{
		Key:   key,
		Value: value,
	})
	return nil
}

func (d *Disk[T]) Get(key string) (*T, error) {
	d.lock.RLock()
	defer d.lock.RUnlock()

	for _, item := range d.data {
		if item.Key == key {
			return item.Value, nil
		}
	}
	return nil, ErrNotFound
}

func (d *Disk[T]) List() ([]*T, error) {
	d.lock.RLock()
	defer d.lock.RUnlock()

	vals := make([]*T, len(d.data))
	for i, item := range d.data {
		vals[i] = item.Value
	}
	return vals, nil
}

func (d *Disk[T]) Pop() (*T, error) {
	d.lock.Lock()
	defer d.lock.Unlock()

	if len(d.data) == 0 {
		return nil, ErrNotFound
	}

	result := d.data[0]
	err := d.deleteDB(result.Key)
	if err != nil {
		return nil, err
	}
	d.data = d.data[1:]

	return result.Value, nil
}

func (d *Disk[T]) Delete(key string) error {
	d.lock.Lock()
	defer d.lock.Unlock()
	if len(d.data) == 0 {
		return nil
	}

	if d.deleteItem(key) {
		return d.deleteDB(key)
	}
	return nil
}

func (d *Disk[T]) Len() int {
	d.lock.RLock()
	defer d.lock.RUnlock()
	return len(d.data)
}

func (d *Disk[T]) Close() error {
	return d.db.Close()
}

func (d *Disk[T]) deleteItem(key string) bool {
	newItems := make([]*Item[T], 0, len(d.data)-1)
	for _, item := range d.data {
		if item.Key == key {
			continue
		}
		newItems = append(newItems, item)
	}
	if len(d.data) == len(newItems) {
		return false
	}
	d.data = newItems
	return true
}

func (d *Disk[T]) deleteDB(key string) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(d.name))
		return b.Delete([]byte(key))
	})
}
