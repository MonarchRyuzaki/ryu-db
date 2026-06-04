package storage

import (
	"bytes"
	"testing"
)

func TestBufferManager_EvictionAndPinning(t *testing.T) {
	dir := t.TempDir()

	// 1. Create a Buffer Manager with max 3 pages in memory
	bm, err := NewBufferManager(dir, "test_bm.db", 3)
	if err != nil {
		t.Fatalf("Failed to create Buffer Manager: %v", err)
	}
	defer bm.pager.Close()

	// 2. Pre-allocate 4 pages on disk (IDs 0, 1, 2, 3)
	for i := 0; i < 4; i++ {
		_, err := bm.pager.AllocatePage(PageTypeLeaf)
		if err != nil {
			t.Fatalf("Failed to allocate page: %v", err)
		}
	}

	// 3. Fetch 3 pages to perfectly fill the buffer pool (IDs 0, 1, 2)
	for i := uint32(0); i < 3; i++ {
		p, err := bm.FetchPage(i, PageTypeLeaf)
		if err != nil {
			t.Fatalf("Failed to fetch page %d: %v", i, err)
		}

		// Insert dummy data so we can verify it was safely written to disk if evicted
		c := NewKVCell(0, []byte("key"), []byte{byte(i)})
		p.Insert(c.Serialize())

		// Unpin it and mark it dirty!
		err = bm.UnpinPage(i, true)
		if err != nil {
			t.Fatalf("Failed to unpin page %d: %v", i, err)
		}
	}

	// Verify the cache is full
	if len(bm.pageTable) != 3 {
		t.Fatalf("Expected exactly 3 pages in memory, got %d", len(bm.pageTable))
	}

	// 4. Fetch the 4th page (PageID 3). This MUST trigger an eviction!
	// Because the map iteration is random, one of 0, 1, or 2 will be kicked out.
	_, err = bm.FetchPage(3, PageTypeLeaf)
	if err != nil {
		t.Fatalf("Failed to fetch page 3 (eviction failed): %v", err)
	}
	
	// Unpin page 3 (clean)
	bm.UnpinPage(3, false)

	if len(bm.pageTable) != 3 {
		t.Fatalf("Expected memory to remain at 3 pages after eviction, got %d", len(bm.pageTable))
	}

	// 5. Fetch all original 3 pages back. 
	// The evicted page will be a cache miss and get read from disk (where its dirty data was safely flushed).
	// The other two will be instant cache hits.
	for i := uint32(0); i < 3; i++ {
		p, err := bm.FetchPage(i, PageTypeLeaf)
		if err != nil {
			t.Fatalf("Failed to fetch page %d back: %v", i, err)
		}
		
		// Verify the data we inserted is still there
		res, err := p.Get(0)
		if err != nil {
			t.Fatalf("Lost data on page %d: %v", i, err)
		}
		
		cell := DeserializeKVCell(res)
		if !bytes.Equal(cell.Key, []byte("key")) || cell.Value[0] != byte(i) {
			t.Fatalf("Data corruption on page %d", i)
		}
		
		// Leave them pinned for the next test step!
	}

	// 6. Test Error: Buffer Pool Full
	// Right now, pages 0, 1, and 2 are PINNED. 
	// The pool size is exactly 3. 
	// If we try to fetch Page 3, it should fail because nothing can be evicted.
	_, err = bm.FetchPage(3, PageTypeLeaf)
	if err == nil {
		t.Fatalf("Expected an error when fetching a page with a fully pinned buffer pool!")
	}
	if err.Error() != "buffer pool is full and all pages are pinned" {
		t.Fatalf("Expected 'full pool' error, got: %v", err)
	}

	// Clean up by unpinning
	bm.UnpinPage(0, false)
	bm.UnpinPage(1, false)
	bm.UnpinPage(2, false)
}
