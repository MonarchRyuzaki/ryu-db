package storage

import (
	"fmt"
	"sync"
)

// Frame represents a page in the buffer pool.
type Frame struct {
	Page          *Page        // Pointer to the actual 4KB page data
	IsDirty       bool         // True if the page has been modified since being read from disk
	PinCount      uint32       // Number of active threads using this page
	ReferenceBits uint16       // Used for page replacement policy
	Latch         sync.RWMutex // Page-level lock
	RecLSN        uint64       // The LSN of the first log record that dirtied this page
}

// BufferManager handles the caching of disk pages in memory.
type BufferManager struct {
	pager     *Pager
	pageTable map[uint32]*Frame // Maps PageID -> Frame in memory

	// MaxSize limits how many frames we can hold in memory at once.
	// When len(pageTable) == maxSize, we must evict an unpinned frame.
	maxSize int

	wal *WAL // Write-Ahead Log reference

	mu sync.Mutex
}

const (
	evictionPolicy = "first-find"
)

// NewBufferManager initializes a new BufferManager with a specific memory limit.
func NewBufferManager(dir, filename string, maxSize int, wal *WAL) (*BufferManager, error) {
	pager, err := NewPager(dir, filename)
	if err != nil {
		return nil, err
	}
	return &BufferManager{
		pager:     pager,
		pageTable: make(map[uint32]*Frame),
		maxSize:   maxSize,
		wal:       wal,
	}, nil
}

// FetchPageForRead retrieves a page and acquires a read (shared) latch.
func (bm *BufferManager) FetchPageForRead(pageID uint32, fallbackPageMode uint8) (*Page, error) {
	frame, err := bm.getFrame(pageID, fallbackPageMode)
	if err != nil {
		return nil, err
	}
	frame.Latch.RLock()
	return frame.Page, nil
}

// FetchPageForWrite retrieves a page and acquires a write (exclusive) latch.
func (bm *BufferManager) FetchPageForWrite(pageID uint32, fallbackPageMode uint8) (*Page, error) {
	frame, err := bm.getFrame(pageID, fallbackPageMode)
	if err != nil {
		return nil, err
	}
	frame.Latch.Lock()

	// Full Page Write (Backup for Torn Pages)
	// We do this immediately when a clean page is fetched for writing.
	if !frame.IsDirty && bm.wal != nil {
		lsn, err := bm.wal.Append(0, pageID, 0, LogOpFullPage, nil, frame.Page.GetData())
		if err != nil {
			frame.Latch.Unlock()
			return nil, err
		}
		frame.Page.SetLSN(lsn)
		frame.RecLSN = lsn
	}

	return frame.Page, nil
}

// getFrame handles the buffer pool logic (pinning, evicting, reading from disk).
func (bm *BufferManager) getFrame(pageID uint32, fallbackPageMode uint8) (*Frame, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if frame, exists := bm.pageTable[pageID]; exists {
		frame.PinCount++
		// (Later: update ReferenceBits here for eviction policy)
		return frame, nil
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

	return frame, nil
}

// UnpinPage tells the buffer manager that a thread is done using this page.
func (bm *BufferManager) UnpinPage(pageID uint32, isDirty bool, isWrite bool) error {
	bm.mu.Lock()

	frame, exists := bm.pageTable[pageID]
	if !exists {
		bm.mu.Unlock()
		return fmt.Errorf("page %d is not in buffer pool", pageID)
	}

	if frame.PinCount == 0 {
		bm.mu.Unlock()
		return fmt.Errorf("page %d pin count is already 0", pageID)
	}

	frame.PinCount--

	if isDirty {
		frame.IsDirty = true
	}
	
	bm.mu.Unlock()

	if isWrite {
		frame.Latch.Unlock()
	} else {
		frame.Latch.RUnlock()
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
		victimFrame.IsDirty = false
		victimFrame.RecLSN = 0
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

// FlushAll writes all dirty pages to disk.
func (bm *BufferManager) FlushAll() error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	for id, frame := range bm.pageTable {
		if frame.IsDirty {
			if err := bm.pager.WritePage(id, frame.Page); err != nil {
				return err
			}
			frame.IsDirty = false
			frame.RecLSN = 0
		}
	}
	return nil
}

// Close flushes all pages and closes the pager.
func (bm *BufferManager) Close() error {
	if err := bm.FlushAll(); err != nil {
		return err
	}
	return bm.pager.Close()
}

// GetDirtyPageTable returns a snapshot of all currently dirty pages and their RecLSN.
func (bm *BufferManager) GetDirtyPageTable() map[uint32]uint64 {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	dpt := make(map[uint32]uint64)
	for id, frame := range bm.pageTable {
		if frame.IsDirty {
			dpt[id] = frame.RecLSN
		}
	}
	return dpt
}
