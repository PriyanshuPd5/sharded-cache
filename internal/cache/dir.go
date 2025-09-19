package cache

// directory helpers: sharded map key -> nodeIdx

func (c *Cache) dirShardIndex(key string) int {
	return int(hash64(key) % uint64(c.dirShardCnt))
}

func (c *Cache) dirGet(key string) (int, bool) {
	si := c.dirShardIndex(key)
	c.dirLocks[si].RLock()
	idx, ok := c.dirShards[si][key]
	c.dirLocks[si].RUnlock()
	return idx, ok
}

func (c *Cache) dirPut(key string, nodeIdx int) {
	si := c.dirShardIndex(key)
	c.dirLocks[si].Lock()
	c.dirShards[si][key] = nodeIdx
	c.dirLocks[si].Unlock()
}

func (c *Cache) dirDelete(key string) {
	si := c.dirShardIndex(key)
	c.dirLocks[si].Lock()
	delete(c.dirShards[si], key)
	c.dirLocks[si].Unlock()
}
