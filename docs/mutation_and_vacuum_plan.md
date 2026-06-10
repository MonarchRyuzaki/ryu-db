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

---

## 4. Architectural Note: "Ghost" Routing Keys
A common question arises when discussing the Vacuum process: *If we delete all the cells in a Leaf Node so that it becomes completely empty, don't we need to go up to the Parent Node and delete the routing key that points to it?*

The answer is **No**, and understanding why is key to B+ Tree architecture.

In a B+ Tree, data only lives in the leaves. The routing keys in the internal nodes are strictly **separators** (guideposts), not data records. 
If a leaf splits and pushes the key `50` up to the parent, the parent knows that everything in the left child is `< 50` and everything in the right child is `>= 50`. 
If the user later deletes the record `50` from the database, the right leaf might only contain `[60]`. 
The parent **still** has the routing key `50`. This is perfectly mathematically valid! The routing key `50` still perfectly separates the left side (`40`) from the right side (`60`). 

**The Trade-off:**
If we forced the Vacuum process to remove empty leaf nodes and update parent routing keys, we would have to implement complex recursive B-Tree node merging (underflow handling) which requires stalling the database with heavy locks. 
By allowing these "ghost" routing keys to remain in the internal nodes indefinitely, we trade a microscopic amount of wasted disk space in exchange for incredibly fast, lock-free, and simple deletions. 

---

## 5. Implementation Notes

The actual implementation of these concepts evolved with the following specific logic:

### The Free Page List Stack
Instead of building a separate data structure for free pages, we treat `PageTypeFree (4)` pages as a persistent linked list (a Stack) entirely embedded within the physical file.
- **`deallocatePage(pageID)` (Push)**: When a page is freed, we format it as `PageTypeFree`. We write the current `MetaPage.FirstFreePageID` into its header as the "next" pointer. We then update the Meta-Page so `FirstFreePageID` points to this newly freed page. 
- **`allocatePage(pageType)` (Pop)**: When a new page is requested, if `FirstFreePageID` is non-zero, we read that free page to find its "next" pointer. We update the Meta-Page to point to the next free page, re-format the popped page to the requested `pageType`, and return its ID. This guarantees O(1) space reclamation without expanding the file size on disk.

### Immediate Overflow Reclamation
During implementation, a memory leak risk was identified: if a user deletes a key that has an Overflow chain, the `Upsert` method creates a new Tombstone cell with a nil value. Because the Tombstone has no value, the Overflow flag is stripped. If we waited for the background `Vacuum()` to free the pages, it would have lost the pointer to the old overflow chain!

**Solution:** The logic was adjusted so that `upsertCell` immediately calls `deallocatePage` on any old overflow chain that is being overwritten by an update or a tombstone. This keeps the tombstone cells completely tiny (0 bytes of value) while immediately reclaiming the huge overflow payload, rather than deferring it to the Vacuum process.

### DFS Vacuum Traversal
Because our B-Tree lacks right-sibling pointers at the leaf level, the `Vacuum()` background process cannot simply scan across the bottom of the tree. Instead, it performs a Depth-First Search (DFS) starting from the Root Page. 
It uses **Latch Crabbing**: acquiring a Read Latch on internal nodes to collect their child pointers, dropping the lock, and recursing downward. When it hits a leaf node, it briefly drops the read lock and upgrades to an exclusive Write Latch, drops the dead tombstone cells, rewrites the page, and unlocks it. This allows the background cleanup to run concurrently without halting user queries.
