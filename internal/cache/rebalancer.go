package cache

import (
	"sync/atomic"
	"time"
)

// StartRebalancer starts a background rebalancer that wakes up periodically and
// migrates small batches from overloaded nodes. Call once on startup.
func (c *Cache) StartRebalancer(interval time.Duration, stopCh <-chan struct{}) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.rebalanceOnce()
			case <-stopCh:
				return
			}
		}
	}()
}

// rebalanceOnce checks node loads and triggers migrations for hot nodes.
func (c *Cache) rebalanceOnce() {
	total := float64(atomic.LoadInt64(&c.totalKeys))
	if total == 0 || c.nodeCnt == 0 {
		return
	}
	avg := total / float64(c.nodeCnt)
	upper := avg * c.RebalanceUpper

	for idx := 0; idx < c.nodeCnt; idx++ {
		if float64(atomic.LoadInt64(&c.nodes[idx].keyCount)) > upper {
			c.expireSweepNode(idx)
			if float64(atomic.LoadInt64(&c.nodes[idx].keyCount)) > upper {
				c.rebalanceNode(idx, c.RebalanceBatch)
			}
		}
	}
}

// expireSweepNode attempts to remove expired keys from a single node.
func (c *Cache) expireSweepNode(idx int) {
	now := time.Now()
	node := c.nodes[idx]
	node.mu.Lock()
	for k, e := range node.mp {
		if !e.ExpireAt.IsZero() && now.After(e.ExpireAt) {
			delete(node.mp, k)
			if node.keyCount > 0 {
				node.keyCount--
			}
			c.dirDelete(k)
			atomic.AddInt64(&c.totalKeys, -1)
		}
	}
	node.mu.Unlock()
}

// rebalanceNode moves up to batchSize keys from srcIdx to other nodes, with version check.
// It batches directory updates per shard to reduce lock churn.

// rebalanceNode moves up to batchSize keys from srcIdx to other nodes.
// It batches directory updates per dir-shard and checks per-key versions to avoid lost writes.
func (c *Cache) rebalanceNode(srcIdx int, batchSize int) {
	src := c.nodes[srcIdx]
	now := time.Now()
	minLive := 30 * time.Second // don't move keys expiring sooner than this

	// collect candidate keys with their snapshot versions and expiry
	type cand struct {
		key     string
		value   []byte
		expire  time.Time
		version uint64
	}
	candidates := make([]cand, 0, batchSize)

	// gather candidates under read lock
	src.mu.RLock()
	for k, e := range src.mp {
		if len(candidates) >= batchSize {
			break
		}
		if !e.ExpireAt.IsZero() && e.ExpireAt.Sub(now) < minLive {
			continue
		}
		// snapshot value and version
		valCopy := make([]byte, len(e.Value))
		copy(valCopy, e.Value)
		candidates = append(candidates, cand{
			key:     k,
			value:   valCopy,
			expire:  e.ExpireAt,
			version: e.Version,
		})
	}
	src.mu.RUnlock()

	if len(candidates) == 0 {
		return
	}

	// For each candidate, choose a destination and insert copy there.
	// Also prepare per-key metadata for directory update.
	type moveMeta struct {
		key     string
		dstIdx  int
		expire  time.Time
		version uint64
	}
	moveList := make([]moveMeta, 0, len(candidates))

	for _, cc := range candidates {
		dstIdx := c.pickNodePoTC()
		if dstIdx == srcIdx {
			dstIdx = (srcIdx + 1) % c.nodeCnt
		}
		dst := c.nodes[dstIdx]

		// insert into destination
		dst.mu.Lock()
		// overwrite unconditionally (we will correct with version checks)
		dst.mp[cc.key] = &Entry{Value: cc.value, ExpireAt: cc.expire, TTL: int(time.Until(cc.expire).Seconds()), Version: cc.version}
		dst.keyCount++
		dst.mu.Unlock()

		moveList = append(moveList, moveMeta{
			key:     cc.key,
			dstIdx:  dstIdx,
			expire:  cc.expire,
			version: cc.version,
		})
	}

	// Group keys by directory shard for batched update
	shardToKeys := make(map[int][]moveMeta)
	shardIdxs := make([]int, 0)
	for _, m := range moveList {
		si := c.dirShardIndex(m.key)
		if _, ok := shardToKeys[si]; !ok {
			shardIdxs = append(shardIdxs, si)
		}
		shardToKeys[si] = append(shardToKeys[si], m)
	}

	// sort shardIdxs to lock deterministically and prevent deadlocks
	// simple insertion sort (number of shards per batch small)
	for i := 1; i < len(shardIdxs); i++ {
		j := i
		for j > 0 && shardIdxs[j-1] > shardIdxs[j] {
			shardIdxs[j-1], shardIdxs[j] = shardIdxs[j], shardIdxs[j-1]
			j--
		}
	}

	// perform batched directory updates with version checks
	updatedKeys := make([]moveMeta, 0, len(moveList))
	for _, si := range shardIdxs {
		c.dirLocks[si].Lock()
		// process keys for this shard
		for _, m := range shardToKeys[si] {
			// verify source still has the same version
			src.mu.RLock()
			e, ok := src.mp[m.key]
			src.mu.RUnlock()
			if !ok {
				// nothing to move; possibly deleted concurrently; skip
				continue
			}
			if e.Version != m.version {
				// modified during migration; skip
				continue
			}
			// update directory to point to new node
			c.dirShards[si][m.key] = m.dstIdx
			updatedKeys = append(updatedKeys, m)
		}
		c.dirLocks[si].Unlock()
	}

	// After directory updated for updatedKeys, delete from source for those keys if still unchanged
	for _, m := range updatedKeys {
		src.mu.Lock()
		if cur, ok := src.mp[m.key]; ok {
			if cur.Version == m.version {
				delete(src.mp, m.key)
				if src.keyCount > 0 {
					src.keyCount--
				}
				c.decCounts(srcIdx)
			}
		}
		src.mu.Unlock()
		// increment dst counts already done when inserting; ensure totalKeys increment
	}
}

