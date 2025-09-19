package cache

import (
	"time"
	"sync/atomic"
)

// StartExpirySweeper replaced: start timing wheel for expiry management.
// Use the new startWheel which processes expiry slots per-second.
func (c *Cache) StartExpirySweeper(tick time.Duration, stopCh <-chan struct{}) {
	// start the timing wheel
	c.startWheel(tick, stopCh)
}

// expireSweepOnce retained for compatibility: run an aggressive sweep (rarely needed).
func (c *Cache) expireSweepOnce() {
	now := time.Now()
	for idx := 0; idx < c.nodeCnt; idx++ {
		node := c.nodes[idx]
		// acquire write lock to scan and delete (fallback)
		node.mu.Lock()
		for k, e := range node.mp {
			if !e.ExpireAt.IsZero() && now.After(e.ExpireAt) {
				delete(node.mp, k)
				if node.keyCount > 0 {
					node.keyCount--
				}
				c.dirDelete(k)
				// update global count
				atomic.AddInt64(&c.totalKeys, -1)
			}
		}
		node.mu.Unlock()
	}
}
