A **Redis-like in-memory cache system** written in Go — built from scratch to explore the **design, trade-offs, and internals** of high-performance caching.  

It supports **TTL expiry, sharding, rebalancing, directory indexing, and a network API** — designed for learning and experimentation.

---

## 📖 Table of Contents
- [✨ Features](#-features)
- [🏗 Architecture](#-architecture)
- [⚖️ Design Trade-offs](#️-design-trade-offs)
- [📦 Installation](#-installation)
- [▶️ Running the Server](#️-running-the-server)
- [📡 HTTP API](#-http-api)
- [📊 Examples](#-examples)
- [🔮 Roadmap](#-roadmap)
- [🛠 Development](#-development)
- [🤝 Contributing](#-contributing)
- [📜 License](#-license)

---

## ✨ Features
- **Concurrent-safe cache engine** (`Set`, `Get`, `Delete`, TTL).
- **Sharding across multiple nodes** for reduced contention.
- **Power of Two Choices (PoTC)** strategy for balanced placement.
- **Per-node Timing Wheel** for efficient TTL expiry:
  - O(expired) instead of O(total).
  - Current: 3600 slots (1 hour resolution).
- **Rebalancer**:
  - Batched directory updates.
  - Deterministic locking (avoids deadlocks).
  - Per-key versioning (avoids lost writes).
- **Admin operations**: `KEYS`, `SCAN`, `FLUSHALL`, `STATS`.
- **HTTP API** (via Gorilla Mux) for remote access.
- **Background workers**: expiry sweeper & rebalancer.

---

## 🏗 Architecture

### Sharding
- Cache is split into **N nodes**.  
- Each node has its own map + lock.  
- A **directory** maps `key → node index`.  
- **PoTC** chooses between two candidate nodes to place a key.

```
+-------------+      +----------+
| Key Hashing | ---> | Node[i]  |
+-------------+      +----------+
                         ...
                      +----------+
                      | Node[n]  |
                      +----------+
```

### Timing Wheel
- Each node has a **3600-slot wheel** (per second, 1 hour horizon).  
- Keys expire by being slotted into the right bucket.  
- Each tick scans only 1 slot (O(expired)).

```
+---------------------------------------------------+
| Slot[0] | Slot[1] | ... | Slot[3599]             |
+---------------------------------------------------+
   ↑ current tick (every second)
```

⚠️ Limitation: Only supports TTL ≤ 1 hour.  
✅ Future: **Hierarchical Timing Wheels** (seconds → minutes → hours → days).

### Rebalancer
- Periodically checks for skewed node sizes.  
- Migrates keys from hot nodes → lighter nodes.  
- Uses:
  - **Per-key versioning** → prevents overwriting during migration.  
  - **Batch directory updates** → reduces lock contention.  
  - **Deterministic locking order** → avoids deadlocks.

---

## ⚖️ Design Trade-offs

- **Node Count**  
  - Small (8–16): less memory overhead, more contention.  
  - Large (64–256): better concurrency, higher metadata + rebalancing cost.  

- **Expiry**  
  - Naive sweeper: O(n) → bad at scale.  
  - Timing wheel: O(expired) → efficient, but short TTL horizon.  

- **Rebalancing**  
  - Needed for fairness.  
  - Must avoid race conditions, deadlocks, and lost writes.  

---

## 📦 Installation

Clone the repo:

```bash
git clone https://github.com/PriyanshuPd5/sharded-cache.git
cd sharded-cache
```

Install dependencies:

```bash
go mod tidy
```

---

## ▶️ Running the Server

Run the cache server locally:

```bash
go run ./cmd/server
```

Default: runs at **http://localhost:8080**

---

## 📡 HTTP API

### `POST /set`
Set a key with optional TTL.

```json
{
  "key": "user:1",
  "value": "alice",
  "ttl": 30
}
```

### `GET /get/{key}`
Fetch a value.

```json
{ "key": "user:1", "value": "alice" }
```

### `DELETE /del/{key}`
Delete a key.

```json
{ "status": "deleted" }
```

### `GET /keys?pattern=user:*`
Fetch all keys matching a pattern (glob).

### `GET /scan?cursor=0&match=user:*&count=2`
Incremental key scan (cursor-based).

### `POST /flushall`
Clear all keys.

### `GET /stats`
Get node-level stats.

```json
{
  "total": 12345,
  "nodes": [
    { "index": 0, "keys": 1234 },
    { "index": 1, "keys": 5678 }
  ]
}
```

---

## 📊 Examples

```bash
# Set key with TTL 30s
curl -X POST -H "Content-Type: application/json"   -d '{"key":"user:1","value":"alice","ttl":30}'   http://localhost:8080/set

# Get key
curl http://localhost:8080/get/user:1

# Delete key
curl -X DELETE http://localhost:8080/del/user:1

# List keys
curl http://localhost:8080/keys?pattern=user:*
```

---

## 🔮 Roadmap

- [ ] Hierarchical timing wheels (support days/weeks TTL).
- [ ] Prometheus metrics & observability.
- [ ] Persistence (snapshotting / AOF).
- [ ] Value search / secondary indexes.
- [ ] Cluster mode:
  - Consistent hashing across nodes.
  - Replication for fault tolerance.
  - Gossip protocol for membership.

---

## 🛠 Development

Run tests (once added):

```bash
go test ./...
```

Lint code:

```bash
golangci-lint run
```

---

## 🤝 Contributing

Contributions are welcome!  

- Open an [issue](https://github.com/PriyanshuPd5/sharded-cache/issues) for bugs/ideas.  
- Fork and submit a PR for improvements.  

---

## 📜 License

MIT — free to use, modify, and learn from.
