package protocol_test

import (
	"context"
	"testing"
	"time"

	"github.com/erena/scion-libp2p/internal/protocol"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPingRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create two in-memory hosts.
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

	// Register ping handler on h2.
	handler := &protocol.PingHandler{}
	handler.Register(h2)

	// Connect h1 -> h2.
	h2Info := peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}
	if err := h1.Connect(ctx, h2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Send a ping.
	result := protocol.SendPing(ctx, h1, h2.ID())
	if result.Err != nil {
		t.Fatalf("ping failed: %v", result.Err)
	}

	if result.RTT <= 0 {
		t.Errorf("expected positive RTT, got %v", result.RTT)
	}

	if result.RTT > 5*time.Second {
		t.Errorf("RTT too large for localhost: %v", result.RTT)
	}

	t.Logf("ping RTT: %v", result.RTT)
}

func TestSendPingsMultiple(t *testing.T) {
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

	handler := &protocol.PingHandler{}
	handler.Register(h2)

	h2Info := peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}
	if err := h1.Connect(ctx, h2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	results := protocol.SendPings(ctx, h1, h2.ID(), 3)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	for i, r := range results {
		if r.Err != nil {
			t.Errorf("ping %d failed: %v", i, r.Err)
		}
	}
}
