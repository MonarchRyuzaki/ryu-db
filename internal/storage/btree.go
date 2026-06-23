package storage

/*
NOTE ON IMPLEMENTATION:
Although often referred to interchangeably, what is implemented here is functionally
closer to a B-Tree (storing data exclusively in leaf nodes) but it lacks the
next/sibling leaf pointers required by a true B+ Tree.

Because of this, this tree is highly optimized for POINT QUERIES (Insert, Find, Delete),
but is not currently equipped for efficient sequential RANGE QUERIES.

However, because the storage engine uses a swappable `Index` interface, a true
B+ Tree implementation (with sibling pointers in the page headers) can easily
be swapped in later if range queries become necessary!
*/

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Index is the interface that any tree or hash index must implement.
type Index interface {
	Insert(key []byte, value []byte, txMgr *TransactionManager) error
	Find(key []byte) ([]byte, error)
	FindLatest(mvccUserKey []byte, txMgr *TransactionManager) ([]byte, error)
	Delete(key []byte, txMgr *TransactionManager) error
}

// BTree implements the Index interface using a B+ Tree over the BufferManager.
type BTree struct {
	bm         *BufferManager
	wal        *WAL
	rootPageID uint32
}

// NewBTree creates a new B+ Tree instance. It initializes the underlying buffer manager
// and pager using the table name.
func NewBTree(tableName string, dir string) (*BTree, error) {
	if dir == "" {
		dir = "."
	}
	filename := tableName + ".db"
	walPath := filepath.Join(dir, tableName+".wal")

	wal, err := NewWAL(walPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize wal: %w", err)
	}

	bm, err := NewBufferManager(dir, filename, 100, wal)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize buffer manager: %w", err)
	}

	tree := &BTree{
		bm:         bm,
		wal:        wal,
		rootPageID: 0,
	}

	// Run ARIES Recovery before doing anything else
	err = tree.Recover()
	if err != nil {
		return nil, fmt.Errorf("failed during recovery: %w", err)
	}

	path := filepath.Join(dir, filename)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat db file: %w", err)
	}

	if info.Size() == 0 {
		// Allocate Page 0 (Meta Page)
		metaID, err := bm.pager.AllocatePage(PageTypeMeta)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate meta page: %w", err)
		}

		// Allocate Page 1 (Root Leaf Page)
		rootID, err := bm.pager.AllocatePage(PageTypeLeaf)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate root page: %w", err)
		}

		// Setup Meta Page
		metaPage, err := bm.FetchPageForWrite(metaID, PageTypeMeta)
		if err != nil {
			return nil, err
		}
		metaPage.SetRootPageID(rootID)
		metaPage.SetFirstFreePageID(0)

		if tree.wal != nil {
			lsn, _ := tree.wal.Append(0, metaID, 0, LogOpFullPage, nil, metaPage.GetData())
			metaPage.SetLSN(lsn)
		}

		bm.UnpinPage(metaID, true, true)

		tree.rootPageID = rootID
	} else {
		// Read Root Page ID from Meta Page (Page 0)
		metaPage, err := bm.FetchPageForRead(MetaPageID, PageTypeMeta)
		if err != nil {
			return nil, fmt.Errorf("failed to read meta page: %w", err)
		}
		tree.rootPageID = metaPage.GetRootPageID()
		bm.UnpinPage(MetaPageID, false, false)
	}

	return tree, nil
}

// Close gracefully shuts down the BTree by flushing all buffers and closing files.
func (tree *BTree) Close() error {
	if tree.wal != nil {
		tree.wal.Close()
	}
	return tree.bm.Close()
}

