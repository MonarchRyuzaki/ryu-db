package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

// Recover executes the ARIES Recovery algorithm (Analysis and Redo phases).
func (tree *BTree) Recover() error {
	if tree.wal == nil {
		return nil // Nothing to recover from
	}

	// Disable WAL logging temporarily so we don't log our own recovery actions!
	wal := tree.wal
	tree.wal = nil
	tree.bm.wal = nil
	defer func() {
		tree.wal = wal
		tree.bm.wal = wal
	}()

	fmt.Println("[Recovery] Starting ARIES Recovery...")

	// Checkpoint Initialization
	chkPath := filepath.Join(filepath.Dir(tree.bm.pager.file.Name()), "checkpoint.meta")
	buf, err := os.ReadFile(chkPath)
	var checkpointLSN uint64 = 0
	if err == nil && len(buf) == 8 {
		checkpointLSN = binary.LittleEndian.Uint64(buf)
		fmt.Printf("[Recovery] Found checkpoint at LSN %d\n", checkpointLSN)
	} else {
		fmt.Println("[Recovery] No checkpoint found. Scanning from beginning of WAL.")
	}

	// Analysis Phase
	dpt := make(map[uint32]uint64) // Dirty Page Table
	att := make(map[uint64]uint64) // Active Transaction Table: TxnID -> LastLSN
	lsn := checkpointLSN
	if lsn == 0 {
		// Only skip 4 bytes if the file actually has the magic header
		buf := make([]byte, 4)
		if n, _ := wal.file.ReadAt(buf, 0); n == 4 && string(buf) == "WAL\n" {
			lsn = 4
		}
	}
	
	fmt.Println("[Recovery] Analysis Phase...")
	for {
		rec, size, err := wal.ReadRecord(lsn)
		if err != nil {
			break // Reached EOF
		}

		if rec.OpType == LogOpCheckpoint {
			// Deserialize DPT
			numEntries := binary.LittleEndian.Uint32(rec.Value[0:4])
			valOffset := 4
			for i := uint32(0); i < numEntries; i++ {
				pageID := binary.LittleEndian.Uint32(rec.Value[valOffset : valOffset+4])
				recLSN := binary.LittleEndian.Uint64(rec.Value[valOffset+4 : valOffset+12])
				dpt[pageID] = recLSN
				valOffset += 12
			}
		} else if rec.OpType == LogOpFullPage || rec.OpType == LogOpInsert || rec.OpType == LogOpDelete || rec.OpType == LogOpCLR {
			// If a page was dirtied after the checkpoint, add it to DPT
			if _, exists := dpt[rec.PageID]; !exists {
				dpt[rec.PageID] = rec.LSN
			}
		}

		// Update ATT
		if rec.TxnID != 0 {
			if rec.OpType == LogOpCommit || rec.OpType == LogOpAbort {
				delete(att, rec.TxnID)
			} else {
				att[rec.TxnID] = rec.LSN
			}
		}

		lsn += uint64(size)
	}

	// Calculate MinLSN
	minLSN := lsn // Default to EOF
	for _, recLSN := range dpt {
		if recLSN < minLSN {
			minLSN = recLSN
		}
	}
	if len(dpt) == 0 {
		minLSN = checkpointLSN
	}

	fmt.Printf("[Recovery] Analysis Complete. MinLSN determined as %d\n", minLSN)

	// Redo Phase (Repeating History)
	lsn = minLSN
	if lsn == 0 {
		buf := make([]byte, 4)
		if n, _ := wal.file.ReadAt(buf, 0); n == 4 && string(buf) == "WAL\n" {
			lsn = 4
		}
	}
	fmt.Println("[Recovery] Redo Phase...")
	
	redoCount := 0
	for {
		rec, size, err := wal.ReadRecord(lsn)
		if err != nil {
			break // EOF
		}

		// Skip Rule 1: Page completely missing from DPT
		recLSN, exists := dpt[rec.PageID]
		if !exists {
			lsn += uint64(size)
			continue
		}

		// Skip Rule 2: Log Record is older than the oldest unflushed change
		if rec.LSN < recLSN {
			lsn += uint64(size)
			continue
		}

		// Skip Rule 3: Read the physical page from disk to check if it's already applied
		page, err := tree.bm.FetchPageForWrite(rec.PageID, PageTypeLeaf) // Type doesn't strictly matter here for physical redo
		if err != nil {
			return fmt.Errorf("failed to fetch page %d during redo: %w", rec.PageID, err)
		}

		if page.GetLSN() >= rec.LSN && rec.LSN != 0 {
			// Safely flushed!
			tree.bm.UnpinPage(rec.PageID, false, true)
			lsn += uint64(size)
			continue
		}

		// The change didn't make it to disk. We MUST REDO IT!
		redoCount++
		if rec.OpType == LogOpFullPage {
			copy(page.GetData(), rec.Value)
		} else if rec.OpType == LogOpInsert || rec.OpType == LogOpDelete {
			// Physiological REDO: Update the exact cell in the page
			tree.redoUpsertOnPage(page, rec.Key, rec.Value, rec.OpType)
		}
		
		page.SetLSN(rec.LSN)
		tree.bm.UnpinPage(rec.PageID, true, true)

		lsn += uint64(size)
	}

	fmt.Printf("[Recovery] Redo Phase Complete. Redid %d operations.\n", redoCount)

	// 4. Undo Phase
	fmt.Printf("[Recovery] Undo Phase... %d active transactions\n", len(att))
	undoCount := 0
	
	// We need to re-enable the WAL temporarily for Undo to write CLR and Abort records!
	tree.wal = wal
	tree.bm.wal = wal
	
	for len(att) > 0 {
		var maxLSN uint64 = 0
		var maxTxn uint64 = 0
		for txn, lsn := range att {
			if lsn > maxLSN {
				maxLSN = lsn
				maxTxn = txn
			}
		}
		
		if maxLSN == 0 {
			break
		}
		
		rec, _, err := wal.ReadRecord(maxLSN)
		if err != nil {
			break
		}
		
		if rec.OpType == LogOpInsert || rec.OpType == LogOpDelete {
			page, err := tree.bm.FetchPageForWrite(rec.PageID, PageTypeLeaf)
			if err == nil {
				inverseOp := LogOpDelete
				if rec.OpType == LogOpDelete {
					inverseOp = LogOpInsert
				}
				tree.redoUpsertOnPage(page, rec.Key, rec.Value, inverseOp)
				
				clrLSN, _ := tree.wal.Append(maxTxn, rec.PageID, rec.PrevLSN, LogOpCLR, rec.Key, rec.Value)
				page.SetLSN(clrLSN)
				tree.bm.UnpinPage(rec.PageID, true, true)
				undoCount++
			}
		}
		
		if rec.PrevLSN == 0 {
			tree.wal.Append(maxTxn, 0, 0, LogOpAbort, nil, nil)
			delete(att, maxTxn)
		} else {
			att[maxTxn] = rec.PrevLSN
		}
	}

	fmt.Printf("[Recovery] Undo Phase Complete. Undid %d operations.\n", undoCount)
	return nil
}

