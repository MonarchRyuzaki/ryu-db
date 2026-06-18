package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"
)

// TODO: Since we are doing MVCC, we must delete all older versions of a node when a newer version has a DELETED FLAG

// StartVacuumRoutine launches a background goroutine that runs the vacuum process periodically.
func (tree *BTree) StartVacuumRoutine(interval time.Duration) {
	go func() {
		for {
			time.Sleep(interval)
			fmt.Println("[Vacuum] Starting background cleanup...")
			err := tree.Vacuum()
			if err != nil {
				fmt.Printf("[Vacuum] Error during cleanup: %v\n", err)
			} else {
				fmt.Println("[Vacuum] Cleanup finished successfully.")
			}
		}
	}()
}

// Vacuum triggers a full database sweep to drop tombstones and free associated overflow pages.
func (tree *BTree) Vacuum() error {
	// We perform a DFS starting to visit every single leaf node.
	return tree.vacuumNode(tree.rootPageID)
}

func (tree *BTree) vacuumNode(pageID uint32) error {
	// Fetch for READ first since we don't know if it's a leaf yet
	page, err := tree.bm.FetchPageForRead(pageID, PageTypeInternal)
	if err != nil {
		return err
	}

	// Leaf Node! Drop the read latch and fetch it for WRITE so we can clean it.
	if page.GetPageType() == PageTypeLeaf {
		tree.bm.UnpinPage(pageID, false, false)
		return tree.vacuumLeaf(pageID)
	}

	// Internal node. Collect all child pointers.
	slotCount := page.getSlotCount()
	var children []uint32
	for i := uint16(0); i < slotCount; i++ {
		cellData, _ := page.Get(i)
		kCell := DeserializeKeyCell(cellData)
		children = append(children, kCell.ChildPageID)
	}

	tree.bm.UnpinPage(pageID, false, false)

	for _, childID := range children {
		if childID != 0 {
			tree.vacuumNode(childID)
		}
	}

	return nil
}

func (tree *BTree) vacuumLeaf(pageID uint32) error {
	leaf, err := tree.bm.FetchPageForWrite(pageID, PageTypeLeaf)
	if err != nil {
		return err
	}

	slotCount := leaf.getSlotCount()
	var cellsToKeep [][]byte
	cleanedCount := 0

	// We need to find the latest version of each userKey.
	// Since keys are strictly sorted by [UserKey]\x00[TxID], all versions of a
	// userKey are contiguous, and the latest version is the last one in the sequence.
	for i := uint16(0); i < slotCount; i++ {
		c, _ := leaf.Get(i)
		kv := DeserializeKVCell(c)
		userKey := kv.Key[:len(kv.Key)-9]

		isOldVersion := false
		if i < slotCount-1 {
			nextC, _ := leaf.Get(i + 1)
			nextKV := DeserializeKVCell(nextC)
			nextUserKey := nextKV.Key[:len(nextKV.Key)-9]
			if bytes.Equal(userKey, nextUserKey) {
				isOldVersion = true
			}
		}

		if isOldVersion {
			cleanedCount++
			if kv.IsOverflow() {
				tree.freeOverflowChain(kv.Value)
			}
			continue
		}

		// This is the latest version of this userKey
		if kv.IsDeleted() {
			cleanedCount++
			if kv.IsOverflow() {
				tree.freeOverflowChain(kv.Value)
			}
		} else {
			cCopy := make([]byte, len(c))
			copy(cCopy, c)
			cellsToKeep = append(cellsToKeep, cCopy)
		}
	}

	if cleanedCount > 0 {
		leaf.Init(PageTypeLeaf)
		for _, c := range cellsToKeep {
			leaf.Insert(c)
		}
		// Unpin and mark as dirty (isWrite=true)
		return tree.bm.UnpinPage(pageID, true, true)
	}

	return tree.bm.UnpinPage(pageID, false, true)
}

func (tree *BTree) freeOverflowChain(metadata []byte) {
	currPageID := binary.LittleEndian.Uint32(metadata[0:4])

	for currPageID != 0 {
		page, err := tree.bm.FetchPageForRead(currPageID, PageTypeOverflow)
		if err != nil {
			break
		}
		nextPageID := page.GetNextOverflowPageID()
		tree.bm.UnpinPage(currPageID, false, false)

		// Add `currPageID` to the Database Free Page List!
		err = tree.deallocatePage(currPageID)
		if err != nil {
			fmt.Printf("[Vacuum] Failed to free page %d: %v\n", currPageID, err)
		} else {
			fmt.Printf("[Vacuum] Orphan Overflow Page %d successfully added to Free List.\n", currPageID)
		}

		currPageID = nextPageID
	}
}
