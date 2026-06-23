package storage

import (
	"bytes"
	"fmt"
	"testing"
)

// TestRecovery_CrashAndRestart simulates a database crash by writing records,
// forcibly abandoning the BTree without flushing buffers, and then restarting
// a new BTree instance to verify that the ARIES recovery phase restores the data.
func TestRecovery_CrashAndRestart(t *testing.T) {
	dir := t.TempDir()

	// 1. Start the DB and write some records
	tree1, err := NewBTree("recovery_test", dir)
	if err != nil {
		t.Fatalf("Failed to create BTree: %v", err)
	}

	txMgr := NewTransactionManager()

	records := map[string]string{
		"alice":   "alice@example.com",
		"bob":     "bob@example.com",
		"charlie": "charlie@example.com",
	}

	for k, v := range records {
		err := tree1.Insert([]byte(k), []byte(v), txMgr)
		if err != nil {
			t.Fatalf("Failed to insert %s: %v", k, err)
		}
	}

	// Verify they exist in tree1
	for k, expectedValue := range records {
		val, err := tree1.Find([]byte(k))
		if err != nil {
			t.Fatalf("Could not find key %s in tree1: %v", k, err)
		}
		if !bytes.Equal(val, []byte(expectedValue)) {
			t.Fatalf("tree1 Expected %s, got %s", expectedValue, string(val))
		}
	}

	// Trigger a checkpoint to test fuzzy checkpoint recovery
	err = tree1.Checkpoint()
	if err != nil {
		t.Fatalf("Failed to write checkpoint: %v", err)
	}

	// Write more records after checkpoint
	moreRecords := map[string]string{
		"dave": "dave@example.com",
		"eve":  "eve@example.com",
	}
	for k, v := range moreRecords {
		err := tree1.Insert([]byte(k), []byte(v), txMgr)
		if err != nil {
			t.Fatalf("Failed to insert %s: %v", k, err)
		}
	}

	// CRASH SIMULATION: Do NOT call tree1.Close()!
	// This means any dirty pages in the BufferManager are NOT flushed to disk.
	// Only the WAL on disk contains the truth for `dave` and `eve`.
	// We just drop the reference to tree1.
	tree1 = nil

	// 2. Restart the DB on the same files
	// This should trigger ARIES recovery.
	tree2, err := NewBTree("recovery_test", dir)
	if err != nil {
		t.Fatalf("Failed to recover BTree: %v", err)
	}
	defer tree2.Close()

	// 3. Verify ALL records were restored by the Redo phase
	for k, expectedValue := range records {
		val, err := tree2.Find([]byte(k))
		if err != nil {
			t.Fatalf("Could not find key %s after recovery: %v", k, err)
		}
		if !bytes.Equal(val, []byte(expectedValue)) {
			t.Fatalf("After recovery expected %s, got %s", expectedValue, string(val))
		}
	}

	for k, expectedValue := range moreRecords {
		val, err := tree2.Find([]byte(k))
		if err != nil {
			t.Fatalf("Could not find key %s (inserted after checkpoint) after recovery: %v", k, err)
		}
		if !bytes.Equal(val, []byte(expectedValue)) {
			t.Fatalf("After recovery expected %s, got %s", expectedValue, string(val))
		}
	}

	// 4. Verify we can still insert after recovery
	err = tree2.Insert([]byte("frank"), []byte("frank@example.com"), txMgr)
	if err != nil {
		t.Fatalf("Failed to insert after recovery: %v", err)
	}

	val, err := tree2.Find([]byte("frank"))
	if err != nil || !bytes.Equal(val, []byte("frank@example.com")) {
		t.Fatalf("Failed to find newly inserted key after recovery")
	}
}

// TestRecovery_SplitRecovery simulates a crash after a page split.
func TestRecovery_SplitRecovery(t *testing.T) {
	dir := t.TempDir()

	tree1, err := NewBTree("split_recovery", dir)
	if err != nil {
		t.Fatalf("Failed to create BTree: %v", err)
	}

	txMgr := NewTransactionManager()

	// Insert enough records to trigger a root split
	for i := 0; i < 500; i++ {
		k := fmt.Sprintf("key%04d", i)
		v := fmt.Sprintf("val%04d", i)
		err := tree1.Insert([]byte(k), []byte(v), txMgr)
		if err != nil {
			t.Fatalf("Failed to insert %s: %v", k, err)
		}
	}

	// CRASH SIMULATION
	tree1 = nil

	// RECOVER
	tree2, err := NewBTree("split_recovery", dir)
	if err != nil {
		t.Fatalf("Failed to recover BTree: %v", err)
	}
	defer tree2.Close()

	// Verify records
	for i := 0; i < 500; i++ {
		k := fmt.Sprintf("key%04d", i)
		v := fmt.Sprintf("val%04d", i)
		val, err := tree2.Find([]byte(k))
		if err != nil {
			t.Fatalf("Could not find key %s after recovery: %v", k, err)
		}
		if !bytes.Equal(val, []byte(v)) {
			t.Fatalf("After recovery expected %s, got %s", v, string(val))
		}
	}
}
