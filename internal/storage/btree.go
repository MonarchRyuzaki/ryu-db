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
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Index is the interface that any tree or hash index must implement.
type Index interface {
	Insert(key []byte, value []byte) error
	Find(key []byte) ([]byte, error)
	Delete(key []byte) error
}

// BTree implements the Index interface using a B+ Tree over the BufferManager.
type BTree struct {
	bm         *BufferManager
	rootPageID uint32
}

// NewBTree creates a new B+ Tree instance. It initializes the underlying buffer manager
// and pager using the table name.
func NewBTree(tableName string, dir string) (*BTree, error) {
	if dir == "" {
		dir = "." 
	}
	filename := tableName + ".db"

	bm, err := NewBufferManager(dir, filename, 100)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize buffer manager: %w", err)
	}

	tree := &BTree{
		bm:         bm,
		rootPageID: 0, 
	}

	path := filepath.Join(dir, filename)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat db file: %w", err)
	}

	if info.Size() == 0 {
		rootID, err := bm.pager.AllocatePage(PageTypeLeaf)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate root page: %w", err)
		}
		tree.rootPageID = rootID
	}

	return tree, nil
}

// findLeafPage traverses the tree to find the appropriate leaf page for a key.
// It returns the leaf page (PINNED!) and the path of parent page IDs we took to get there.
func (tree *BTree) findLeafPage(key []byte) (*Page, []uint32, error) {
	var path []uint32
	currPageID := tree.rootPageID

	for {
		// Fetch the page (this pins it in memory)
		page, err := tree.bm.FetchPage(currPageID, PageTypeLeaf)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to fetch page %d: %w", currPageID, err)
		}

		if page.GetPageType() == PageTypeLeaf {
			// Leaf Node
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
		tree.bm.UnpinPage(currPageID, false)

		currPageID = nextChildID
	}
}

// Find searches the B+ tree for the given key and returns the associated value.
func (tree *BTree) Find(key []byte) ([]byte, error) {
	leaf, _, err := tree.findLeafPage(key)
	if err != nil {
		return nil, err
	}
	defer tree.bm.UnpinPage(leaf.GetPageID(), false)

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
			return kv.Value, nil // Found it!
		}
	}

	return nil, fmt.Errorf("key not found")
}

// Insert adds a new key-value pair to the B+ tree. If the key already exists, it updates it (Upsert).
func (tree *BTree) Insert(key []byte, value []byte) error {
	return tree.upsertCell(key, value, 0)
}

// Delete removes a key from the B+ tree using a Tombstone approach.
func (tree *BTree) Delete(key []byte) error {
	// KEY_DELETED_FLAG indicates a Tombstone (deleted) cell
	return tree.upsertCell(key, nil, KEY_DELETED_FLAG)
}

// upsertCell handles the actual insertion, updating, or tombstoning of a cell.
func (tree *BTree) upsertCell(key []byte, value []byte, flag uint8) error {
	leaf, path, err := tree.findLeafPage(key)
	if err != nil {
		return err
	}

	slotCount := leaf.getSlotCount()
	var cells [][]byte
	keyExists := false

	newCell := NewKVCell(flag, key, value)
	cellBytes := newCell.Serialize()
	
	// We start the space calculation with the header size
	totalSpaceNeeded := HeaderSize

	// Extract all cells. If we find the key, we swap its byte data with the new cell.
	for i := uint16(0); i < slotCount; i++ {
		c, _ := leaf.Get(i)
		kv := DeserializeKVCell(c)

		var cellToKeep []byte
		if bytes.Equal(kv.Key, key) {
			keyExists = true
			cellToKeep = cellBytes // Swap! (Updates or Tombstones the cell)
		} else {
			cellToKeep = make([]byte, len(c))
			copy(cellToKeep, c)
		}

		cells = append(cells, cellToKeep)
		totalSpaceNeeded += SlotSize + len(cellToKeep)
	}

	// If the key wasn't found...
	if !keyExists {
		if flag == KEY_DELETED_FLAG {
			// We are trying to delete a key that doesn't exist.
			// Don't bloat the DB with an unnecessary tombstone.
			tree.bm.UnpinPage(leaf.GetPageID(), false)
			return fmt.Errorf("cannot delete: key not found")
		}

		// It's a brand new insert!
		cells = append(cells, cellBytes)
		totalSpaceNeeded += SlotSize + len(cellBytes)
	}

	// Because we extracted all cells, we can definitively check if they fit in one page
	if totalSpaceNeeded <= PageSize {
		leaf.Init(PageTypeLeaf) // Wipe the page clean
		for _, c := range cells {
			leaf.Insert(c) // Re-insert all cells
		}
		return tree.bm.UnpinPage(leaf.GetPageID(), true)
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
	newLeafID, err := tree.bm.pager.AllocatePage(PageTypeLeaf)
	if err != nil {
		return err
	}
	newLeaf, err := tree.bm.FetchPage(newLeafID, PageTypeLeaf)
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

	// 6. Unpin both leaves (they are dirty now)
	tree.bm.UnpinPage(leaf.GetPageID(), true)
	tree.bm.UnpinPage(newLeafID, true)

	// 7. Bubble up to parent
	return tree.insertIntoParent(leaf.GetPageID(), routingCell, path)
}

// insertIntoParent handles recursively bubbling up routing keys.
func (tree *BTree) insertIntoParent(leftChildID uint32, routingCell []byte, path []uint32) error {
	if len(path) == 0 {
		// We split the root! Create a brand new root internal node.
		newRootID, _ := tree.bm.pager.AllocatePage(PageTypeInternal)
		newRoot, _ := tree.bm.FetchPage(newRootID, PageTypeInternal)

		// The new root needs a left-most child pointer (empty key, representing -infinity)
		leftPointer := NewKeyCell(leftChildID, nil).Serialize()
		newRoot.Insert(leftPointer)
		newRoot.Insert(routingCell)

		tree.rootPageID = newRootID
		return tree.bm.UnpinPage(newRootID, true)
	}

	// Pop the parent from the stack
	parentID := path[len(path)-1]
	path = path[:len(path)-1]

	parent, err := tree.bm.FetchPage(parentID, PageTypeInternal)
	if err != nil {
		return err
	}

	// Try to insert the routing key
	_, err = parent.Insert(routingCell)
	if err == nil {
		return tree.bm.UnpinPage(parentID, true)
	}

	// If the internal node is also full, we must split it!
	if err.Error() == "page full" {
		return tree.splitInternal(parent, path, routingCell)
	}

	tree.bm.UnpinPage(parentID, false)
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

	newInternalID, _ := tree.bm.pager.AllocatePage(PageTypeInternal)
	newInternal, _ := tree.bm.FetchPage(newInternalID, PageTypeInternal)

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

	tree.bm.UnpinPage(internalNode.GetPageID(), true)
	tree.bm.UnpinPage(newInternalID, true)

	return tree.insertIntoParent(internalNode.GetPageID(), routingCell, path)
}


