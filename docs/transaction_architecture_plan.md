# Transaction Architecture & Concurrency Control Plan

## Overview
This document outlines the planned architecture for implementing Transactions and Multi-Version Concurrency Control (MVCC) with an **Immediate Apply + Undo Log** approach. 

This design aligns closely with traditional relational databases (like PostgreSQL and InnoDB) and flawlessly complements our existing ARIES Recovery architecture.

---

## 1. Immediate Execution Model
Instead of buffering commands and applying them in a batch, commands will execute against the B-Tree in real-time.

### Flow:
1. **`BEGIN`**: The client initiates a transaction. The Database generates a unique `TxID` (timestamp or sequence) and adds it to the **Global Transaction Table** with a state of `Running`.
2. **Immediate Application**: As the client sends commands (e.g., `SET X 10`, `DELETE Y`), they are immediately executed in the B-Tree. New MVCC records are inserted with the current `TxID`. The WAL logs these changes immediately, including a `PrevLSN` pointer to the transaction's previous log record.
3. **`COMMIT`**: The engine writes a `COMMIT` record to the WAL, and updates the Global Transaction Table to mark the `TxID` as `Committed`.
4. **`ROLLBACK`**: The engine triggers the Undo Phase, scanning backward through the WAL via `PrevLSN` pointers to generate Compensation Log Records (CLRs) and revert the B-Tree state.

### Advantage over Buffering:
Clients can read their own writes. A client can `SET balance 100`, and immediately run `GET balance` to retrieve `100`, making interactive transactions possible.

---

## 2. Global Transaction Table
To protect readers from dirty reads of uncommitted data, we implement a **Global Transaction Table**.

### States:
* **`Running`**: The transaction is active. Its writes are in the B-Tree but invisible to other transactions.
* **`Committed`**: The transaction is finalized. Its writes are permanently visible.
* **`Rollback`**: The transaction was aborted. Its writes are logically invalid (and will be physically undone).

### `FindLatest` Visibility Rules (MVCC):
When `FindLatest(search_key, reader_txid)` scans the B-Tree for the newest version of a key:
1. It reads the `TxID` of the found version.
2. If `TxID == reader_txid`, the version is visible (transactions can read their own writes).
3. If the version's `TxID` is marked as `Committed` in the Global Transaction Table, it is visible.
4. If the version's `TxID` is `Running` or `Rollback` (and not the reader's), it is ignored, and the scan continues to an older, committed version.

---

## 3. Conflict Resolution
Because we apply changes immediately, two active transactions might try to modify the same key.

### Write-Write Conflicts:
If a transaction attempts to write a key, but discovers that another `Running` transaction has already written an uncommitted version of that key:
* **Fail-Fast**: To avoid complex deadlock detection, the younger transaction can immediately abort, triggering a `ROLLBACK`, and returning a conflict error to the client. Alternatively, we can use a wait-die scheme.

---

## 4. ARIES Undo Logging & Rollback
This is the final missing piece of our ARIES protocol.

### Structure:
Every WAL record for a data modification must include a `PrevLSN` field. The Active Transaction Table tracks the `LastLSN` for every `Running` transaction.

### Rollback Process:
1. Fetch the `LastLSN` from the Transaction Table.
2. Read the WAL record at `LastLSN`.
3. Apply the inverse operation to the B-Tree (e.g., if it was an `Insert`, we execute a `Delete` of that exact version).
4. Write a **Compensation Log Record (CLR)** to the WAL (to ensure rollbacks are durable).
5. Follow the `PrevLSN` pointer backward and repeat until `PrevLSN == 0`.
6. Mark the transaction as `Rollback` in the Transaction Table.

This Undo process is used both for interactive client `ROLLBACK` commands and during system startup if the database crashed while transactions were `Running`.
