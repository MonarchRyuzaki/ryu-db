package storage

import (
	"bytes"
	"os"
	"testing"
)

func TestPager_WriteAndRead(t *testing.T) {
	// Create a temporary file for testing
	f, err := os.CreateTemp("", "test_pager_*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	// Create and initialize a page
	p1 := NewPage(PageTypeLeaf)
	p1.SetPageID(0)
	p1.SetLSN(1234)

	// Add some data to p1
	c1 := NewKVCell(0, []byte("hello"), []byte("world"))
	_, err = p1.Insert(c1.Serialize())
	if err != nil {
		t.Fatalf("failed to insert cell: %v", err)
	}

	// Write Page 0
	err = WritePage(f, 0, p1)
	if err != nil {
		t.Fatalf("failed to write page: %v", err)
	}

	// Read it back into a new page
	p2 := NewPage(0)
	err = ReadPage(f, 0, p2)
	if err != nil {
		t.Fatalf("failed to read page: %v", err)
	}

	// Verify they are identical
	if !bytes.Equal(p1.data, p2.data) {
		t.Errorf("read page does not match written page")
	}

	// Verify specific fields
	if p2.GetPageType() != PageTypeLeaf {
		t.Errorf("expected page type %d, got %d", PageTypeLeaf, p2.GetPageType())
	}
	if p2.GetLSN() != 1234 {
		t.Errorf("expected LSN 1234, got %d", p2.GetLSN())
	}
}

func TestPager_AppendPage(t *testing.T) {
	f, err := os.CreateTemp("", "test_pager_append_*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	// Write to Page ID 2 (which should effectively make the file 3 pages long)
	p := NewPage(PageTypeInternal)
	p.SetPageID(2)

	err = WritePage(f, 2, p)
	if err != nil {
		t.Fatalf("failed to write page: %v", err)
	}

	stat, _ := f.Stat()
	expectedSize := int64(3 * PageSize)
	if stat.Size() != expectedSize {
		t.Errorf("expected file size %d, got %d", expectedSize, stat.Size())
	}
}