// Checkpoint performs a fuzzy checkpoint and writes the Checkpoint LSN to a .checkpoint file.
func (tree *BTree) Checkpoint() error {
	if tree.wal == nil {
		return nil
	}

	dpt := tree.bm.GetDirtyPageTable()
	lsn, err := tree.wal.WriteCheckpoint(dpt)
	if err != nil {
		return fmt.Errorf("failed to write checkpoint to WAL: %w", err)
	}

	// Write the Checkpoint LSN to a .checkpoint file atomically
	chkPath := filepath.Join(filepath.Dir(tree.bm.pager.file.Name()), "checkpoint.meta")
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, lsn)

	err = os.WriteFile(chkPath+".tmp", buf, 0644)
	if err != nil {
		return err
	}
	return os.Rename(chkPath+".tmp", chkPath)
}

// StartCheckpointRoutine launches a background goroutine that performs fuzzy checkpoints periodically.
func (tree *BTree) StartCheckpointRoutine(interval time.Duration) {
	go func() {
		for {
			time.Sleep(interval)
			fmt.Println("[Checkpoint] Starting background checkpoint...")
			err := tree.Checkpoint()
			if err != nil {
				fmt.Printf("[Checkpoint] Error during checkpoint: %v\n", err)
			} else {
				fmt.Println("[Checkpoint] Successfully wrote checkpoint.")
			}
		}
	}()
}

// allocatePage checks the Free Page List on the Meta Page. If a free page is available,
// it pops it and reuses it. Otherwise, it calls pager.AllocatePage to append to the file.
func (tree *BTree) allocatePage(pageType uint8) (uint32, error) {
	metaPage, err := tree.bm.FetchPageForWrite(MetaPageID, PageTypeMeta)
	if err != nil {
		return 0, err
	}
	defer tree.bm.UnpinPage(MetaPageID, true, true)

	firstFree := metaPage.GetFirstFreePageID()
	if firstFree != 0 {
		// Pop the free page
		freePage, err := tree.bm.FetchPageForRead(firstFree, PageTypeFree)
		if err != nil {
			return 0, err
		}
		nextFree := freePage.GetNextFreePageID()
		tree.bm.UnpinPage(firstFree, false, false)

		metaPage.SetFirstFreePageID(nextFree)

		// Re-initialize the reused page to its new type
		reusedPage, err := tree.bm.FetchPageForWrite(firstFree, pageType)
		if err != nil {
			return 0, err
		}
		reusedPage.Init(pageType)
		tree.bm.UnpinPage(firstFree, true, true)

		return firstFree, nil
	}

	// No free pages, append to file
	return tree.bm.pager.AllocatePage(pageType)
}

// deallocatePage marks a page as Free and pushes it onto the Free Page List stack.
func (tree *BTree) deallocatePage(pageID uint32) error {
	metaPage, err := tree.bm.FetchPageForWrite(MetaPageID, PageTypeMeta)
	if err != nil {
		return err
	}
	defer tree.bm.UnpinPage(MetaPageID, true, true)

	firstFree := metaPage.GetFirstFreePageID()

	// Initialize the dead page as a Free Page and link it
	deadPage, err := tree.bm.FetchPageForWrite(pageID, PageTypeFree)
	if err != nil {
		return err
	}
	deadPage.Init(PageTypeFree)
	deadPage.SetNextFreePageID(firstFree)
	tree.bm.UnpinPage(pageID, true, true)

	metaPage.SetFirstFreePageID(pageID)
	return nil
}

