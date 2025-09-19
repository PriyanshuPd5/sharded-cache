package cache

import (
	"sync/atomic"

	"time"
)

// addToWheel schedules key in node's timing wheel for the expireAt time.
// It stores the key in the slot and records slotMap for quick removal.
func (c *Cache) addToWheel(nodeIdx int, key string, expireAt time.Time) {
	if nodeIdx < 0 || nodeIdx >= c.nodeCnt {
		return
	}
	n := c.nodes[nodeIdx]
	// compute delay in seconds
	dur := int(time.Until(expireAt).Seconds())
	if dur < 0 {
		dur = 0
	}
	sz := n.wheelSize
	slot := int((int(n.curSlot) + dur) % sz)

	n.wheelMu.Lock()
	defer n.wheelMu.Unlock()
	// remove previous slot if exists
	if prev, ok := n.slotMap[key]; ok {
		delete(n.slots[prev], key)
	}
	// add to new slot
	n.slots[slot][key] = struct{}{}
	n.slotMap[key] = slot
}

// removeFromWheel removes key from node's timing wheel if present.
func (c *Cache) removeFromWheel(nodeIdx int, key string) {
	if nodeIdx < 0 || nodeIdx >= c.nodeCnt {
		return
	}
	n := c.nodes[nodeIdx]
	n.wheelMu.Lock()
	defer n.wheelMu.Unlock()
	if prev, ok := n.slotMap[key]; ok {
		delete(n.slots[prev], key)
		delete(n.slotMap, key)
	}
}

// startWheel launches a goroutine ticking every second to process wheel slots for all nodes.
// It returns a stop function to cancel the goroutine.
func (c *Cache) startWheel(tick time.Duration, stopCh <-chan struct{}) {
	if tick <= 0 {
		tick = time.Second
	}
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.processTick()
			case <-stopCh:
				return
			}
		}
	}()
}

// processTick advances the wheel for each node and evicts keys in the current slot.
func (c *Cache) processTick() {
	now := time.Now()
	for idx := 0; idx < c.nodeCnt; idx++ {
		n := c.nodes[idx]
		n.wheelMu.Lock()
		slotIdx := int(n.curSlot % int64(n.wheelSize))
		keys := make([]string, 0, len(n.slots[slotIdx]))
		for k := range n.slots[slotIdx] {
			keys = append(keys, k)
		}
		// clear slot and slotMap entries
		for _, k := range keys {
			delete(n.slots[slotIdx], k)
			delete(n.slotMap, k)
		}
		n.curSlot = (n.curSlot + 1) % int64(n.wheelSize)
		n.wheelMu.Unlock()

		// for each key, verify expiry and remove from node.mp if expired
		for _, k := range keys {
			n.mu.Lock()
			e, ok := n.mp[k]
			if ok {
				if !e.ExpireAt.IsZero() && now.After(e.ExpireAt) {
					delete(n.mp, k)
					if n.keyCount > 0 {
						n.keyCount--
					}
					c.dirDelete(k)
					// update global count
					// use atomic to be safe
					// Note: decCounts uses atomic.Add
					atomic.AddInt64(&c.totalKeys, -1)
				}
			}
			n.mu.Unlock()
		}
	}
}
