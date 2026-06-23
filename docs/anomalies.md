# Database Transaction Anomalies

When designing a database, preventing anomalies is a balancing act between perfect mathematical correctness and raw performance. Below are the 6 primary transaction anomalies, how standard relational databases handle them by default, and how our Immediate Apply + MVCC architecture mitigates them.

---

## 1. Dirty Write
**The Anomaly:** Transaction A modifies a key. Before A commits or rolls back, Transaction B overwrites that exact same key. If A later decides to abort, the database is in an impossible state regarding B's write.
* **The Industry Standard:** **Prevented by all databases.** Even the lowest isolation level (`Read Uncommitted`) strictly prohibits dirty writes, as allowing them breaks basic crash recovery and structural integrity.
* **How Our DB Handles It:** **Prevented.** We use **Write-Write Conflict Detection**. If Transaction B attempts to write a key and detects that the latest MVCC version belongs to a `Running` transaction, Transaction B is immediately aborted (Fail-Fast).

---

## 2. Dirty Read
**The Anomaly:** Transaction A writes a value but hasn't committed yet. Transaction B reads that uncommitted value. If Transaction A later rolls back, Transaction B has made decisions based on garbage data that theoretically never existed.
* **The Industry Standard:** **Prevented by almost all databases.** The default isolation level for most engines is `Read Committed` (or higher), which explicitly blocks dirty reads.
* **How Our DB Handles It:** **Prevented.** Our **MVCC Read Visibility Rules** dictate that when a transaction executes a `GET` (via `FindLatest`), it evaluates the `Global Transaction Table`. Any versions belonging to `Running` or `Rollback` transactions (other than its own) are completely ignored.

---

## 3. Non-Repeatable Read (Fuzzy Read)
**The Anomaly:** Transaction A reads `X=10`. Concurrently, Transaction B changes `X` to `20` and commits. If Transaction A reads `X` again during the same transaction, it gets `20`. The read was not repeatable.
* **The Industry Standard:** **Allowed by default in many databases.** PostgreSQL, Oracle, and SQL Server default to `Read Committed`, which allows this anomaly. You must upgrade to `Repeatable Read` to prevent it.
* **How Our DB Handles It:** **Prevented.** By locking the reader's view of the database to the timestamp (`TxID`) acquired at `BEGIN`, a transaction will only ever see MVCC versions committed *before* its own `TxID`. Any writes committed by other transactions after our `TxID` are invisible, guaranteeing repeatable reads.

---

## 4. Phantom Read
**The Anomaly:** Transaction A queries a set of rows matching a condition (e.g., `SELECT * WHERE age > 20`) and gets 5 rows. Transaction B inserts a new user aged 25 and commits. Transaction A runs the exact same query again and suddenly gets 6 rows. A "phantom" record has appeared.
* **The Industry Standard:** **Allowed by default in many databases.** PostgreSQL allows this in `Read Committed`. MySQL (InnoDB) tries to prevent it by default using Next-Key Locks.
* **How Our DB Handles It:** **Not Applicable / Prevented.** Because our engine is a pure Key-Value store (supporting single-key `GET` and `SET`, rather than range-based `SELECT` queries), the concept of phantom records appearing in a range scan doesn't apply. If we ever implement range scans, our MVCC snapshot rules will inherently hide phantoms anyway.

---

## 5. Lost Update
**The Anomaly:** A classic "Read-Modify-Write" race condition. Transactions A and B both read `X=10`. A computes `10+5=15` and writes it. B computes `10+2=12` and writes it. B commits last, completely wiping out A's update.
* **The Industry Standard:** **Allowed by default in Read Committed.** Databases like Postgres will allow this unless the developer explicitly uses row-level locks (`SELECT ... FOR UPDATE`).
* **How Our DB Handles It:** **Prevented.** We employ a **"First-Updater-Wins"** rule. When Transaction B attempts to `SET` a key, it checks if the most recent committed version has a `TxID` greater than B's starting `TxID`. If it does, B knows the underlying data changed since B started, so B's write is rejected, and B is forced to abort.

---

## 6. Write Skew
**The Anomaly:** Involves constraints across multiple keys. Rule: `A + B > 0`. Currently, `A=10, B=10`. Transaction A reads both, then sets `A = -5` (sum is 5, valid). Transaction B concurrently reads both, then sets `B = -5` (sum is 5, valid). Both commit. Now `A=-5, B=-5`, and the sum is `-10`. The constraint is broken!
* **The Industry Standard:** **Allowed by almost all databases.** Even `Repeatable Read` and `Snapshot Isolation` suffer from Write Skew. To prevent it, you must use the strictest level: `Serializable`.
* **How Our DB Handles It:** **Allowed.** Because we grant **Snapshot Isolation** through MVCC, Write Skew is still possible. Preventing it would require full transaction read-set tracking or strict two-phase locking, which would massively degrade the high-performance concurrent nature of our engine. For a Key-Value store, allowing Write Skew is the standard, pragmatic choice.
