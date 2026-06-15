# Storage Engine Implementation Plan

## Overview
This document outlines the phased implementation plan for the database storage engine. It tracks completed components, skipped steps, and the upcoming roadmap.

---

## Completed Components

### 1. `internal/storage/cell.go` ✅
**Goal:** Define how a single record (key-value pair) is serialized.

* **Struct `Cell`:**
    * `Key []byte`
    * `Value []byte`
* **Essential Functions:**
    * `SerializeCell(c Cell) []byte`: Implements the Pascal String format. 
        * **Layout:** `[KeyLen (uint16)][KeyBytes...][ValueLen (uint16)][ValueBytes...]`
    * `DeserializeCell(data []byte) Cell`: The inverse operation.
    * `CellSize(c Cell) int`: Returns the total bytes a cell will occupy on disk.

### 2. `internal/storage/page.go` ✅
**Goal:** Implement the Slotted Page logic (the "Slotted Page" is basically a wrapper around a `[]byte`).

* **Constants:**
    * `PageSize = 4096`
    * `HeaderSize = 16` (Adjust as needed)
* **Struct `Page`:**
    * `data []byte` (This should always be exactly 4096 bytes)
* **Header Accessor Methods** (uses `encoding/binary`):
    * `getSlotCount()`, `setSlotCount(n uint16)`
    * `getFreeSpaceOffset()`, `setFreeSpaceOffset(o uint16)`
    * `getFirstFreeblock()`, `setFirstFreeblock(o uint16)`
* **Core Page Methods:**
    * `Init()`: Sets default header values (e.g., FreeSpaceOffset to PageSize).
    * `Insert(cellData []byte) (int, error)`: 
        1. Search availability list.
        2. Else, append to free space.
        3. Update slot directory.
        4. Return the slotID.
    * `Get(slotID int) []byte`: Finds the offset in the directory and returns the cell data.
    * `Delete(slotID int)`: Marks a slot as empty and adds the space to the availability list. *(Deferred)*
    * `Compact()`: The defragmentation logic. *(Deferred)*

### 3. Directory and Bugfixes ✅
* **Step 3 (The Directory):** Implemented `Insert` without the availability list first by appending cells and growing the slot directory.
* **Step 3.5 (Bugfix - Slot Entry Size):** Updated slot entry from 2 bytes (offset only) to 4 bytes (offset + length).
    * Each slot entry: `[CellOffset (2b)][CellLength (2b)]`
    * Updated `directoryEnd` calculation: `slotCount * 4` (was `slotCount * 2`)
    * Updated `Insert` to write both offset and length into slot entry
    * Updated `Get` to read length from slot entry and return `p.data[cellOffset : cellOffset+cellLength]`

### 4. `internal/storage/pager.go` ✅
**Goal:** Handle the physical file and "Page ID" to "Byte Offset" translation.

* **Struct `Pager`:**
    * `file *os.File`
* **Core Methods:**
    * `NewPager(path string) (*Pager, error)`: Opens the file (use `os.O_RDWR|os.O_CREATE`).
    * `ReadPage(id uint32) (*Page, error)`: Calculates `id * 4096`, reads into a `Page.data`.
    * `WritePage(id uint32, p *Page) error`: Writes the 4096 bytes back.
    * `AllocatePage() (uint32, error)`: Returns a new ID by extending the file.

---

## Deferred / Skipped Steps

* **Step 5 (Sorting):** ⏭️ *(Skipped)*
    * Sort the slotIDs in the directory by key order. 
    * *Note:* Slot directory is currently unsorted (insertion order). A sorted index array can be maintained separately for binary search within a page without changing slot IDs. Deferred.

### Order of Operations Shift
The steps below are skipped for now to prioritize understanding what pages look like in practice. The revised order of development will be:
1. **Buffer Manager** ✅
2. **B+ Tree Implementation** ✅
3. **Updation and Deletion (Page internals)** ✅

---

## Upcoming Advanced Steps

* **Step 6 (Overflow Pages):** Implement overflow pages for large values that can't fit in a single page.✅
* **Step 7 (Page Updates/Deletes):** Implement page update logic (if done in place, what if overflow?), delete, and reinsert (tombstone).✅
* **Step 8 (Vacuum Process & Meta-Page):** Implement a background Vacuum process to clear Tombstones, and a Meta-Page (Page 0) to track the Free Page List (reusing empty and overflow pages).
* **Step 9 (Benchmarking):** Write a small benchmark script to measure raw Insert/Get operations per second and prove the engine's speed.✅
* **Step 10 (Interactive REPL):** Build a simple command-line REPL (e.g., `PUT key val`, `GET key`) to demonstrate the working database interactively.✅
