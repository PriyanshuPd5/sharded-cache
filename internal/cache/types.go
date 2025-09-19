package cache

import (
	"hash/fnv"
	"math/rand"
	"runtime"
	"sync"
	"time"
)

// Entry represents a cached value and expiry metadata.
type Entry struct {
	Value    []byte
	ExpireAt time.Time // zero = no expiry
	TTL      int       // TTL in seconds (optional)
	Version  uint64    // monotonic version can be used later
}

// CacheNode stores entries and local metadata. keyCount is maintained atomically.
type CacheNode struct {
	mp       map[string]*Entry
	keyCount int64 // atomic counter
	mu       sync.RWMutex

	// timing wheel fields:
	wheelSize int               // number of slots
	slots     []map[string]struct{} // each slot holds keys expiring at that bucket
	slotMap   map[string]int     // key -> slot index (for quick removal)
	curSlot   int64              // current slot index (mod wheelSize)
	wheelMu   sync.Mutex         // protects slots and slotMap
}

// Cache is the top-level structure holding fixed nodes and a sharded directory.
type Cache struct {
	nodes       []*CacheNode
	nodeCnt     int
	// Sharded directory: maps key -> node index
	dirShardCnt int
	dirShards   []map[string]int
	dirLocks    []sync.RWMutex

	// approximate total number of keys
	totalKeys int64

	// config knobs
	RebalanceUpper float64
	RebalanceBatch int
	WheelSize      int // default timing wheel size in seconds
}

// NewCache constructs a Cache with nodeCount nodes and dirShardCount directory shards.
func NewCache(nodeCount, dirShardCount int) *Cache {
	if nodeCount <= 0 {
		nodeCount = runtime.GOMAXPROCS(0)
		if nodeCount <= 0 {
			nodeCount = 4
		}
	}
	if dirShardCount <= 0 {
		dirShardCount = 64
	}

	c := &Cache{
		nodes:          make([]*CacheNode, nodeCount),
		nodeCnt:        nodeCount,
		dirShardCnt:    dirShardCount,
		dirShards:      make([]map[string]int, dirShardCount),
		dirLocks:       make([]sync.RWMutex, dirShardCount),
		RebalanceUpper: 1.25,
		RebalanceBatch: 1024,
		WheelSize:      3600, // 1 hour wheel by default
	}

	for i := 0; i < nodeCount; i++ {
		// initialize wheel slots for each node
		slots := make([]map[string]struct{}, c.WheelSize)
		for s := 0; s < c.WheelSize; s++ {
			slots[s] = make(map[string]struct{})
		}
		c.nodes[i] = &CacheNode{
			mp:      make(map[string]*Entry),
			wheelSize: c.WheelSize,
			slots:   slots,
			slotMap: make(map[string]int),
		}
	}
	for s := 0; s < dirShardCount; s++ {
		c.dirShards[s] = make(map[string]int)
	}

	rand.New(rand.NewSource(time.Now().UnixNano()))
	return c
}

// hash64 returns a 64-bit hash of the key (fnv-1a).
func hash64(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}