// findLeafPage traverses the tree to find the appropriate leaf page for a key.
// It returns the leaf page (PINNED!) and the path of parent page IDs we took to get there.
func (tree *BTree) findLeafPage(key []byte, forWrite bool) (*Page, []uint32, error) {
	var path []uint32
	currPageID := tree.rootPageID

	for {
		// Fetch the page (this pins it in memory)
		page, err := tree.bm.FetchPageForRead(currPageID, PageTypeLeaf)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to fetch page %d: %w", currPageID, err)
		}

		if page.GetPageType() == PageTypeLeaf {
			if forWrite {
				tree.bm.UnpinPage(currPageID, false, false) // Drop read lock
				page, err = tree.bm.FetchPageForWrite(currPageID, PageTypeLeaf)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to fetch page for write %d: %w", currPageID, err)
				}
			}
			return page, path, nil
		}

		// Internal Node
		slotCount := page.getSlotCount()
		var nextChildID uint32 = 0
		var maxMatchedKey []byte = nil

		for i := uint16(0); i < slotCount; i++ {
			cellData, _ := page.Get(i)
			kCell := DeserializeKeyCell(cellData)

			// We want the child pointer corresponding to the largest key that is <= our search key.
			if bytes.Compare(kCell.Key, key) <= 0 {
				if maxMatchedKey == nil || bytes.Compare(kCell.Key, maxMatchedKey) > 0 {
					maxMatchedKey = kCell.Key
					nextChildID = kCell.ChildPageID
				}
			}
		}

		// Safety fallback if no key is <= (for the very first leftmost child)
		if maxMatchedKey == nil && slotCount > 0 {
			cellData, _ := page.Get(0)
			nextChildID = DeserializeKeyCell(cellData).ChildPageID
		}

		// For Breadcrumbs
		path = append(path, currPageID)

		// Unpin the current internal node since we are done with it
		tree.bm.UnpinPage(currPageID, false, false)

		if nextChildID == 0 {
			return nil, nil, fmt.Errorf("database corrupted: child pointer is 0 (infinite loop prevented)")
		}

		currPageID = nextChildID
	}
}

// isVersionVisible checks if a specific MVCC version cell is visible to the reader.
func isVersionVisible(kvKey []byte, mvccUserKey []byte, txMgr *TransactionManager) bool {
	userKey, readerTxID := ExtractMVCCKey(mvccUserKey)
	kvKey, versionTxID := ExtractMVCCKey(kvKey)

	// Is it the exact same user key?
	if !bytes.Equal(kvKey, userKey) {
		return false
	}

	// Is the version older than or equal to our reader TxID?
	if versionTxID > readerTxID {
		return false // It's from the future
	}

	// Is the version committed, or does it belong to us?
	if versionTxID == readerTxID {
		return true // We can always see our own writes
	}

	if txMgr != nil {
		status := txMgr.GetStatus(versionTxID)
		if status != TXN_COMMITED {
			return false // It's from another active/aborted transaction
		}
	}

	return true
}

// FindLatest searches for the most recent version of a key that is visible to the reader.
func (tree *BTree) FindLatest(mvccUserKey []byte, txMgr *TransactionManager) ([]byte, error) {
	leaf, _, err := tree.findLeafPage(mvccUserKey, false)
	if err != nil {
		return nil, err
	}
	defer tree.bm.UnpinPage(leaf.GetPageID(), false, false)

	slotCount := leaf.getSlotCount()
	var bestKV *KVCell

	for i := uint16(0); i < slotCount; i++ {
		c, _ := leaf.Get(i)
		kv := DeserializeKVCell(c)

		if isVersionVisible(kv.Key, mvccUserKey, txMgr) {
			if bestKV == nil || bytes.Compare(kv.Key, bestKV.Key) > 0 {
				bestKV = kv
			}
		}
	}

	if bestKV == nil {
		return nil, fmt.Errorf("key not found")
	}

	if bestKV.Flag == KEY_DELETED_FLAG {
		return nil, fmt.Errorf("key not found (deleted)")
	}

	if bestKV.IsOverflow() {
		return tree.readOverflowChain(bestKV.Value)
	}

	return bestKV.Value, nil
}

// Find searches the B+ tree for the given key and returns the associated value.
func (tree *BTree) Find(key []byte) ([]byte, error) {
	leaf, _, err := tree.findLeafPage(key, false)
	if err != nil {
		return nil, err
	}
	defer tree.bm.UnpinPage(leaf.GetPageID(), false, false)

	// Linear search the leaf for the exact key
	// TODO: Binary Search Later
	slotCount := leaf.getSlotCount()
	for i := uint16(0); i < slotCount; i++ {
		cellData, _ := leaf.Get(i)
		kv := DeserializeKVCell(cellData)

		if bytes.Equal(kv.Key, key) {
			if kv.IsDeleted() {
				return nil, fmt.Errorf("key not found") // It was tombstoned!
			}
			if kv.IsOverflow() {
				return tree.readOverflowChain(kv.Value)
			}
			return kv.Value, nil // Found it!
		}
	}

	return nil, fmt.Errorf("key not found")
}

