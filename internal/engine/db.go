package engine

import (
	"github.com/MonarchRyuzaki/db-internals/internal/storage"
)

// DB is the MVCC Coordinator that wraps the generic B-Tree storage engine.
type DB struct {
	index storage.Index
}

// NewDB creates a new MVCC Database wrapper.
func NewDB(index storage.Index) *DB {
	return &DB{
		index: index,
	}
}

// Set inserts or updates a key with a new MVCC version.
func (db *DB) Set(txID storage.TxnID, key string, value string) error {
	mvccKey := storage.BuildMVCCKey([]byte(key), uint64(txID))
	return db.index.Insert(mvccKey, []byte(value))
}

// Get retrieves the latest committed version of the key.
func (db *DB) Get(txID storage.TxnID, key string) (string, error) {
	mvccKey := storage.BuildMVCCKey([]byte(key), uint64(txID))

	valBytes, err := db.index.FindLatest(mvccKey)
	if err != nil {
		return "", err
	}
	return string(valBytes), nil
}

// Delete marks the key as deleted by inserting a Tombstone version.
func (db *DB) Delete(txID storage.TxnID, key string) error {
	mvccKey := storage.BuildMVCCKey([]byte(key), uint64(txID))
	return db.index.Delete(mvccKey)
}
