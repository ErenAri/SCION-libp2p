package protocol_test

import (
	"context"
	"testing"
	"time"

	"github.com/ErenAri/PathAware-libp2p/internal/protocol"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestProbeDirectPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host1: %v", err)
	}
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host2: %v", err)
	}
	defer h2.Close()

	// Register probe handler on h2.
	handler := &protocol.ProbeHandler{}
	handler.Register(h2)

	// Connect h1 -> h2.
	h2Info := peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}
	if err := h1.Connect(ctx, h2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	path := protocol.PathInfo{
		ID:     1,
		Target: h2.ID(),
	}

	result := protocol.SendProbe(ctx, h1, path)
	if result.Err != nil {
		t.Fatalf("probe failed: %v", result.Err)
	}

	if result.RTT <= 0 {
		t.Errorf("expected positive RTT, got %v", result.RTT)
	}

	if result.RTT > 5*time.Second {
		t.Errorf("RTT too large for localhost: %v", result.RTT)
	}

	if result.HopCount != 1 {
		t.Errorf("expected hop count 1 (direct with one handler), got %d", result.HopCount)
	}

	if result.PathID != 1 {
		t.Errorf("expected path ID 1, got %d", result.PathID)
	}

	t.Logf("probe RTT: %v, hops: %d", result.RTT, result.HopCount)
}

func TestProbeMultiplePaths(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host1: %v", err)
	}
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host2: %v", err)
	}
	defer h2.Close()

	handler := &protocol.ProbeHandler{}
	handler.Register(h2)

	h2Info := peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}
	if err := h1.Connect(ctx, h2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	paths := []protocol.PathInfo{
		{ID: 1, Target: h2.ID()},
		{ID: 2, Target: h2.ID()},
		{ID: 3, Target: h2.ID()},
	}

	results := protocol.SendProbes(ctx, h1, paths)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	for i, r := range results {
		if r.Err != nil {
			t.Errorf("probe %d failed: %v", i, r.Err)
		}
		if r.PathID != paths[i].ID {
			t.Errorf("probe %d: expected path ID %d, got %d", i, paths[i].ID, r.PathID)
		}
	}
}

func TestProbeTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host1: %v", err)
	}
	defer h1.Close()

	// Generate a fake unreachable peer ID.
	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host2: %v", err)
	}
	unreachableID := h2.ID()
	h2.Close() // close immediately so the peer is unreachable

	shortCtx, shortCancel := context.WithTimeout(ctx, 2*time.Second)
	defer shortCancel()

	path := protocol.PathInfo{
		ID:     99,
		Target: unreachableID,
	}

	result := protocol.SendProbe(shortCtx, h1, path)
	if result.Err == nil {
		t.Fatal("expected error for unreachable peer, got nil")
	}

	t.Logf("expected error: %v", result.Err)
}