// Insert adds a new key-value pair to the B+ tree. If the key already exists, it updates it (Upsert).
func (tree *BTree) Insert(key []byte, value []byte, txMgr *TransactionManager) error {
	return tree.upsertCell(key, value, 0, txMgr)
}

// Delete removes a key from the B+ tree using a Tombstone approach.
func (tree *BTree) Delete(key []byte, txMgr *TransactionManager) error {
	// KEY_DELETED_FLAG indicates a Tombstone (deleted) cell
	return tree.upsertCell(key, nil, KEY_DELETED_FLAG, txMgr)
}

// upsertCell handles the actual insertion, updating, or tombstoning of a cell.
func (tree *BTree) upsertCell(key []byte, value []byte, flag uint8, txMgr *TransactionManager) error {
	leaf, path, err := tree.findLeafPage(key, true)
	if err != nil {
		return err
	}

	slotCount := leaf.getSlotCount()
	var cells [][]byte
	keyExists := false

	// If the value is huge, we move it to overflow pages right now!
	const MaxInlineValueSize = 1024
	if len(value) > MaxInlineValueSize {
		metadata, err := tree.createOverflowChain(value)
		if err != nil {
			return err
		}
		value = metadata
		flag |= KEY_OVERFLOW_FLAG
	}

	newCell := NewKVCell(flag, key, value)
	cellBytes := newCell.Serialize()

	// We start the space calculation with the header size
	totalSpaceNeeded := HeaderSize

	writerUserKey, writerTxID := ExtractMVCCKey(key)

	// Extract all cells. If we find the key, we swap its byte data with the new cell.
	for i := uint16(0); i < slotCount; i++ {
		c, _ := leaf.Get(i)
		kv := DeserializeKVCell(c)

		kvUserKey, kvTxID := ExtractMVCCKey(kv.Key)

		// MVCC Write-Write Conflict Detection (First-Updater-Wins)
		if bytes.Equal(kvUserKey, writerUserKey) && txMgr != nil && kvTxID != writerTxID {
			status := txMgr.GetStatus(kvTxID)
			
			// If another transaction is currently modifying this key, we abort (WW Conflict)
			if status == TXN_RUNNING {
				tree.bm.UnpinPage(leaf.GetPageID(), false, true)
				return fmt.Errorf("write-write conflict: key %s is currently locked by active transaction %d", string(writerUserKey), kvTxID)
			}
			
			// If a future transaction already committed a write to this key, we abort (WW Conflict)
			if status == TXN_COMMITED && kvTxID > writerTxID {
				tree.bm.UnpinPage(leaf.GetPageID(), false, true)
				return fmt.Errorf("write-write conflict: key %s was modified by a newer committed transaction %d", string(writerUserKey), kvTxID)
			}
		}

		var cellToKeep []byte
		if bytes.Equal(kv.Key, key) {
			keyExists = true
			cellToKeep = cellBytes // Swap! (Updates or Tombstones the cell)

			// If we are overwriting a cell that had an overflow chain, we must free the old chain!
			if kv.IsOverflow() {
				tree.freeOverflowChain(kv.Value)
			}
		} else {
			cellToKeep = make([]byte, len(c))
			copy(cellToKeep, c)
		}

		cells = append(cells, cellToKeep)
		totalSpaceNeeded += SlotSize + len(cellToKeep)
	}

	// If the key wasn't found...
	if !keyExists {
		// It's a brand new insert! (Even if it's a tombstone, we must insert it for MVCC)

		// It's a brand new insert!
		cells = append(cells, cellBytes)
		totalSpaceNeeded += SlotSize + len(cellBytes)
	}

	// Because we extracted all cells, we can definitively check if they fit in one page
	if totalSpaceNeeded <= PageSize {
		// Sort the cells by key to ensure the page remains strictly sorted!
		sort.Slice(cells, func(i, j int) bool {
			ki := DeserializeKVCell(cells[i]).Key
			kj := DeserializeKVCell(cells[j]).Key
			return bytes.Compare(ki, kj) < 0
		})

		leaf.Init(PageTypeLeaf) // Wipe the page clean
		for _, c := range cells {
			leaf.Insert(c) // Re-insert all cells
		}

		var lsn uint64
		if tree.wal != nil {
			op := LogOpInsert
			if flag == KEY_DELETED_FLAG {
				op = LogOpDelete
			}
			var prevLSN uint64 = 0
			if txMgr != nil {
				prevLSN = txMgr.GetLastLSN(writerTxID)
			}

			var errAppend error
			lsn, errAppend = tree.wal.Append(uint64(writerTxID), leaf.GetPageID(), prevLSN, op, key, value)
			if errAppend != nil {
				tree.bm.UnpinPage(leaf.GetPageID(), true, true)
				return errAppend
			}
			if txMgr != nil {
				txMgr.SetLastLSN(writerTxID, lsn)
			}
			leaf.SetLSN(lsn)
		}

		return tree.bm.UnpinPage(leaf.GetPageID(), true, true)
	}

	// It doesn't fit! The page is full, so we must split the leaf.
	return tree.splitLeafCells(leaf, path, cells)
}

