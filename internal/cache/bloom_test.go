package cache_test

import (
	"fmt"
	"testing"

	"github.com/erena/scion-libp2p/internal/cache"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/crypto"
)

func TestBloomFilterAddContains(t *testing.T) {
	bf := cache.NewBloomFilter(1000, 0.01)

	items := make([]string, 100)
	for i := range items {
		items[i] = fmt.Sprintf("cid-%d", i)
		bf.Add(items[i])
	}

	// All inserted items must be found (no false negatives).
	for _, item := range items {
		if !bf.Contains(item) {
			t.Errorf("expected Contains(%q) = true, got false", item)
		}
	}

	// Count false positives among items we never added.
	fp := 0
	fpTrials := 10000
	for i := 0; i < fpTrials; i++ {
		key := fmt.Sprintf("absent-%d", i)
		if bf.Contains(key) {
			fp++
		}
	}

	fpRate := float64(fp) / float64(fpTrials)
	// Configured for 1% FP rate; allow up to 3% to account for statistical variance.
	if fpRate > 0.03 {
		t.Errorf("false positive rate %.2f%% exceeds 3%% threshold", fpRate*100)
	}
	t.Logf("false positive rate: %.2f%% (%d/%d)", fpRate*100, fp, fpTrials)
}

func TestBloomFilterSerialization(t *testing.T) {
	bf := cache.NewBloomFilter(500, 0.01)

	keys := []string{"alpha", "beta", "gamma", "delta"}
	for _, k := range keys {
		bf.Add(k)
	}

	// Serialize and deserialize.
	data := bf.Bytes()
	bf2 := cache.BloomFromBytes(data)
	if bf2 == nil {
		t.Fatal("BloomFromBytes returned nil")
	}

	// All keys should still be present after round-trip.
	for _, k := range keys {
		if !bf2.Contains(k) {
			t.Errorf("after round-trip, expected Contains(%q) = true", k)
		}
	}

	// A key we never added should (most likely) not be present.
	if bf2.Contains("never-added-key-xyz") {
		t.Log("false positive on 'never-added-key-xyz' (possible but unlikely)")
	}
}

func TestBloomFromBytesInvalid(t *testing.T) {
	// Too short.
	if bf := cache.BloomFromBytes([]byte{1, 2, 3}); bf != nil {
		t.Error("expected nil for short data")
	}

	// Nil.
	if bf := cache.BloomFromBytes(nil); bf != nil {
		t.Error("expected nil for nil data")
	}
}

func TestBloomFilterEmpty(t *testing.T) {
	bf := cache.NewBloomFilter(100, 0.01)

	// Empty filter should not contain anything.
	if bf.Contains("anything") {
		t.Error("empty bloom filter should not contain any key")
	}
}

func TestBloomFilterDefaults(t *testing.T) {
	// Zero/negative values should use defaults.
	bf := cache.NewBloomFilter(0, 0)
	bf.Add("test")
	if !bf.Contains("test") {
		t.Error("expected Contains('test') = true with default params")
	}
}

func TestPeerBloomStore(t *testing.T) {
	store := cache.NewPeerBloomStore()

	// Generate a test peer ID.
	priv, _, _ := crypto.GenerateEd25519Key(nil)
	pid, _ := peer.IDFromPrivateKey(priv)

	// No filter yet — should return false.
	if store.PeerMayHave(pid, "cid-1") {
		t.Error("expected false when peer has no bloom filter")
	}

	// Set a filter with one item.
	bf := cache.NewBloomFilter(100, 0.01)
	bf.Add("cid-1")
	store.Set(pid, bf)

	if !store.PeerMayHave(pid, "cid-1") {
		t.Error("expected true for cid-1 in peer's bloom filter")
	}
	if store.PeerMayHave(pid, "cid-99") {
		t.Log("false positive for cid-99 (possible but unlikely)")
	}

	// Remove filter.
	store.Remove(pid)
	if store.PeerMayHave(pid, "cid-1") {
		t.Error("expected false after removing peer's bloom filter")
	}
}
