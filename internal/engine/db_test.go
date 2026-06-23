package engine

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MonarchRyuzaki/db-internals/internal/storage"
)

func TestMVCC_BasicTimeTravel(t *testing.T) {
	// 1. Setup
	dbPath := "test_mvcc_db"
	os.RemoveAll(dbPath)
	os.MkdirAll(dbPath, 0755)
	defer os.RemoveAll(dbPath)

	bTree, err := storage.NewBTree("test_mvcc", dbPath)
	if err != nil {
		t.Fatalf("Failed to create BTree: %v", err)
	}
	defer bTree.Close()

	txMgr := storage.NewTransactionManager()
	mvccDB := NewDB(bTree, txMgr)

	// 2. Insert Version 1
	tx1 := txMgr.Begin()
	err = mvccDB.Set(tx1, "UserA", "Alice_V1")
	if err != nil {
		t.Fatalf("Failed to set V1: %v", err)
	}
	txMgr.Commit(tx1)

	val, err := mvccDB.Get(tx1, "UserA")
	if err != nil || val != "Alice_V1" {
		t.Fatalf("Expected Alice_V1, got %v (err: %v)", val, err)
	}

	// 3. Sleep slightly to ensure TxID (UnixNano) increases significantly
	time.Sleep(2 * time.Millisecond)

	// Capture the TxID boundary
	boundaryTxID := uint64(time.Now().UnixNano())

	time.Sleep(2 * time.Millisecond)

	// 4. Insert Version 2
	tx2 := txMgr.Begin()
	err = mvccDB.Set(tx2, "UserA", "Alice_V2")
	if err != nil {
		t.Fatalf("Failed to set V2: %v", err)
	}
	txMgr.Commit(tx2)

	// 5. Get should now return Version 2
	tx3 := txMgr.Begin()
	val, err = mvccDB.Get(tx3, "UserA")
	if err != nil || val != "Alice_V2" {
		t.Fatalf("Expected Alice_V2, got %v (err: %v)", val, err)
	}
	txMgr.Commit(tx3)

	// 6. Time travel! Ask the raw B-Tree for the version of "UserA" at the boundaryTxID
	oldSearchKey := storage.BuildMVCCKey([]byte("UserA"), boundaryTxID)
	oldValBytes, err := bTree.FindLatest(oldSearchKey, txMgr)
	if err != nil {
		t.Fatalf("Failed to time travel: %v", err)
	}

	if string(oldValBytes) != "Alice_V1" {
		t.Fatalf("Time travel failed. Expected Alice_V1, got %v", string(oldValBytes))
	}
}

func TestMVCC_DeleteTimeTravel(t *testing.T) {
	dbPath := "test_mvcc_del_db"
	os.RemoveAll(dbPath)
	os.MkdirAll(dbPath, 0755)
	defer os.RemoveAll(dbPath)

	bTree, err := storage.NewBTree("test_mvcc_del", dbPath)
	if err != nil {
		t.Fatalf("Failed to create BTree: %v", err)
	}
	defer bTree.Close()

	txMgr := storage.NewTransactionManager()
	mvccDB := NewDB(bTree, txMgr)

	// 1. Insert Version 1
	tx1 := txMgr.Begin()
	mvccDB.Set(tx1, "UserB", "Bob_V1")
	txMgr.Commit(tx1)
	
	time.Sleep(2 * time.Millisecond)
	boundaryTxID := uint64(time.Now().UnixNano())
	time.Sleep(2 * time.Millisecond)

	// 2. Delete the key (Inserts a Tombstone Version)
	tx2 := txMgr.Begin()
	mvccDB.Delete(tx2, "UserB")
	txMgr.Commit(tx2)

	// 3. Ensure it is deleted from the current perspective
	tx3 := txMgr.Begin()
	_, err = mvccDB.Get(tx3, "UserB")
	if err == nil {
		t.Fatalf("Expected error getting deleted key")
	}
	txMgr.Commit(tx3)

	// 4. Time travel back to before it was deleted!
	oldSearchKey := storage.BuildMVCCKey([]byte("UserB"), boundaryTxID)
	oldValBytes, err := bTree.FindLatest(oldSearchKey, txMgr)
	if err != nil {
		t.Fatalf("Failed to time travel to deleted key: %v", err)
	}

	if string(oldValBytes) != "Bob_V1" {
		t.Fatalf("Time travel failed. Expected Bob_V1, got %v", string(oldValBytes))
	}
}

