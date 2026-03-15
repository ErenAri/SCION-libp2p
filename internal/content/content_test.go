package content_test

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"testing"

	"github.com/erena/scion-libp2p/internal/content"
)

func TestChunkSmallFile(t *testing.T) {
	data := []byte("hello, world!")
	r := bytes.NewReader(data)

	blocks, err := content.Chunk(r, 256*1024)
	if err != nil {
		t.Fatalf("chunk error: %v", err)
	}

	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}

	if !bytes.Equal(blocks[0].Data, data) {
		t.Error("block data doesn't match input")
	}

	if blocks[0].CID == "" {
		t.Error("expected non-empty CID")
	}
}

func TestChunkMultipleBlocks(t *testing.T) {
	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	blocks, err := content.Chunk(bytes.NewReader(data), 400)
	if err != nil {
		t.Fatalf("chunk error: %v", err)
	}

	// 1000 / 400 = 2 full + 1 partial = 3 blocks
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}

	if len(blocks[0].Data) != 400 {
		t.Errorf("block 0: expected 400 bytes, got %d", len(blocks[0].Data))
	}
	if len(blocks[2].Data) != 200 {
		t.Errorf("block 2: expected 200 bytes, got %d", len(blocks[2].Data))
	}

	// All CIDs should be unique.
	seen := make(map[string]bool)
	for i, b := range blocks {
		if seen[b.CID] {
			t.Errorf("duplicate CID at block %d", i)
		}
		seen[b.CID] = true
	}
}

func TestComputeCID(t *testing.T) {
	cid1 := content.ComputeCID([]byte("hello"))
	cid2 := content.ComputeCID([]byte("hello"))
	cid3 := content.ComputeCID([]byte("world"))

	if cid1 != cid2 {
		t.Error("same content should produce same CID")
	}
	if cid1 == cid3 {
		t.Error("different content should produce different CID")
	}
}

func TestBuildManifest(t *testing.T) {
	blocks := []content.Block{
		{CID: "aaa", Data: []byte("a")},
		{CID: "bbb", Data: []byte("b")},
	}

	m := content.BuildManifest("test.txt", 2, blocks)

	if m.RootCID == "" {
		t.Error("expected non-empty root CID")
	}
	if m.Name != "test.txt" {
		t.Errorf("expected name 'test.txt', got %q", m.Name)
	}
	if len(m.ChunkCIDs) != 2 {
		t.Errorf("expected 2 chunk CIDs, got %d", len(m.ChunkCIDs))
	}
}

func TestStoreRoundTrip(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "scion-store-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := content.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	block := content.Block{
		CID:  content.ComputeCID([]byte("test data")),
		Data: []byte("test data"),
	}

	// Put.
	if err := store.Put(block); err != nil {
		t.Fatalf("put block: %v", err)
	}

	// Has.
	if !store.Has(block.CID) {
		t.Error("expected block to exist")
	}

	// Get.
	got, err := store.Get(block.CID)
	if err != nil {
		t.Fatalf("get block: %v", err)
	}
	if !bytes.Equal(got.Data, block.Data) {
		t.Error("retrieved block data doesn't match")
	}

	// Delete.
	if err := store.Delete(block.CID); err != nil {
		t.Fatalf("delete block: %v", err)
	}
	if store.Has(block.CID) {
		t.Error("expected block to be deleted")
	}
}

func TestManifestRoundTrip(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "scion-manifest-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := content.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	m := content.Manifest{
		RootCID:   "abc123",
		Name:      "test.txt",
		TotalSize: 1024,
		ChunkCIDs: []string{"aaa", "bbb"},
	}

	if err := store.PutManifest(m); err != nil {
		t.Fatalf("put manifest: %v", err)
	}

	got, err := store.GetManifest("abc123")
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}

	if got.Name != m.Name {
		t.Errorf("expected name %q, got %q", m.Name, got.Name)
	}
	if len(got.ChunkCIDs) != 2 {
		t.Errorf("expected 2 chunk CIDs, got %d", len(got.ChunkCIDs))
	}
}

func TestSignAndVerifyManifest(t *testing.T) {
	blocks := []content.Block{
		{CID: "aaa", Data: []byte("a")},
		{CID: "bbb", Data: []byte("b")},
	}

	m := content.BuildManifest("signed.txt", 2, blocks)

	// Generate a test key pair.
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	_ = pub

	// Sign.
	content.SignManifest(&m, priv)
	if m.PublisherID == "" || m.Signature == "" {
		t.Fatal("expected PublisherID and Signature to be set")
	}

	// Verify.
	if err := content.VerifyManifest(&m); err != nil {
		t.Fatalf("verify should succeed: %v", err)
	}
}