// redoUpsertOnPage handles physiological redo of an Insert or Delete directly on a physical page.
func (tree *BTree) redoUpsertOnPage(page *Page, key, value []byte, opType uint8) {
	slotCount := page.getSlotCount()
	var cells [][]byte
	keyExists := false

	flag := uint8(0)
	if opType == LogOpDelete {
		flag = KEY_DELETED_FLAG
	}
	
	// Value overflow should technically be logged physically as well, but for simplicity
	// we assume Redo applies the exact bytes. In a real system, the value bytes in the log 
	// for an overflow would be the overflow metadata pointer.
	newCell := NewKVCell(flag, key, value)
	cellBytes := newCell.Serialize()

	for i := uint16(0); i < slotCount; i++ {
		c, _ := page.Get(i)
		kv := DeserializeKVCell(c)

		var cellToKeep []byte
		if bytes.Equal(kv.Key, key) {
			keyExists = true
			cellToKeep = cellBytes
		} else {
			cellToKeep = make([]byte, len(c))
			copy(cellToKeep, c)
		}
		cells = append(cells, cellToKeep)
	}

	if !keyExists {
		cells = append(cells, cellBytes)
	}

	// Wipe the page and re-insert
	// Note: since this is physiological redo, we assume the page doesn't split during this operation
	// because ARIES logs structural modifications (splits) independently.
	pageType := page.GetPageType()
	page.Init(pageType)
	for _, c := range cells {
		page.Insert(c)
	}
}
