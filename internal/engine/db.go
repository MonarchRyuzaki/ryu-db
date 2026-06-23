package engine

import (
	"strings"

	"github.com/MonarchRyuzaki/ryu-db/internal/storage"
)

// DB is the MVCC Coordinator that wraps the generic B-Tree storage engine.
type DB struct {
	index storage.Index
	txMgr *storage.TransactionManager
}

// NewDB creates a new MVCC Database wrapper.
func NewDB(index storage.Index, txMgr *storage.TransactionManager) *DB {
	return &DB{
		index: index,
		txMgr: txMgr,
	}
}

// Set inserts or updates a key with a new MVCC version.
func (db *DB) Set(txID storage.TxnID, key string, value string) error {
	mvccKey := storage.BuildMVCCKey([]byte(key), uint64(txID))
	err := db.index.Insert(mvccKey, []byte(value), db.txMgr)
	if err != nil {
		if strings.HasPrefix(err.Error(), "write-write conflict") {
			db.index.Rollback(txID, db.txMgr)
		}
		return err
	}
	return nil
}

// Get retrieves the latest committed version of the key.
func (db *DB) Get(txID storage.TxnID, key string) (string, error) {
	mvccKey := storage.BuildMVCCKey([]byte(key), uint64(txID))

	valBytes, err := db.index.FindLatest(mvccKey, db.txMgr)
	if err != nil {
		return "", err
	}
	return string(valBytes), nil
}

// Delete marks the key as deleted by inserting a Tombstone version.
func (db *DB) Delete(txID storage.TxnID, key string) error {
	mvccKey := storage.BuildMVCCKey([]byte(key), uint64(txID))
	err := db.index.Delete(mvccKey, db.txMgr)
	if err != nil {
		if strings.HasPrefix(err.Error(), "write-write conflict") {
			db.index.Rollback(txID, db.txMgr)
		}
		return err
	}
	return nil
}

// Rollback manually aborts a transaction, triggering the undo phase.
func (db *DB) Rollback(txID storage.TxnID) error {
	return db.index.Rollback(txID, db.txMgr)
}

// Commit finalizes a transaction.
func (db *DB) Commit(txID storage.TxnID) error {
	return db.index.Commit(txID, db.txMgr)
}
