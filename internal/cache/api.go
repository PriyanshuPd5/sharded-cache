package cache

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sync"
	"sync/atomic"
	"time"
)

var ErrNotFound = errors.New("key not found or expired")

func (n *CacheNode) KeyCount() int64 {
    return atomic.LoadInt64(&n.keyCount)
}

func (c *Cache) Nodes() []*CacheNode {
    return c.nodes
}

// Set stores key->value with optional ttl. Returns previous value copy and true if replaced.
func (c *Cache) Set(key string, val []byte, ttl time.Duration) ([]byte, bool) {
    store := cloneBytes(val)
    exp := time.Time{}
    if ttl > 0 {
        exp = time.Now().Add(ttl)
    }

    // Try directory lookup first
    if idx, ok := c.dirGet(key); ok {
        return c.setInNode(idx, key, store, exp, ttl)
    }

    // Not in directory: pick node via PoTC
    idx := c.pickNodePoTC()
    return c.setInNode(idx, key, store, exp, ttl)
}

// setInNode handles setting a key in a specific node.
func (c *Cache) setInNode(idx int, key string, store []byte, exp time.Time, ttl time.Duration) ([]byte, bool) {
    node := c.nodes[idx]
    node.mu.Lock()
    defer node.mu.Unlock()

    old, exists := node.mp[key]
    if exists {
        // If expired, replace
        if !old.ExpireAt.IsZero() && time.Now().After(old.ExpireAt) {
            prev := cloneBytes(old.Value)
            node.mp[key] = &Entry{Value: store, ExpireAt: exp, TTL: int(ttl.Seconds()), Version: 1}
            c.updateWheel(idx, key, exp)
            c.dirPut(key, idx)
            c.incCounts(idx)
            return prev, true
        }
        // Update in-place
        prev := cloneBytes(old.Value)
        old.Value = store
        old.ExpireAt = exp
        old.Version++
        c.updateWheel(idx, key, exp)
        c.dirPut(key, idx)
        return prev, true
    }

    // Insert new
    node.mp[key] = &Entry{Value: store, ExpireAt: exp, TTL: int(ttl.Seconds()), Version: 1}
    node.keyCount++
    c.updateWheel(idx, key, exp)
    c.dirPut(key, idx)
    c.incCounts(idx)
    return nil, false
}

// updateWheel adds or removes key from timing wheel based on expiry.
func (c *Cache) updateWheel(idx int, key string, exp time.Time) {
    if !exp.IsZero() {
        c.addToWheel(idx, key, exp)
    } else {
        c.removeFromWheel(idx, key)
    }
}

// cloneBytes safely copies a byte slice.
func cloneBytes(src []byte) []byte {
    if src == nil {
        return nil
    }
    dst := make([]byte, len(src))
    copy(dst, src)
    return dst
}

// Get reads by directory -> node. Returns a copy of value.
func (c *Cache) Get(key string) ([]byte, error) {
    if idx, ok := c.dirGet(key); ok {
        node := c.nodes[idx]
        node.mu.RLock()
        entry, exists := node.mp[key]
        node.mu.RUnlock()
        if !exists {
            c.dirDelete(key)
            return nil, ErrNotFound
        }
        if isExpired(entry) {
            c.deleteExpired(idx, key)
            return nil, ErrNotFound
        }
        return cloneBytes(entry.Value), nil
    }

    // Directory miss: fallback scan 
    type result struct {
        value []byte
        idx   int
    }
    resCh := make(chan result, 1)
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
    defer cancel()

    for nodeIdx, node := range c.nodes {
        go func(nodeIdx int, node *CacheNode) {
            node.mu.RLock()
            entry, exists := node.mp[key]
            node.mu.RUnlock()
            if exists && !isExpired(entry) {
                select {
					case resCh <- result{cloneBytes(entry.Value), nodeIdx}:
					case <-ctx.Done():
                }
            }
        }(nodeIdx, node)
	}

    select {
		case res := <-resCh:
			c.dirPut(key, res.idx)
			return res.value, nil
		case <-ctx.Done():
			return nil, ErrNotFound
    }
}

// isExpired checks if an entry is expired.
func isExpired(e *Entry) bool {
    return !e.ExpireAt.IsZero() && time.Now().After(e.ExpireAt)
}

// deleteExpired removes expired key from node and directory.
func (c *Cache) deleteExpired(idx int, key string) {
    node := c.nodes[idx]
    node.mu.Lock()
    if e, ok := node.mp[key]; ok && isExpired(e) {
        delete(node.mp, key)
        if node.keyCount > 0 {
            node.keyCount--
        }
        c.removeFromWheel(idx, key)
    }
    node.mu.Unlock()
    c.dirDelete(key)
}

