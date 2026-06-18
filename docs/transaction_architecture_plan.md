# Transaction Architecture & Concurrency Control Plan

## Overview
This document outlines the planned architecture for implementing Transactions, Multi-Version Concurrency Control (MVCC), and Optimistic Locking in the database. 

The chosen design heavily mimics **Redis's `MULTI`/`EXEC` transaction model**, utilizing a **Batch Execution Model** combined with **Optimistic Concurrency Control (OCC)** and **Automatic Retries**.

---

## 1. The Batch Execution Model
Instead of executing commands interactively one by one, the database will queue commands submitted by the client during an active transaction. 

### Flow:
1. **`BEGIN`**: The client initiates a transaction. A new transaction context is created in memory, equipped with a command buffer.
2. **Buffering**: As the client sends commands (e.g., `SET X 10`, `GET Y`), they are parsed and stored purely in the transaction's local buffer. **No execution against the B-Tree happens yet.**
3. **`COMMIT`**: The execution phase begins. The database takes the entire buffer of commands, acquires a transaction timestamp (`TxID`), and begins applying the operations one by one against the B-Tree.

### Drawback: Lack of Interactive Transactions
Because `GET` commands are merely buffered and not actually executed until `COMMIT`, the client application cannot make decisions mid-transaction based on database state. 
* **Impossible Workflow:** Client runs `GET balance` -> Wait for DB reply -> Client calculates `balance - 50` -> Client runs `SET balance`.
* **Solution:** Clients must submit completely self-contained logic, or rely on optimistic locking checks (like Redis `WATCH`), knowing that the sequence of commands executes blindly as a batch at the very end.

---

## 2. Global Transaction Table
Because multiple batches will be executing concurrently across different threads upon reaching the `COMMIT` phase, the B-Tree will contain uncommitted data.

To protect readers from dirty reads, we will implement a **Global Transaction Table**.

### States:
* **`Running`**: The transaction is actively applying its buffered operations to the B-Tree.
* **`Committed`**: All operations finished successfully and validation passed.
* **`Rollback`**: The transaction encountered a conflict or error and its operations are invalid.

### `FindLatest` Visibility Rules:
When `FindLatest(search_key, reader_txid)` scans the B-Tree for the newest version of a key:
1. It must check the `TxID` of the found version against the Transaction Table.
2. It **only** returns versions whose `TxID` is explicitly marked as `Committed`.
3. If a version belongs to a `Running` or `Rollback` transaction, it ignores it and continues scanning for an older, committed version.

---

## 3. Optimistic Concurrency Control (OCC) & Automatic Retries
To achieve strict isolation (handling Write-Write conflicts and Write Skew) without heavy locking, the execution phase will use optimistic locking.

### Validation Phase:
As the buffered commands are executed, the engine tracks what keys were read and written. 
* **Conflict Detection:** If the transaction attempts to write a key, but discovers that a *younger* (more recently committed) transaction has already modified that key, a conflict is triggered.
* **Write Skew Detection:** Before marking as `Committed`, verify that none of the keys *read* during execution have been modified by another committed transaction.

### Automatic Retry Mechanism:
Unlike traditional databases that throw an error and force the client application to retry from scratch, this batch architecture gives us a superpower: **We have the entire sequence of the client's commands in memory.**

If a conflict is detected:
1. The transaction state is marked as `Rollback`.
2. A brand new `TxID` (timestamp) is generated.
3. The execution engine automatically re-runs the entire command buffer against the new state of the database.
4. The client application never knows a conflict occurred; it simply receives a successful `COMMIT` response once a retry succeeds.