// splitLeafCells handles the splitting of a leaf node using a pre-extracted slice of cells.
func (tree *BTree) splitLeafCells(leaf *Page, path []uint32, cells [][]byte) error {
	// 1. Sort the cells by key to ensure correct splitting
	sort.Slice(cells, func(i, j int) bool {
		ki := DeserializeKVCell(cells[i]).Key
		kj := DeserializeKVCell(cells[j]).Key
		return bytes.Compare(ki, kj) < 0
	})

	// 2. Clear the original leaf page
	leaf.Init(PageTypeLeaf)

	// 3. Allocate and fetch a new leaf page
	newLeafID, err := tree.allocatePage(PageTypeLeaf)
	if err != nil {
		return err
	}
	newLeaf, err := tree.bm.FetchPageForWrite(newLeafID, PageTypeLeaf)
	if err != nil {
		return err
	}

	// 4. Distribute cells evenly
	mid := len(cells) / 2
	for i := 0; i < mid; i++ {
		leaf.Insert(cells[i])
	}
	for i := mid; i < len(cells); i++ {
		newLeaf.Insert(cells[i])
	}

	// 5. Get the routing key to bubble up (the smallest key in the new right-side leaf)
	routingKey := DeserializeKVCell(cells[mid]).Key
	routingCell := NewKeyCell(newLeafID, routingKey).Serialize()

	// Log AFTER state of split pages
	if tree.wal != nil {
		lsn1, _ := tree.wal.Append(0, leaf.GetPageID(), 0, LogOpFullPage, nil, leaf.GetData())
		leaf.SetLSN(lsn1)
		lsn2, _ := tree.wal.Append(0, newLeaf.GetPageID(), 0, LogOpFullPage, nil, newLeaf.GetData())
		newLeaf.SetLSN(lsn2)
	}

	// 6. Unpin both leaves (they are dirty now)
	tree.bm.UnpinPage(leaf.GetPageID(), true, true)
	tree.bm.UnpinPage(newLeafID, true, true)

	// 7. Bubble up to parent
	return tree.insertIntoParent(leaf.GetPageID(), routingCell, path)
}

