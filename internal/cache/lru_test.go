package cache_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/erena/scion-libp2p/internal/cache"
	"github.com/erena/scion-libp2p/internal/content"
)

func TestPutGet(t *testing.T) {
	c := cache.NewLRUCache(1024)

	block := content.Block{
		CID:  "test-cid",
		Data: []byte("hello, world"),
	}

	c.Put(block)

	got, ok := c.Get("test-cid")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(got.Data) != "hello, world" {
		t.Errorf("expected 'hello, world', got %q", string(got.Data))
	}
}

func TestCacheMiss(t *testing.T) {
	c := cache.NewLRUCache(1024)

	_, ok := c.Get("nonexistent")
	if ok {
		t.Error("expected cache miss")
	}
}

func TestEviction(t *testing.T) {
	// Cache can hold 20 bytes max.
	c := cache.NewLRUCache(20)

	// Add 3 blocks of 10 bytes each. Only 2 should remain.
	for i := 0; i < 3; i++ {
		c.Put(content.Block{
			CID:  fmt.Sprintf("cid-%d", i),
			Data: make([]byte, 10),
		})
	}

	if c.Len() != 2 {
		t.Errorf("expected 2 entries after eviction, got %d", c.Len())
	}

	// Oldest (cid-0) should be evicted.
	_, ok := c.Get("cid-0")
	if ok {
		t.Error("expected cid-0 to be evicted")
	}

	// Newest should still be present.
	_, ok = c.Get("cid-2")
	if !ok {
		t.Error("expected cid-2 to be cached")
	}
}

func TestLRUOrder(t *testing.T) {
	c := cache.NewLRUCache(30) // room for 3 blocks of 10 bytes

	for i := 0; i < 3; i++ {
		c.Put(content.Block{
			CID:  fmt.Sprintf("cid-%d", i),
			Data: make([]byte, 10),
		})
	}

	// Access cid-0 to make it recently used.
	c.Get("cid-0")

	// Add another block — should evict cid-1 (least recently used), not cid-0.
	c.Put(content.Block{CID: "cid-3", Data: make([]byte, 10)})

	_, ok := c.Get("cid-0")
	if !ok {
		t.Error("cid-0 should survive eviction (recently accessed)")
	}

	_, ok = c.Get("cid-1")
	if ok {
		t.Error("cid-1 should have been evicted (least recently used)")
	}
}

func TestExplicitEvict(t *testing.T) {
	c := cache.NewLRUCache(1024)

	c.Put(content.Block{CID: "a", Data: []byte("data")})
	c.Evict("a")

	_, ok := c.Get("a")
	if ok {
		t.Error("expected explicit eviction to remove entry")
	}
}

func TestStats(t *testing.T) {
	c := cache.NewLRUCache(1024)

	c.Put(content.Block{CID: "x", Data: []byte("12345")})
	c.Get("x")       // hit
	c.Get("missing") // miss

	stats := c.Stats()
	if stats.Hits != 1 {
		t.Errorf("expected 1 hit, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}
	if stats.Entries != 1 {
		t.Errorf("expected 1 entry, got %d", stats.Entries)
	}
	if stats.CurrentBytes != 5 {
		t.Errorf("expected 5 bytes, got %d", stats.CurrentBytes)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := cache.NewLRUCache(1024 * 1024)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			block := content.Block{
				CID:  fmt.Sprintf("cid-%d", i),
				Data: []byte(fmt.Sprintf("data-%d", i)),
			}
			c.Put(block)
			c.Get(block.CID)
		}(i)
	}
	wg.Wait()

	// Should not panic; just verify it completes.
	stats := c.Stats()
	if stats.Entries == 0 {
		t.Error("expected some entries after concurrent access")
	}
}
