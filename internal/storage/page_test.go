package storage

import (
	"bytes"
	"testing"
)

func TestPage_BasicInsertGet(t *testing.T) {
	p := NewPage(true)
	p.SetPageID(1)
	p.SetPageType(PageTypeLeaf)
	p.SetLSN(100)

	// Verify Header Fields
	if !p.VerifyMagic() {
		t.Errorf("Magic number verification failed")
	}
	if p.GetPageType() != PageTypeLeaf {
		t.Errorf("PageType mismatch")
	}
	if p.GetLSN() != 100 {
		t.Errorf("LSN mismatch")
	}

	// Create some cells
	c1 := NewKVCell(0, []byte("name"), []byte("ryuzaki"))
	c2 := NewKVCell(0, []byte("age"), []byte("25"))

	data1 := c1.Serialize()
	data2 := c2.Serialize()

	// Insert
	s1, err := p.Insert(data1)
	if err != nil {
		t.Fatalf("Failed to insert cell 1: %v", err)
	}
	s2, err := p.Insert(data2)
	if err != nil {
		t.Fatalf("Failed to insert cell 2: %v", err)
	}

	if s1 != 0 || s2 != 1 {
		t.Errorf("Expected slot IDs 0 and 1, got %d and %d", s1, s2)
	}

	// Get and verify
	res1, err := p.Get(s1)
	if err != nil {
		t.Fatalf("Failed to get cell 1: %v", err)
	}
	dec1 := DeserializeKVCell(res1)
	if !bytes.Equal(dec1.Key, c1.Key) || !bytes.Equal(dec1.Value, c1.Value) {
		t.Errorf("Cell 1 mismatch")
	}

	res2, err := p.Get(s2)
	if err != nil {
		t.Fatalf("Failed to get cell 2: %v", err)
	}
	dec2 := DeserializeKVCell(res2)
	if !bytes.Equal(dec2.Key, c2.Key) || !bytes.Equal(dec2.Value, c2.Value) {
		t.Errorf("Cell 2 mismatch")
	}
}

func TestPage_Integrity(t *testing.T) {
	p := NewPage(true)
	c := NewKVCell(0, []byte("key"), []byte("value"))
	p.Insert(c.Serialize())

	p.SetChecksum()

	if !p.VerifyChecksum() {
		t.Errorf("Checksum verification failed on clean page")
	}

	p.data[PageSize-1] ^= 0xFF

	if p.VerifyChecksum() {
		t.Errorf("Checksum verification passed on corrupted page")
	}
}

func TestPage_Full(t *testing.T) {
	p := NewPage(true)
	// Large cell to fill page quickly
	largeKey := make([]byte, 2000)
	largeVal := make([]byte, 2000)
	cell := NewKVCell(0, largeKey, largeVal)
	data := cell.Serialize()

	_, err := p.Insert(data) 
	if err != nil {
		t.Fatalf("First insert failed: %v", err)
	}

	_, err = p.Insert(data) 
	if err == nil {
		t.Errorf("Expected page full error, got nil")
	}
}
