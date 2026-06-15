# DB-Internals: Roadmap V2 (Transactions, Recovery & LSM-Trees)

Having successfully built a high-performance, disk-backed B-Tree with Page-Level Latching and Zero-Copy Deserialization, the project will now evolve from a simple "Storage Engine" into a fully ACID-compliant Database System. 

This roadmap mirrors the advanced concepts covered in *Database Internals* by Alex Petrov (specifically Chapters 5 and 7).

## Phase 1: Durability & Recovery (Chapter 5 - Part 1)
- **WAL (Write-Ahead Logging):** 
  - Implement an append-only log file to ensure durability (the `D` in ACID).
  - All mutations (Inserts/Updates/Deletes) must be sequentially written to the WAL before the corresponding B-Tree pages are flushed to disk.
  - Implement a `Steal / No-Force` Buffer Management policy.
- **ARIES Protocol:** 
  - Implement Crash Recovery using the ARIES algorithm.
  - **Analysis Phase:** Scan the WAL to reconstruct the state of active transactions and dirty pages at the time of the crash.
  - **Redo Phase:** Replay history to restore the database to its exact pre-crash state.
  - **Undo Phase:** Roll back uncommitted transactions.

## Phase 2: Transactions & Concurrency Control (Chapter 5 - Part 2)
- **Logical Transactions:** Introduce the concept of atomic transactions (`BEGIN`, `COMMIT`, `ROLLBACK`) on top of the physical storage layer.
- **Serializability:** Implement strict concurrency control mechanisms to ensure isolated transaction execution.
- **MVCC (Multi-Version Concurrency Control):**
  - Upgrade the engine to store multiple versions of a record using timestamps.
  - Ensure Readers never block Writers, and Writers never block Readers, maximizing concurrency.

## Phase 3: LSM-Trees vs. B-Trees (Chapter 7)
- **Log-Structured Merge-Trees:** 
  - Build a completely independent Storage Engine based on the LSM-Tree architecture.
  - Implement an in-memory `MemTable`.
  - Implement on-disk immutable `SSTables` (Sorted String Tables).
  - Implement a background Compaction routine.
- **The Ultimate Benchmark:** 
  - Pit the B-Tree engine against the LSM-Tree engine using the E-Commerce workload.
  - Compare write-amplification and read-amplification.
  - Demonstrate why LSM-Trees excel at write-heavy workloads while B-Trees dominate read-heavy workloads.
