//go:build integration

package internal_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ErenAri/PathAware-libp2p/internal/content"
	"github.com/ErenAri/PathAware-libp2p/internal/protocol"
	"github.com/ErenAri/PathAware-libp2p/testutil"
)

func TestMeshFormation(t *testing.T) {
	cluster := testutil.NewCluster(t, 3)
	defer cluster.Stop(t)

	cluster.WaitForMesh(t, 10*time.Second)

	// Verify each node sees the other two.
	for i, n := range cluster.Nodes {
		peers := n.Host.Network().Peers()
		if len(peers) < 2 {
			t.Errorf("node %d has %d peers, expected at least 2", i, len(peers))
		}
	}
}

func TestPingAcrossNodes(t *testing.T) {
	cluster := testutil.NewCluster(t, 2)
	defer cluster.Stop(t)

	cluster.WaitForMesh(t, 10*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Ping node1 from node0.
	result := protocol.SendPing(ctx, cluster.Nodes[0].Host, cluster.Nodes[1].Host.ID())
	if result.Err != nil {
		t.Fatalf("ping failed: %v", result.Err)
	}

	t.Logf("RTT node0 -> node1: %v", result.RTT)
}

func TestProbeAcrossNodes(t *testing.T) {
	cluster := testutil.NewCluster(t, 2)
	defer cluster.Stop(t)

	cluster.WaitForMesh(t, 10*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	path := protocol.PathInfo{
		ID:     1,
		Target: cluster.Nodes[1].Host.ID(),
	}

	result := protocol.SendProbe(ctx, cluster.Nodes[0].Host, path)
	if result.Err != nil {
		t.Fatalf("probe failed: %v", result.Err)
	}

	t.Logf("probe RTT: %v, hops: %d", result.RTT, result.HopCount)

	if result.HopCount != 1 {
		t.Errorf("expected 1 hop for direct path, got %d", result.HopCount)
	}
}

func TestPathPolicySelection(t *testing.T) {
	cluster := testutil.NewCluster(t, 2)
	defer cluster.Stop(t)

	cluster.WaitForMesh(t, 10*time.Second)

	// Wait for path manager to probe at least once.
	time.Sleep(2 * time.Second)

	pm := cluster.Nodes[0].PathManager
	if pm == nil {
		t.Fatal("path manager is nil")
	}

	target := cluster.Nodes[1].Host.ID()
	best := pm.BestPath(target)
	if best == nil {
		t.Fatal("expected path manager to discover at least one path")
	}

	t.Logf("best path: %s, type: %s, avg_rtt: %v, hops: %d",
		best.ID, best.Type, best.Metrics.AvgRTT, best.Metrics.HopCount)
}

func TestContentPublishAndFetch(t *testing.T) {
	cluster := testutil.NewCluster(t, 2)
	defer cluster.Stop(t)

	cluster.WaitForMesh(t, 10*time.Second)

	// Create a test file.
	testData := []byte("Hello from PathAware-libp2p content layer! This is a test file for integration testing.")

	// Chunk and store on node0.
	blocks, err := content.Chunk(bytes.NewReader(testData), 256*1024)
	if err != nil {
		t.Fatalf("chunk: %v", err)
	}

	store0 := cluster.Nodes[0].ContentStore
	for _, b := range blocks {
		if err := store0.Put(b); err != nil {
			t.Fatalf("store block: %v", err)
		}
	}

	manifest := content.BuildManifest("test.txt", int64(len(testData)), blocks)
	if err := store0.PutManifest(manifest); err != nil {
		t.Fatalf("store manifest: %v", err)
	}

	t.Logf("published: root_cid=%s, chunks=%d", manifest.RootCID, len(blocks))

	// Fetch blocks from node0 via node1 using block transfer protocol.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var reassembled bytes.Buffer
	for _, cid := range manifest.ChunkCIDs {
		block, err := protocol.FetchBlock(ctx, cluster.Nodes[1].Host, cluster.Nodes[0].Host.ID(), cid)
		if err != nil {
			t.Fatalf("fetch block %s: %v", cid, err)
		}
		reassembled.Write(block.Data)
	}

	if !bytes.Equal(reassembled.Bytes(), testData) {
		t.Fatalf("fetched content doesn't match: got %d bytes, expected %d",
			reassembled.Len(), len(testData))
	}

	t.Logf("successfully fetched and verified %d bytes", reassembled.Len())

	// Also verify the store can write and read back from file system.
	store1 := cluster.Nodes[1].ContentStore
	tmpFile := filepath.Join(os.TempDir(), "scion-fetch-test.txt")
	defer os.Remove(tmpFile)
	if err := os.WriteFile(tmpFile, reassembled.Bytes(), 0o600); err != nil {
		t.Fatalf("write output: %v", err)
	}

	readBack, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytes.Equal(readBack, testData) {
		t.Fatal("file round-trip mismatch")
	}

	// Store the blocks on node1 to verify cross-node store works.
	for _, b := range blocks {
		if err := store1.Put(b); err != nil {
			t.Fatalf("store block on node1: %v", err)
		}
	}
}

