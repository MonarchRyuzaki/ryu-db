# Buffer Manager Plan & Architecture

The Buffer Manager (or Buffer Pool) sits just above the Disk/Pager layer. Its sole responsibility is to manage memory (RAM) and give higher levels the illusion that the entire database fits in memory. It should remain completely unaware of higher-level database logic.

## 1. What belongs IN the Buffer Manager
* **Page Table (Hash Map):** Translates a `PageID` into a memory location (Frame) so that if a page is already loaded, it isn't loaded twice.
* **Frames (The Cache):** A fixed-size array/pool of memory blocks where disk pages are temporarily held.
* **Pinning & Unpinning:**
  * When a thread needs a page, it **pins** it (`Pin(pageID)`). A pinned page (`pin_count > 0`) cannot be evicted.
  * When done, it **unpins** it (`Unpin(pageID, isDirty)`).
* **Dirty Tracking:** A flag to mark a page that has been modified. Dirty pages must be written to disk before their memory frame can be reused.
* **Eviction Policy:** An algorithm (e.g., **Clock** or **LRU**) used to select a "victim" page (which has `pin_count == 0`) to kick out when the cache is full and a new page needs to be loaded.
* **WAL Enforcement (Write-Ahead-Log):** The only transaction-related rule the Buffer Manager must enforce. Before writing a dirty page to disk, it must ensure the WAL is flushed up to the page's `LSN` (Log Sequence Number). 
*(Note: We can stub this out for now by just writing to disk immediately until we build the WAL manager).*

## 2. Implementation Plan for Buffer Manager
1. **Define Structures:**
   * Create an array of `Frame` objects.
   * Create a mapping of `PageID -> Frame Index`.
2. **Implement Fetch/Pin:** 
   * `FetchPage(pageID)`: Check if page is in memory. If yes, increment pin count and return. If no, find an empty or unpinned frame (evicting if necessary), read from disk via `pager.go`, increment pin count, and return.
3. **Implement Unpin:** 
   * `UnpinPage(pageID, isDirty)`: Decrement pin count. If `isDirty` is true, mark the frame as dirty.
4. **Implement Eviction:** 
   * Simple Clock or LRU algorithm to find a frame with `pin_count == 0`.
   * If the chosen frame is dirty, call `pager.WritePage()` to flush it to disk.

## 3. What belongs OUTSIDE the Buffer Manager
*(These are higher-level concepts from Chapter 5 that will be built at the very end of the project)*

* **Transaction Management & MVCC:** Handled by a Transaction Manager and B-Tree layer. The Buffer Manager does not know what a transaction is.
* **ARIES Protocol:** This is the recovery algorithm. A separate **Recovery Manager** runs this during crash recovery by reading the WAL and calling the Buffer Manager to replay state. 
* **Locks (2PL):** Transactional concurrency control (Locks) goes at the tuple or key level. The Buffer Manager only uses **Latches** (short-term Mutexes) to protect the physical bytes of the page array in memory from concurrent thread crashes.
* **WAL (Write-Ahead Logging):** The Log Manager physically writes the log records. The Buffer Manager just checks with the Log Manager to see if an LSN has been safely flushed.