// insertIntoParent handles recursively bubbling up routing keys.
func (tree *BTree) insertIntoParent(leftChildID uint32, routingCell []byte, path []uint32) error {
	if len(path) == 0 {
		// We split the root! Create a brand new root internal node.
		newRootID, _ := tree.allocatePage(PageTypeInternal)
		newRoot, _ := tree.bm.FetchPageForWrite(newRootID, PageTypeInternal)

		// The new root needs a left-most child pointer (empty key, representing -infinity)
		leftPointer := NewKeyCell(leftChildID, nil).Serialize()
		newRoot.Insert(leftPointer)
		newRoot.Insert(routingCell)

		tree.rootPageID = newRootID

		if tree.wal != nil {
			lsn, _ := tree.wal.Append(0, newRootID, 0, LogOpFullPage, nil, newRoot.GetData())
			newRoot.SetLSN(lsn)
		}

		// Update Meta Page
		metaPage, _ := tree.bm.FetchPageForWrite(MetaPageID, PageTypeMeta)
		if metaPage != nil {
			metaPage.SetRootPageID(newRootID)

			if tree.wal != nil {
				lsn, _ := tree.wal.Append(0, MetaPageID, 0, LogOpFullPage, nil, metaPage.GetData())
				metaPage.SetLSN(lsn)
			}
			tree.bm.UnpinPage(MetaPageID, true, true)
		}

		return tree.bm.UnpinPage(newRootID, true, true)
	}

	// Pop the parent from the stack
	parentID := path[len(path)-1]
	path = path[:len(path)-1]

	parent, err := tree.bm.FetchPageForWrite(parentID, PageTypeInternal)
	if err != nil {
		return err
	}

	// Try to insert the routing key
	_, err = parent.Insert(routingCell)
	if err == nil {
		// It fits! Let's sort the internal node to keep the B-Tree fully sorted.
		slotCount := parent.getSlotCount()
		cells := make([][]byte, 0, slotCount)
		for i := uint16(0); i < slotCount; i++ {
			c, _ := parent.Get(i)
			cCopy := make([]byte, len(c))
			copy(cCopy, c)
			cells = append(cells, cCopy)
		}

		// Sort KeyCells. Remember, nil (empty) key is the smallest.
		sort.Slice(cells, func(i, j int) bool {
			ki := DeserializeKeyCell(cells[i]).Key
			kj := DeserializeKeyCell(cells[j]).Key
			if len(ki) == 0 {
				return true
			}
			if len(kj) == 0 {
				return false
			}
			return bytes.Compare(ki, kj) < 0
		})

		parent.Init(PageTypeInternal)
		for _, c := range cells {
			parent.Insert(c)
		}

		if tree.wal != nil {
			lsn, _ := tree.wal.Append(0, parentID, 0, LogOpFullPage, nil, parent.GetData())
			parent.SetLSN(lsn)
		}

		return tree.bm.UnpinPage(parentID, true, true)
	}

	// If the internal node is also full, we must split it!
	if err.Error() == "page full" {
		return tree.splitInternal(parent, path, routingCell)
	}

	tree.bm.UnpinPage(parentID, false, true)
	return err
}

// splitInternal handles the tricky splitting of internal nodes.
func (tree *BTree) splitInternal(internalNode *Page, path []uint32, newCell []byte) error {
	slotCount := internalNode.getSlotCount()
	cells := make([][]byte, 0, slotCount+1)
	for i := uint16(0); i < slotCount; i++ {
		c, _ := internalNode.Get(i)
		cCopy := make([]byte, len(c))
		copy(cCopy, c)
		cells = append(cells, cCopy)
	}
	cells = append(cells, newCell)

	// Sort KeyCells. Remember, nil (empty) key is the smallest.
	sort.Slice(cells, func(i, j int) bool {
		ki := DeserializeKeyCell(cells[i]).Key
		kj := DeserializeKeyCell(cells[j]).Key
		if len(ki) == 0 {
			return true
		}
		if len(kj) == 0 {
			return false
		}
		return bytes.Compare(ki, kj) < 0
	})

	internalNode.Init(PageTypeInternal)

	newInternalID, _ := tree.allocatePage(PageTypeInternal)
	newInternal, _ := tree.bm.FetchPageForWrite(newInternalID, PageTypeInternal)

	mid := len(cells) / 2

	// Left half goes to the original node
	for i := 0; i < mid; i++ {
		internalNode.Insert(cells[i])
	}

	// The middle cell's key gets pushed UP to the parent.
	// Its ChildPageID becomes the left-most (empty key) pointer of the new right-side node.
	midCell := DeserializeKeyCell(cells[mid])
	routingKey := midCell.Key

	firstRightCell := NewKeyCell(midCell.ChildPageID, nil).Serialize()
	newInternal.Insert(firstRightCell)

	// The rest of the right half goes to the new node normally
	for i := mid + 1; i < len(cells); i++ {
		newInternal.Insert(cells[i])
	}

	routingCell := NewKeyCell(newInternalID, routingKey).Serialize()

	if tree.wal != nil {
		lsn1, _ := tree.wal.Append(0, internalNode.GetPageID(), 0, LogOpFullPage, nil, internalNode.GetData())
		internalNode.SetLSN(lsn1)
		lsn2, _ := tree.wal.Append(0, newInternalID, 0, LogOpFullPage, nil, newInternal.GetData())
		newInternal.SetLSN(lsn2)
	}

	tree.bm.UnpinPage(internalNode.GetPageID(), true, true)
	tree.bm.UnpinPage(newInternalID, true, true)

	return tree.insertIntoParent(internalNode.GetPageID(), routingCell, path)
}

