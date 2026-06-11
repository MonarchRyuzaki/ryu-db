# DB-Internals: High-Performance B-Tree Storage Engine

> **Note:** This is an educational project built from scratch while studying the concepts in the book *Database Internals* by Alex Petrov. The goal was to understand how the lowest physical layers of a database actually work under the hood. While it achieves surprisingly high performance, it is intended as a learning exercise rather than an industry-standard, production-ready system.

A lightning-fast, disk-backed Key-Value storage engine built entirely from scratch in Go. This project implements the physical storage layer found at the bottom of modern databases (like PostgreSQL, MySQL, and SQLite), focusing heavily on concurrency, memory management, and disk I/O optimization.

## 🚀 Features

- **Disk-Backed B-Tree**: A persistent B-Tree structure where data is stored in fixed 4KB pages.
- **Overflow Pages**: Automatically chunks massive payloads (like 10KB+ JSON objects) across linked overflow pages on disk.
- **Page-Level Latching (Latch Crabbing)**: Highly concurrent `sync.RWMutex` locking mechanism that allows hundreds of goroutines to traverse and mutate the tree simultaneously without global locks.
- **Zero-Copy Deserialization**: `Find` operations execute with exactly **0 memory allocations** (`1 alloc/op` due to testing overhead), preventing Garbage Collector pauses.
- **Buffer Pool Manager**: Intelligent memory caching layer that prevents thrashing the physical disk.
- **Background Vacuuming**: Deletions use *Tombstones*. A background goroutine periodically runs a Depth-First Search (DFS) Latch Crabbing traversal to drop tombstones and rewrite pages.
- **O(1) Space Reclamation**: Freed pages and dead overflow chains are instantly pushed to a persistent Free Page Stack (tracked by the `MetaPage`) and immediately reused during the next allocation, preventing disk bloat.

## ⚡ Performance 

The engine was heavily benchmarked on an AMD Ryzen 5 processor. Thanks to the Zero-Copy Slicing and Page-Level Latching optimizations, the engine achieves enterprise-grade speeds:

| Operation | Throughput | Latency | Allocations |
| :--- | :--- | :--- | :--- |
| **Sequential Writes** | ~59,000 ops/sec | `16,927 ns/op` | `93 allocs/op` |
| **Random Writes** | ~91,000 ops/sec | `10,963 ns/op` | `92 allocs/op` |
| **Random Reads (Find)** | **~485,000 ops/sec** | `2,060 ns/op` | `1 alloc/op` |
| **Parallel Read/Write (50/50)** | **~531,000 ops/sec** | `1,882 ns/op` | `34 allocs/op` |
| **E-Commerce JSON Workload** | ~255,000 ops/sec | `3,913 ns/op` | `7 allocs/op` |

> 📖 **Read the Case Study:** Check out [res/High Allocs Case Study.md](./res/High%20Allocs%20Case%20Study.md) to see how we dropped read allocations from 237 down to 1 using Go Escape Analysis!

## 💻 Interactive CLI (REPL)

You can interact directly with the storage engine using the built-in KV-store REPL. 

### Starting the Database
```bash
go run ./cmd/db/main.go
```

### Commands
Once the database initializes, you can execute raw physical commands:

```text
db> SET user_1 "John Doe"
OK (took 34.5µs)

db> GET user_1
"John Doe" (took 2.1µs)

db> DELETE user_1
OK (took 15.2µs)

db> EXIT
Shutting down database...
```

*(Note: The background Vacuum process runs every 10 seconds and will automatically clean up the tombstone left by the `DELETE` command).*

## 📚 Architecture Deep-Dives
If you want to learn more about how the internals of this database work, read the detailed architectural design documents:
- [Mutation & Vacuum Architecture](./docs/mutation_and_vacuum_plan.md)
- [Zero-Copy Optimization Case Study](./res/High%20Allocs%20Case%20Study.md)