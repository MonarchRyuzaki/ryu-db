# Mutation and Vacuum Strategy

## 1. Mutations (Insert, Update, Delete)
Our B-Tree implements mutations with specific trade-offs designed to prioritize simplicity and avoid complex intra-page fragmentation tracking.

### Insert (Normal)
When a completely new key is inserted, it is formatted as a `KVCell` and appended to the slotted page.

### Update (In-Place Rewrite / Compaction)
When an existing key is updated, we do not attempt to overwrite the old bytes "in place" within the page's free space (which would leave fragmented holes if the new value is a different size). 
Instead, we extract all existing cells into memory, swap the updated cell in, completely wipe the page (`page.Init()`), and re-insert everything.

**Trade-off:** This costs slight CPU overhead for mem-copying, but completely eliminates the need for intra-page "free block lists." The page remains perfectly compacted at all times.
*(Note: In the future, we may experiment with changing updates to a "Delete Old + Insert New" Tombstone approach to observe MVCC-like behavior, but for learning purposes, having this compaction-rewrite mechanism gives us a strong foundation).*

### Delete (Tombstones)
When a key is deleted, we use the Tombstone approach. We perform an Update that overwrites the existing cell with a new cell where `Flag = KEY_DELETED_FLAG`. `Find()` recognizes this flag and pretends the key does not exist.

---

## 2. The Vacuum Process
Because Deletions leave Tombstones sitting physically inside the page, the database will eventually bloat. The Vacuum Process is a background task that reclaims this dead space.

### Cleaning Up Cells
The Vacuum process sequentially scans the B-Tree leaf pages. It reads the cells, and if it finds any where `IsDeleted() == true`, it permanently drops them during the page-rewrite phase.

### Freeing Overflow Pages
Crucially, if a dropped cell also has the `KEY_OVERFLOW_FLAG`, the Vacuum process traverses the linked list of Overflow Pages associated with that cell and marks every single one of those `PageID`s as free.

---

## 3. The Free Page List
When the Vacuum process frees an Overflow Page (or if a leaf node becomes completely empty), we do not shrink the physical file. Instead, we link those pages into a global "Free Page List".

### Allocation
The Pager's `AllocatePage()` function is modified to check the Free Page List first. If a free page is available, it pops it off the list and reuses it. Only if the list is empty does it append a new 4KB block to the end of the file.

### The Metadata Page
To persistently manage this Free Page List, we will introduce a "Meta-Page" (typically `PageID 0`), which persistently stores global database pointers, including the `FirstFreePageID`.
