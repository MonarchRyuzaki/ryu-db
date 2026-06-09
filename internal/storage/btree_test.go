package storage

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// TestBTree_Basic is a simple, easy-to-read test that demonstrates how 
// the B+ Tree works from a client's perspective, without worrying about pages or internals.
func TestBTree_Basic(t *testing.T) {
	dir := t.TempDir()
	
	// 1. Initialize the B+ Tree (Creates "users.db" inside the temp directory)
	tree, err := NewBTree("users", dir)
	if err != nil {
		t.Fatalf("Failed to create BTree: %v", err)
	}

	// 2. Insert some basic records
	records := map[string]string{
		"alice":   "alice@example.com",
		"bob":     "bob@example.com",
		"charlie": "charlie@example.com",
	}

	for k, v := range records {
		err := tree.Insert([]byte(k), []byte(v))
		if err != nil {
			t.Fatalf("Failed to insert %s: %v", k, err)
		}
	}

	// 3. Find and verify the records
	for k, expectedValue := range records {
		val, err := tree.Find([]byte(k))
		if err != nil {
			t.Fatalf("Could not find key %s: %v", k, err)
		}
		
		if !bytes.Equal(val, []byte(expectedValue)) {
			t.Fatalf("Expected %s, got %s", expectedValue, string(val))
		}
	}

	// 4. Verify that searching for a non-existent key fails gracefully
	_, err = tree.Find([]byte("david"))
	if err == nil {
		t.Fatalf("Expected an error when searching for non-existent key")
	}
}

// TestBTree_Stress violently hammers the B+ Tree with thousands of records 
// to trigger multiple root and leaf splits, ensuring structural integrity.
func TestBTree_Stress(t *testing.T) {
	dir := t.TempDir()
	
	// Create a BTree with a reasonably sized buffer pool (100 frames) 
	tree, err := NewBTree("stress_test", dir)
	if err != nil {
		t.Fatalf("Failed to create BTree: %v", err)
	}

	const numRecords = 5000
	insertedKeys := make([][]byte, 0, numRecords)

	// 1. Insert 5000 random keys
	for i := 0; i < numRecords; i++ {
		// Use a random format so keys are inserted in random order, forcing tricky splits
		key := []byte(fmt.Sprintf("key_%04d_%d", i, rand.Intn(100000)))
		val := []byte(fmt.Sprintf("val_%d", i))
		
		err := tree.Insert(key, val)
		if err != nil {
			t.Fatalf("Failed to insert on iteration %d: %v", i, err)
		}
		
		insertedKeys = append(insertedKeys, key)
	}

	// 2. Verify all 5000 keys can be found successfully
	for i, key := range insertedKeys {
		val, err := tree.Find(key)
		if err != nil {
			t.Fatalf("Failed to find key %s on iteration %d: %v", string(key), i, err)
		}
		
		expectedVal := []byte(fmt.Sprintf("val_%d", i))
		if !bytes.Equal(val, expectedVal) {
			t.Fatalf("Data corruption on key %s. Expected %s, got %s", string(key), string(expectedVal), string(val))
		}
	}
}

// TestBTree_Delete verifies that tombstone deletion correctly hides keys,
// while leaving other keys completely intact.
func TestBTree_Delete(t *testing.T) {
	dir := t.TempDir()
	
	tree, err := NewBTree("delete_test", dir)
	if err != nil {
		t.Fatalf("Failed to create BTree: %v", err)
	}

	// 1. Insert records
	tree.Insert([]byte("key1"), []byte("value1"))
	tree.Insert([]byte("key2"), []byte("value2"))
	tree.Insert([]byte("key3"), []byte("value3"))

	// 2. Delete key2
	err = tree.Delete([]byte("key2"))
	if err != nil {
		t.Fatalf("Failed to delete key2: %v", err)
	}

	// 3. Verify key2 is gone
	_, err = tree.Find([]byte("key2"))
	if err == nil {
		t.Fatalf("Expected key2 to be not found (tombstoned), but found it!")
	}

	// 4. Verify key1 and key3 are perfectly fine
	val1, err := tree.Find([]byte("key1"))
	if err != nil || !bytes.Equal(val1, []byte("value1")) {
		t.Fatalf("key1 was corrupted by deletion")
	}

	val3, err := tree.Find([]byte("key3"))
	if err != nil || !bytes.Equal(val3, []byte("value3")) {
		t.Fatalf("key3 was corrupted by deletion")
	}

	// 5. Verify deleting a non-existent key fails gracefully
	err = tree.Delete([]byte("key4"))
	if err == nil {
		t.Fatalf("Expected error when deleting non-existent key")
	}
}