func TestTamperedManifest(t *testing.T) {
	blocks := []content.Block{
		{CID: "ccc", Data: []byte("c")},
	}

	m := content.BuildManifest("tampered.txt", 1, blocks)

	_, priv, _ := ed25519.GenerateKey(nil)
	content.SignManifest(&m, priv)

	// Tamper with the manifest.
	m.Name = "evil.txt"

	if err := content.VerifyManifest(&m); err == nil {
		t.Fatal("verification should fail for tampered manifest")
	}
}

func TestAdaptiveChunkSizeSmallFile(t *testing.T) {
	// File smaller than minChunk → returns file size.
	size := content.AdaptiveChunkSize(32*1024, 10, 64*1024, 1024*1024)
	if size != 32*1024 {
		t.Errorf("expected 32KB for small file, got %d", size)
	}
}

func TestAdaptiveChunkSizeHighRTT(t *testing.T) {
	// High RTT (>50ms) → fewer chunks (4), so larger chunk size.
	fileSize := int64(4 * 1024 * 1024) // 4 MB
	size := content.AdaptiveChunkSize(fileSize, 100, 64*1024, 1024*1024)
	// Target 4 chunks: 4MB / 4 = 1MB, clamped to maxChunk.
	if size > 1024*1024 {
		t.Errorf("expected chunk size <= 1MB, got %d", size)
	}
	if size < 64*1024 {
		t.Errorf("expected chunk size >= 64KB, got %d", size)
	}
}

func TestAdaptiveChunkSizeLowRTT(t *testing.T) {
	// Low RTT (<5ms) → more chunks (16), so smaller chunk size.
	fileSize := int64(4 * 1024 * 1024) // 4 MB
	size := content.AdaptiveChunkSize(fileSize, 1, 64*1024, 1024*1024)
	// Target 16 chunks: 4MB / 16 = 256KB.
	if size > 512*1024 {
		t.Errorf("expected smaller chunk for low RTT, got %d", size)
	}
	if size < 64*1024 {
		t.Errorf("expected chunk size >= minChunk, got %d", size)
	}
}

func TestAdaptiveChunkSizeClampMin(t *testing.T) {
	// Very large file with low RTT → many small chunks, but clamped to min.
	fileSize := int64(1024) // 1KB, but > minChunk is false here
	minChunk := 64 * 1024
	size := content.AdaptiveChunkSize(fileSize, 2, minChunk, 1024*1024)
	// File < minChunk → returns fileSize.
	if size != int(fileSize) {
		t.Errorf("expected %d, got %d", fileSize, size)
	}
}

func TestAdaptiveChunkSizeDefaults(t *testing.T) {
	// Zero min/max should use defaults.
	size := content.AdaptiveChunkSize(2*1024*1024, 10, 0, 0)
	if size <= 0 {
		t.Errorf("expected positive chunk size, got %d", size)
	}
}

func TestFragmentStorage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "scion-frag-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := content.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	parentCID := "parent-abc"
	fragData := []byte("fragment-data-0")

	// Put fragment.
	if err := store.PutFragment(parentCID, 0, fragData); err != nil {
		t.Fatalf("put fragment: %v", err)
	}
	if err := store.PutFragment(parentCID, 1, []byte("fragment-data-1")); err != nil {
		t.Fatalf("put fragment 1: %v", err)
	}

	// Get fragment.
	got, err := store.GetFragment(parentCID, 0)
	if err != nil {
		t.Fatalf("get fragment: %v", err)
	}
	if !bytes.Equal(got, fragData) {
		t.Error("fragment data doesn't match")
	}

	// List fragments.
	indices := store.ListFragments(parentCID)
	if len(indices) != 2 {
		t.Errorf("expected 2 fragments, got %d", len(indices))
	}

	// List for nonexistent parent.
	empty := store.ListFragments("nonexistent")
	if len(empty) != 0 {
		t.Errorf("expected 0 fragments for nonexistent parent, got %d", len(empty))
	}
}

func TestReplicationTracker(t *testing.T) {
	rt := content.NewReplicationTracker(3) // threshold = 3 fetches

	cid := "test-block-cid"

	// First two fetches shouldn't trigger replication.
	if rt.RecordFetch(cid) {
		t.Error("should not trigger replication at count=1")
	}
	if rt.RecordFetch(cid) {
		t.Error("should not trigger replication at count=2")
	}

	// Third fetch should trigger.
	if !rt.RecordFetch(cid) {
		t.Error("should trigger replication at count=3")
	}

	// Fourth fetch should not re-trigger.
	if rt.RecordFetch(cid) {
		t.Error("should not re-trigger at count=4")
	}

	// Check PopularBlocks.
	popular := rt.PopularBlocks()
	if len(popular) != 1 || popular[0] != cid {
		t.Errorf("expected 1 popular block %q, got %v", cid, popular)
	}

	// Check stats.
	stats := rt.Stats()
	if stats.TrackedBlocks != 1 {
		t.Errorf("expected 1 tracked block, got %d", stats.TrackedBlocks)
	}
	if stats.PopularBlocks != 1 {
		t.Errorf("expected 1 popular block, got %d", stats.PopularBlocks)
	}
}

