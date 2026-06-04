package storage

import (
	"fmt"
	"sync"
)

// Frame represents a single slot in memory that holds a cached page.
type Frame struct {
	Page          *Page  // Pointer to the actual 4KB page data
	IsDirty       bool   // True if the page has been modified since being read from disk
	PinCount      uint32 // Number of active threads using this page (uint32 is safer than uint8 for concurrency)
	ReferenceBits uint16 // Used for page replacement policy
}

// BufferManager handles the caching of disk pages in memory.
type BufferManager struct {
	pager     *Pager
	pageTable map[uint32]*Frame // Maps PageID -> Frame in memory

	// MaxSize limits how many frames we can hold in memory at once.
	// When len(pageTable) == maxSize, we must evict an unpinned frame.
	maxSize int

	mu sync.Mutex
}

const (
	evictionPolicy = "first-find"
)

// NewBufferManager initializes a new BufferManager with a specific memory limit.
func NewBufferManager(dir, filename string, maxSize int) (*BufferManager, error) {
	pager, err := NewPager(dir, filename)
	if err != nil {
		return nil, err
	}
	return &BufferManager{
		pager:     pager,
		pageTable: make(map[uint32]*Frame),
		maxSize:   maxSize,
	}, nil
}

// FetchPage retrieves a page from the buffer pool. If it's not in memory, it reads it from disk.
func (bm *BufferManager) FetchPage(pageID uint32, fallbackPageMode uint8) (*Page, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if frame, exists := bm.pageTable[pageID]; exists {
		frame.PinCount++
		// (Later: update ReferenceBits here for eviction policy)
		return frame.Page, nil
	}

	if len(bm.pageTable) >= bm.maxSize {
		if err := bm.evict(); err != nil {
			return nil, err
		}
	}

	p := NewPage(fallbackPageMode)
	if err := bm.pager.ReadPage(pageID, p); err != nil {
		return nil, err
	}

	frame := &Frame{
		Page:     p,
		IsDirty:  false,
		PinCount: 1,
	}
	bm.pageTable[pageID] = frame

	return p, nil
}

// UnpinPage tells the buffer manager that a thread is done using this page.
func (bm *BufferManager) UnpinPage(pageID uint32, isDirty bool) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	frame, exists := bm.pageTable[pageID]
	if !exists {
		return fmt.Errorf("page %d is not in buffer pool", pageID)
	}

	if frame.PinCount == 0 {
		return fmt.Errorf("page %d pin count is already 0", pageID)
	}

	frame.PinCount--

	if isDirty {
		frame.IsDirty = true
	}

	return nil
}

// evict selects a page to remove from the buffer pool to make space.
func (bm *BufferManager) evict() error {
	var victimID uint32
	var victimFrame *Frame
	found := false

	switch evictionPolicy {
	case "first-find":
		victimID, victimFrame, found = bm.firstFind()
	}

	if !found {
		return fmt.Errorf("buffer pool is full and all pages are pinned")
	}

	if victimFrame.IsDirty {
		if err := bm.pager.WritePage(victimID, victimFrame.Page); err != nil {
			return err
		}
	}

	delete(bm.pageTable, victimID)
	return nil
}

func (bm *BufferManager) firstFind() (uint32, *Frame, bool) {
	for id, frame := range bm.pageTable {
		if frame.PinCount == 0 {
			return id, frame, true
		}
	}
	return 0, nil, false
}
