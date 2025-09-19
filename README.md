# Sharded TTL-aware Cache

Modular Go implementation of a sharded in-memory cache with:
- PoTC placement for balanced inserts,
- Sharded directory `key -> nodeIdx` for O(1) lookup,
- Per-node TTL sweeping to remove expired entries,
- A TTL-aware incremental rebalancer skeleton.

Run example:
```
go run ./cmd/example
```

This repo is for experimentation and will need performance tuning for production (directory memory, shard counts, GC tuning, batching tuning).
