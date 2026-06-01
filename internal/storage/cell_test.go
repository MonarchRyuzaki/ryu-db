package storage

import (
	"bytes"
	"testing"
)

func TestKeyCell_Serialization(t *testing.T) {
	pageID := uint32(1024)
	key := []byte("some-internal-key")
	cell := NewKeyCell(pageID, key)

	// Test Size
	expectedSize := 4 + 2 + len(key)
	if cell.Size() != expectedSize {
		t.Errorf("Expected size %d, got %d", expectedSize, cell.Size())
	}

	// Test Serialization
	data := cell.Serialize()
	if len(data) != expectedSize {
		t.Errorf("Serialized data length mismatch: expected %d, got %d", expectedSize, len(data))
	}

	// Test Deserialization
	deserialized := DeserializeKeyCell(data)
	if deserialized.ChildPageID != pageID {
		t.Errorf("Expected ChildPageID %d, got %d", pageID, deserialized.ChildPageID)
	}
	if !bytes.Equal(deserialized.Key, key) {
		t.Errorf("Expected Key %s, got %s", string(key), string(deserialized.Key))
	}
}

func TestKVCell_Serialization(t *testing.T) {
	flag := uint8(1)
	key := []byte("user:123")
	value := []byte("John Doe")
	cell := NewKVCell(flag, key, value)

	// Test Size
	expectedSize := 1 + 2 + 2 + len(key) + len(value)
	if cell.Size() != expectedSize {
		t.Errorf("Expected size %d, got %d", expectedSize, cell.Size())
	}

	// Test Serialization
	data := cell.Serialize()
	if len(data) != expectedSize {
		t.Errorf("Serialized data length mismatch: expected %d, got %d", expectedSize, len(data))
	}

	// Test Deserialization
	deserialized := DeserializeKVCell(data)
	if deserialized.Flag != flag {
		t.Errorf("Expected Flag %d, got %d", flag, deserialized.Flag)
	}
	if !bytes.Equal(deserialized.Key, key) {
		t.Errorf("Expected Key %s, got %s", string(key), string(deserialized.Key))
	}
	if !bytes.Equal(deserialized.Value, value) {
		t.Errorf("Expected Value %s, got %s", string(value), string(deserialized.Value))
	}
}

func TestKeyCell_EmptyKey(t *testing.T) {
	cell := NewKeyCell(1, []byte(""))
	data := cell.Serialize()
	deserialized := DeserializeKeyCell(data)

	if len(deserialized.Key) != 0 {
		t.Errorf("Expected empty key, got length %d", len(deserialized.Key))
	}
}

func TestKVCell_EmptyValue(t *testing.T) {
	cell := NewKVCell(0, []byte("key"), []byte(""))
	data := cell.Serialize()
	deserialized := DeserializeKVCell(data)

	if len(deserialized.Value) != 0 {
		t.Errorf("Expected empty value, got length %d", len(deserialized.Value))
	}
}
