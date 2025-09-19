package cache

import (
	"math/rand"
	"sync/atomic"
)

// pickNodePoTC samples two nodes and returns the index of the one with smaller load.
func (c *Cache) pickNodePoTC() int {
	if c.nodeCnt == 1 {
		return 0
	}
	i := rand.Intn(c.nodeCnt)
	j := rand.Intn(c.nodeCnt - 1)
	if j >= i {
		j++
	}
	ci := atomic.LoadInt64(&c.nodes[i].keyCount)
	cj := atomic.LoadInt64(&c.nodes[j].keyCount)
	if ci <= cj {
		return i
	}
	return j
}

// inc/dec helpers
func (c *Cache) incCounts(nodeIdx int) {
	atomic.AddInt64(&c.totalKeys, 1)
	atomic.AddInt64(&c.nodes[nodeIdx].keyCount, 1)
}

func (c *Cache) decCounts(nodeIdx int) {
	atomic.AddInt64(&c.totalKeys, -1)
	if atomic.LoadInt64(&c.nodes[nodeIdx].keyCount) > 0 {
		atomic.AddInt64(&c.nodes[nodeIdx].keyCount, -1)
	}
}