// Delete removes key via directory. Returns true if removed.
func (c *Cache) Delete(key string) (bool, error) {
    if idx, ok := c.dirGet(key); ok {
        node := c.nodes[idx]
        node.mu.Lock()
        entry, exists := node.mp[key]
        node.mu.Unlock()
        if !exists {
            c.dirDelete(key)
            return false, ErrNotFound
        }
        if isExpired(entry) {
            c.deleteExpired(idx, key)
            return false, ErrNotFound
        }
        node.mu.Lock()
        delete(node.mp, key)
        if node.keyCount > 0 {
            node.keyCount--
        }
        node.mu.Unlock()
        c.dirDelete(key) 
        c.decCounts(idx)
        c.removeFromWheel(idx, key)
        return true, nil
    }
    return false, ErrNotFound
}

func (c *Cache) FlushAll() {
    // Clear nodes concurrently
    var wg sync.WaitGroup
    for i := 0; i < c.nodeCnt; i++ {
        wg.Add(1)
        go func(node *CacheNode) {
            defer wg.Done()
            node.mu.Lock()
            node.mp = make(map[string]*Entry)
            atomic.StoreInt64(&node.keyCount, 0)
            node.wheelMu.Lock()
            for s := 0; s < node.wheelSize; s++ {
                for k := range node.slots[s] {
                    delete(node.slots[s], k)
                }
            }
            node.slotMap = make(map[string]int)
            node.wheelMu.Unlock()
            node.mu.Unlock()
        }(c.nodes[i])
    }
    wg.Wait()

    // Clear directory shards (can also be done concurrently if needed)
    for si := 0; si < c.dirShardCnt; si++ {
        c.dirLocks[si].Lock()
        for k := range c.dirShards[si] {
            delete(c.dirShards[si], k)
        }
        c.dirLocks[si].Unlock()
    }

    // Reset total keys
    atomic.StoreInt64(&c.totalKeys, 0)
}

func (c *Cache) Keys(pattern string) ([]string, error) {
    outCh := make(chan []string, c.dirShardCnt)
    errCh := make(chan error, 1)
    var wg sync.WaitGroup

    for si := 0; si < c.dirShardCnt; si++ {
        wg.Add(1)
        go func(si int) {
            defer wg.Done()
            matches := make([]string, 0, 32)
            c.dirLocks[si].RLock()
            defer c.dirLocks[si].RUnlock()
            for k := range c.dirShards[si] {
                matched, err := path.Match(pattern, k)
                if err != nil {
                    // send error and return immediately
                    select {
                    case errCh <- fmt.Errorf("invalid pattern: %w", err):
                    default:
                    }
                    return
                }
                if matched {
                    matches = append(matches, k)
                }
            }
            outCh <- matches
        }(si)
    }

    // Wait for all goroutines
    go func() {
        wg.Wait()
        close(outCh)
    }()

    // Collect results
    out := make([]string, 0, 128)
    for matches := range outCh {
        out = append(out, matches...)
    }

    // Check for error
    select {
    case err := <-errCh:
        return nil, err
    default:
    }

    return out, nil
}


func makeCursor(shard int, offset uint32) uint64 {
    return (uint64(shard) << 32) | uint64(offset)
}

func decodeCursor(cursor uint64) (shard int, offset uint32) {
    if cursor == 0 {
        return 0, 0
    }
    shard = int(cursor >> 32)
    offset = uint32(cursor & 0xffffffff)
    return
}

// Scan returns (nextCursor, keys). Use nextCursor==0 to indicate iteration is complete.
// match can be empty ("" meaning match all). count is a hint of how many keys to return.
func (c *Cache) Scan(cursor uint64, match string, count int) (uint64, []string, error) {
    if count <= 0 {
        count = 10 // default hint
    }
    startShard, startOffset := decodeCursor(cursor)
    if cursor == 0 {
        startShard, startOffset = 0, 0
    }

    collected := make([]string, 0, count)
    si := startShard
    offset := startOffset

    for scannedShards := 0; scannedShards < c.dirShardCnt && len(collected) < count; scannedShards++ {
        if si >= c.dirShardCnt {
            si -= c.dirShardCnt
        }

        keys, nextOffset, err := c.scanShard(si, offset, match, count-len(collected))
        if err != nil {
            return 0, nil, err
        }
        collected = append(collected, keys...)

        if len(collected) >= count {
            next := makeCursor(si, nextOffset)
            return next, collected, nil
        }

        si++
        offset = 0
    }

    return 0, collected, nil
}

// scanShard scans a single shard for up to maxCount matching keys, starting at offset.
func (c *Cache) scanShard(si int, offset uint32, match string, maxCount int) ([]string, uint32, error) {
    c.dirLocks[si].RLock()
    defer c.dirLocks[si].RUnlock()

    keys := make([]string, 0, maxCount)
    i := uint32(0)
    for k := range c.dirShards[si] {
        if i < offset {
            i++
            continue
        }
        if match != "" {
            ok, err := path.Match(match, k)
            if err != nil {
                return nil, 0, fmt.Errorf("invalid match pattern: %w", err)
            }
            if !ok {
                i++
                continue
            }
        }
        keys = append(keys, k)
        i++
        if len(keys) >= maxCount {
            break
        }
    }
    return keys, i, nil
}