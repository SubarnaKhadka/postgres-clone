# PostgreSQL Internals

This document explains how we will reimplement it in Go with goroutines instead of processes.

---

## 1. Process Model (What We Are Replacing)

### How PostgreSQL Works
- **Postmaster** — The main daemon process. Listens on TCP port 5432.
- On every new client connection, postmaster calls `fork()` to create a **backend process**.
- Each backend process has its own private memory and communicates with others via **shared memory** (System V or POSIX).
- Shared memory holds: buffer pool, lock tables, WAL buffers, proc array, transaction state.
- IPC is done via **semaphores**, **lightweight locks (LWLocks)**, and **spin locks**.

### Why This Matters
The fork model gives isolation (a crash in one backend doesn't kill others) but is
expensive: ~1-5 MB per connection, fork overhead, shared memory complexity, no
lightweight communication.

### Our Go Replacement
| PostgreSQL                  | GoPostgres                              |
|-----------------------------|-----------------------------------------|
| Postmaster (main process)   | Main goroutine + TCP listener           |
| fork() per connection       | `go handleConnection(conn)` per client  |
| Shared memory segments      | Go structs with sync.Mutex/RWMutex      |
| System V semaphores         | sync.Cond, channels, sync.WaitGroup     |
| LWLocks                     | sync.RWMutex                            |
| Spin locks                  | sync/atomic operations                  |
| Signal-based communication  | Channels + context.Context cancellation |

**Advantage:** Goroutines cost ~2-8 KB of stack (grows as needed). We can handle
10,000+ concurrent connections easily. Shared state is in-process, no serialization overhead.

**Risk:** No crash isolation — a panic in one goroutine can crash the whole process.
We must use `recover()` at connection boundaries.

---

# GoPostgres — Implementation Roadmap

## Day 1: TCP Server + Wire Protocol + Handshake
**Goal:** Accept connections from `psql` and complete the startup handshake.

### 1.1: TCP Listener
- Listen on configurable port (default 5432).
- Accept connections in a loop.
- Spawn a goroutine per connection: `go handleConnection(conn)`.
- Graceful shutdown via `context.Context`.
- Connection limit enforcement.

### 1.2: Startup Message Parsing
- Read the initial message (no type byte: length + protocol version + key-value params).
- Handle SSLRequest (respond with 'N' for now — no SSL).
- Parse protocol version (must be 3.0 = 196608).
- Extract `user`, `database`, and other parameters.

### 1.3: Authentication
- Implement `trust` auth (always accept).
- Send AuthenticationOk (R + 0).

### 1.4: Post-Auth Messages
- Send ParameterStatus messages (server_version, client_encoding, etc.).
- Send BackendKeyData (connection ID + cancel key).
- Send ReadyForQuery ('I' for idle).

### 1.5: Simple Query Protocol (Stub)
- Read 'Q' message.
- For now, return ErrorResponse ("not implemented") + ReadyForQuery.
- Handle 'X' (Terminate) to close cleanly.

## Day 2: In-Memory Catalog + CREATE TABLE / DROP TABLE
**Goal:** Maintain metadata about tables in memory.

### 2.1: Type System Foundation
- Define Go types for the core PostgreSQL types (int4, int8, text, bool, float8,
  timestamp, etc.).
- Map OIDs to Go types.
- Implement text encoding/decoding for each type.

### 2.2: Catalog Structures
- In-memory catalog: maps of database -> schema -> table -> columns.
- `TableDef`: name, OID, columns (name, type OID, nullable, default).
- `ColumnDef`: name, type OID, type modifier, not null, has default.

### 2.3: SQL Parser (Minimal)
- Lexer: tokenize SQL into keywords, identifiers, literals, operators, punctuation.
- Parser: recursive descent, handle:
  - `CREATE TABLE name (col1 type1 [NOT NULL] [PRIMARY KEY], ...)`
  - `DROP TABLE [IF EXISTS] name`
- Produce AST nodes: `CreateTableStatement`, `DropTableStatement`.

### 2.4: Execute DDL
- CREATE TABLE: validate types, add to catalog, return CommandComplete ("CREATE TABLE").
- DROP TABLE: remove from catalog, return CommandComplete ("DROP TABLE").


## Day 3: Heap Storage Engine + Sequential Scan
**Goal:** Store actual data on disk in 8 KB pages. Read it back.

### 3.1: Page Layout
- Implement 8 KB page structure: header, item pointers, free space, tuple data.
- Page read/write functions.
- Tuple encoding: null bitmap + column values in type-specific binary format.

### 3.2: Heap File
- One file per table.
- Append new tuples to the last page (or first page with space).
- Simple free space tracking (scan pages for space, or maintain a free list).

### 3.3: Buffer Pool (Optimization part, will implement later)
- Fixed-size buffer pool (configurable, e.g., 1000 pages = ~8 MB).
- Hash table: (file, block_number) -> buffer slot.
- Pin/Unpin mechanism.
- Clock sweep eviction.
- Dirty page tracking.

### 3.4: INSERT
- Parse: `INSERT INTO table (col1, col2) VALUES (val1, val2)`
- Encode tuple, find a page with space, insert.
- Return CommandComplete ("INSERT 0 1").

### 3.5: Sequential Scan + SELECT
- Parse: `SELECT col1, col2 FROM table [WHERE condition]`
- Executor: SeqScan node reads every page, every tuple.
- Filter: evaluate WHERE clause on each tuple.
- Return RowDescription + DataRow messages.

### 3.6: Simple Expressions (Implementing) 
- Comparison operators: =, !=, <, >, <=, >=
- Logical operators: AND, OR, NOT
- Literal values: integers, strings, booleans, NULL
- IS NULL / IS NOT NULL
- Column references

## Next Plan: UPDATE, DELETE, and Basic MVCC
**Goal:** Modify and delete rows with basic multi-version concurrency control.

### 4.1: Transaction IDs
- Global XID counter (64-bit, atomically incremented).
- Each connection gets implicit transaction for each statement (autocommit).

### 4.2: Tuple Versioning
- Add `xmin` and `xmax` to tuple header.
- INSERT sets `xmin = current_xid`, `xmax = 0`.
- DELETE sets `xmax = current_xid` on existing tuple.
- UPDATE = DELETE old + INSERT new (with `t_ctid` chain).

### 4.3: Visibility Checks
- Read the current XID and list of in-progress transactions.
- Apply visibility rules: tuple visible if xmin committed and xmax not committed/not set.
- For now: simple READ COMMITTED (each statement sees latest committed data).

### 4.4: CLOG
- Commit log tracking transaction status: in-progress, committed, aborted.
- In-memory for now (bitmap), persisted later.

### 4.5: DELETE Execution
- Parse: `DELETE FROM table WHERE condition`
- SeqScan + Filter to find matching tuples.
- Mark each with xmax.
- Return CommandComplete ("DELETE N").

### 4.6: UPDATE Execution
- Parse: `UPDATE table SET col1 = expr WHERE condition`
- For each matching tuple: mark old version with xmax, insert new version with xmin.
- Return CommandComplete ("UPDATE N").