package cache

import (
	"container/list"
	"sync"
	"sync/atomic"

	"github.com/erena/scion-libp2p/internal/content"
)

// PinChecker is an interface for checking if a CID is pinned.
type PinChecker interface {
	IsPinned(cid string) bool
}

// MetricsHook allows wiring cache events to external metrics (e.g., Prometheus).
type MetricsHook struct {
	OnHit  func()
	OnMiss func()
}

// LRUCache is a thread-safe, byte-size-limited LRU cache for content blocks.
type LRUCache struct {
	maxBytes int64

	mu       sync.Mutex
	items    map[string]*list.Element
	eviction *list.List // front = most recently used

	currentBytes int64
	hits         atomic.Int64
	misses       atomic.Int64

	pinChecker  PinChecker  // optional: if set, pinned blocks are never evicted
	metricsHook MetricsHook // optional: called on hit/miss for Prometheus integration
}

type cacheEntry struct {
	key        string
	block      content.Block
	size       int64
	fetchCount int64 // number of times this block has been accessed
}

// NewLRUCache creates an LRU cache with the given maximum size in bytes.
func NewLRUCache(maxBytes int64) *LRUCache {
	return &LRUCache{
		maxBytes: maxBytes,
		items:    make(map[string]*list.Element),
		eviction: list.New(),
	}
}

// SetPinChecker sets a pin checker so that pinned blocks are never evicted.
func (c *LRUCache) SetPinChecker(pc PinChecker) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pinChecker = pc
}

// SetMetricsHook sets callbacks for cache hit/miss events (e.g., Prometheus counters).
func (c *LRUCache) SetMetricsHook(hook MetricsHook) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metricsHook = hook
}

// Get retrieves a block from the cache. Returns the block and true if found.
func (c *LRUCache) Get(cid string) (content.Block, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[cid]; ok {
		entry := elem.Value.(*cacheEntry)
		entry.fetchCount++
		c.eviction.MoveToFront(elem)
		c.hits.Add(1)
		if c.metricsHook.OnHit != nil {
			c.metricsHook.OnHit()
		}
		return entry.block, true
	}

	c.misses.Add(1)
	if c.metricsHook.OnMiss != nil {
		c.metricsHook.OnMiss()
	}
	return content.Block{}, false
}

// Put adds a block to the cache, evicting old entries if needed.
func (c *LRUCache) Put(b content.Block) {
	c.mu.Lock()
	defer c.mu.Unlock()

	blockSize := int64(len(b.Data))

	// If already cached, update and move to front.
	if elem, ok := c.items[b.CID]; ok {
		old := elem.Value.(*cacheEntry)
		c.currentBytes -= old.size
		old.block = b
		old.size = blockSize
		c.currentBytes += blockSize
		c.eviction.MoveToFront(elem)
		return
	}

	// Evict until there's room.
	for c.currentBytes+blockSize > c.maxBytes && c.eviction.Len() > 0 {
		c.evictOldest()
	}

	entry := &cacheEntry{key: b.CID, block: b, size: blockSize}
	elem := c.eviction.PushFront(entry)
	c.items[b.CID] = elem
	c.currentBytes += blockSize
}

// Evict removes a specific block from the cache.
func (c *LRUCache) Evict(cid string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[cid]; ok {
		c.removeElement(elem)
	}
}

// evictOldest removes the least valuable entry from the cache.
// Instead of pure LRU, this uses a popularity-aware strategy: it scans
// from the back (LRU end) and skips entries that have high fetch counts,
// giving them a second chance. This prevents popular blocks from being
// evicted prematurely (a known LRU weakness in P2P caching, per
// Kangasharju et al.'s adaptive P2P caching research).
func (c *LRUCache) evictOldest() {
	const popularityThreshold int64 = 3

	// Scan from back, looking for a low-popularity victim.
	candidate := c.eviction.Back()
	checked := 0
	maxCheck := c.eviction.Len() / 2 // don't scan more than half the list
	if maxCheck < 1 {
		maxCheck = 1
	}

	for candidate != nil && checked < maxCheck {
		entry := candidate.Value.(*cacheEntry)
		// Never evict pinned blocks.
		if c.pinChecker != nil && c.pinChecker.IsPinned(entry.key) {
			candidate = candidate.Prev()
			checked++
			continue
		}
		if entry.fetchCount < popularityThreshold {
			c.removeElement(candidate)
			return
		}
		// Popular entry gets a second chance: halve its count and skip.
		entry.fetchCount /= 2
		candidate = candidate.Prev()
		checked++
	}

	// Fallback: if all scanned entries are popular, evict the true LRU
	// (but still skip pinned blocks).
	candidate = c.eviction.Back()
	for candidate != nil {
		entry := candidate.Value.(*cacheEntry)
		if c.pinChecker != nil && c.pinChecker.IsPinned(entry.key) {
			candidate = candidate.Prev()
			continue
		}
		c.removeElement(candidate)
		return
	}
}

func (c *LRUCache) removeElement(elem *list.Element) {
	entry := c.eviction.Remove(elem).(*cacheEntry)
	delete(c.items, entry.key)
	c.currentBytes -= entry.size
}

// Stats returns cache statistics.
type Stats struct {
	Hits         int64 `json:"hits"`
	Misses       int64 `json:"misses"`
	Entries      int   `json:"entries"`
	CurrentBytes int64 `json:"current_bytes"`
	MaxBytes     int64 `json:"max_bytes"`
}

// Stats returns current cache statistics.
func (c *LRUCache) Stats() Stats {
	c.mu.Lock()
	entries := c.eviction.Len()
	currentBytes := c.currentBytes
	c.mu.Unlock()

	return Stats{
		Hits:         c.hits.Load(),
		Misses:       c.misses.Load(),
		Entries:      entries,
		CurrentBytes: currentBytes,
		MaxBytes:     c.maxBytes,
	}
}

// Len returns the number of cached entries.
func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.eviction.Len()
}