// createOverflowChain chunks a large value across multiple Overflow Pages.
// It returns the 8-byte metadata slice [FirstPageID(4), TotalLength(4)] to be stored in the KVCell.
func (tree *BTree) createOverflowChain(value []byte) ([]byte, error) {
	var firstPageID uint32
	var prevPage *Page

	dataRemaining := value

	for len(dataRemaining) > 0 {
		newPageID, err := tree.allocatePage(PageTypeOverflow)
		if err != nil {
			return nil, err
		}

		page, err := tree.bm.FetchPageForWrite(newPageID, PageTypeOverflow)
		if err != nil {
			return nil, err
		}

		if firstPageID == 0 {
			firstPageID = newPageID
		}

		// Link the previous page to this new page
		if prevPage != nil {
			prevPage.SetNextOverflowPageID(newPageID)
			tree.bm.UnpinPage(prevPage.GetPageID(), true, true) // Done with the prev page
		}

		chunkSize := len(dataRemaining)
		if chunkSize > MaxOverflowDataSize {
			chunkSize = MaxOverflowDataSize
		}

		page.SetNextOverflowPageID(0) // Default: no next page
		page.WriteOverflowData(dataRemaining[:chunkSize])
		dataRemaining = dataRemaining[chunkSize:]

		// If this is the last chunk, we are done with this page
		if len(dataRemaining) == 0 {
			tree.bm.UnpinPage(newPageID, true, true)
		} else {
			// Keep it pinned for the next iteration to link to
			prevPage = page
		}
	}

	metadata := make([]byte, 8)
	binary.LittleEndian.PutUint32(metadata[0:4], firstPageID)
	binary.LittleEndian.PutUint32(metadata[4:8], uint32(len(value)))
	return metadata, nil
}

// readOverflowChain fetches the chain of Overflow Pages and reassembles the large value.
func (tree *BTree) readOverflowChain(metadata []byte) ([]byte, error) {
	currPageID := binary.LittleEndian.Uint32(metadata[0:4])
	totalLength := binary.LittleEndian.Uint32(metadata[4:8])

	result := make([]byte, 0, totalLength)
	bytesRead := uint32(0)

	for currPageID != 0 {
		page, err := tree.bm.FetchPageForRead(currPageID, PageTypeOverflow)
		if err != nil {
			return nil, err
		}

		chunkSize := totalLength - bytesRead
		if chunkSize > MaxOverflowDataSize {
			chunkSize = MaxOverflowDataSize
		}

		chunk := page.ReadOverflowData(chunkSize)
		result = append(result, chunk...)
		bytesRead += chunkSize

		nextPageID := page.GetNextOverflowPageID()
		tree.bm.UnpinPage(currPageID, false, false)

		currPageID = nextPageID
	}

	return result, nil
}