func TestDB_CommitRollback(t *testing.T) {
	dbPath := "test_commit_rollback_db"
	os.RemoveAll(dbPath)
	os.MkdirAll(dbPath, 0755)
	defer os.RemoveAll(dbPath)

	index, err := storage.NewBTree("test_commit_rollback", dbPath)
	if err != nil {
		t.Fatalf("Failed to create BTree: %v", err)
	}
	defer index.Close()

	txMgr := storage.NewTransactionManager()
	db := NewDB(index, txMgr)

	// Test Commit
	tx1 := txMgr.Begin()
	err = db.Set(tx1, "key1", "val1")
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	
	// Read before commit should succeed for the same transaction
	val, err := db.Get(tx1, "key1")
	if err != nil || val != "val1" {
		t.Fatalf("Expected val1, got %s, err: %v", val, err)
	}
	
	db.Commit(tx1)

	// New transaction should see committed value
	tx2 := txMgr.Begin()
	val, err = db.Get(tx2, "key1")
	if err != nil || val != "val1" {
		t.Fatalf("Expected val1 after commit, got %s, err: %v", val, err)
	}

	// Test Manual Rollback
	tx3 := txMgr.Begin()
	err = db.Set(tx3, "key2", "val2")
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	db.Rollback(tx3)

	// New transaction should NOT see rolled back value
	tx4 := txMgr.Begin()
	_, err = db.Get(tx4, "key2")
	if err == nil {
		t.Fatalf("Expected error (key not found) for rolled back key2")
	}
}

func TestDB_WriteWriteConflict(t *testing.T) {
	dbPath := "test_conflict_db"
	os.RemoveAll(dbPath)
	os.MkdirAll(dbPath, 0755)
	defer os.RemoveAll(dbPath)

	index, err := storage.NewBTree("test_conflict", dbPath)
	if err != nil {
		t.Fatalf("Failed to create BTree: %v", err)
	}
	defer index.Close()

	txMgr := storage.NewTransactionManager()
	db := NewDB(index, txMgr)

	tx1 := txMgr.Begin()
	tx2 := txMgr.Begin()

	// tx1 writes key1
	err = db.Set(tx1, "conflict_key", "val_tx1")
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// tx2 tries to write key1 and should fail
	err = db.Set(tx2, "conflict_key", "val_tx2")
	if err == nil || !strings.Contains(err.Error(), "write-write conflict") {
		t.Fatalf("Expected WW conflict, got: %v", err)
	}

	// Because of WW conflict, tx2 should be automatically rolled back!
	status := txMgr.GetStatus(tx2)
	if status != storage.TXN_ROLLEDBACK {
		t.Fatalf("Expected tx2 to be automatically rolled back, but status is %d", status)
	}
}

func TestDB_RecoveryUndoPhase(t *testing.T) {
	dbPath := "test_undo_db"
	os.RemoveAll(dbPath)
	os.MkdirAll(dbPath, 0755)
	defer os.RemoveAll(dbPath)
	
	index, err := storage.NewBTree("test_undo", dbPath)
	if err != nil {
		t.Fatalf("Failed to create BTree: %v", err)
	}

	txMgr := storage.NewTransactionManager()
	db := NewDB(index, txMgr)

	// Tx1 commits
	tx1 := txMgr.Begin()
	db.Set(tx1, "committed_key", "committed_val")
	db.Commit(tx1)

	// Tx2 does NOT commit (Crash simulation)
	tx2 := txMgr.Begin()
	db.Set(tx2, "uncommitted_key", "uncommitted_val")
	
	// Simulate Crash
	index = nil
	db = nil
	txMgr = nil

	// Restart and Recover
	index2, err := storage.NewBTree("test_undo", dbPath)
	if err != nil {
		t.Fatalf("Failed to recover BTree: %v", err)
	}
	defer index2.Close()
	txMgr2 := storage.NewTransactionManager()
	db2 := NewDB(index2, txMgr2)

	tx3 := txMgr2.Begin()
	
	// The committed key should exist
	val, err := db2.Get(tx3, "committed_key")
	if err != nil || val != "committed_val" {
		t.Fatalf("Expected committed_val, got %s, err: %v", val, err)
	}

	// The uncommitted key should NOT exist (Undone during recovery)
	_, err = db2.Get(tx3, "uncommitted_key")
	if err == nil {
		t.Fatalf("Expected uncommitted_key to be undone by recovery, but it was found!")
	}
}
