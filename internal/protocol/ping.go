package protocol

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// PingHandler handles incoming ping requests by echoing back the payload.
type PingHandler struct{}

// Register sets up the ping stream handler on the host.
func (h *PingHandler) Register(host host.Host) {
	host.SetStreamHandler(PingID, h.handleStream)
	slog.Debug("registered ping protocol handler")
}

func (h *PingHandler) handleStream(s network.Stream) {
	defer s.Close()

	// Read 8 bytes (timestamp nanos).
	var ts int64
	if err := binary.Read(s, binary.BigEndian, &ts); err != nil {
		if err != io.EOF {
			slog.Debug("ping handler read error", "err", err)
		}
		return
	}

	// Echo back the timestamp.
	if err := binary.Write(s, binary.BigEndian, ts); err != nil {
		slog.Debug("ping handler write error", "err", err)
		return
	}
}

// PingResult holds the result of a single ping.
type PingResult struct {
	RTT  time.Duration
	Peer peer.ID
	Err  error
}

// SendPing sends a ping to the target peer and returns the RTT.
func SendPing(ctx context.Context, h host.Host, target peer.ID) PingResult {
	result := PingResult{Peer: target}

	s, err := h.NewStream(ctx, target, PingID)
	if err != nil {
		result.Err = fmt.Errorf("open stream: %w", err)
		return result
	}
	defer s.Close()

	sent := time.Now()
	ts := sent.UnixNano()

	if err := binary.Write(s, binary.BigEndian, ts); err != nil {
		result.Err = fmt.Errorf("write ping: %w", err)
		return result
	}

	// Close write side to signal we're done sending.
	if err := s.CloseWrite(); err != nil {
		result.Err = fmt.Errorf("close write: %w", err)
		return result
	}

	var echoTS int64
	if err := binary.Read(s, binary.BigEndian, &echoTS); err != nil {
		result.Err = fmt.Errorf("read pong: %w", err)
		return result
	}

	result.RTT = time.Since(sent)

	if echoTS != ts {
		result.Err = fmt.Errorf("timestamp mismatch: sent %d, got %d", ts, echoTS)
		return result
	}

	return result
}

// SendPings sends multiple pings and collects results.
func SendPings(ctx context.Context, h host.Host, target peer.ID, count int) []PingResult {
	results := make([]PingResult, 0, count)
	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			results = append(results, PingResult{Peer: target, Err: ctx.Err()})
			return results
		default:
		}
		result := SendPing(ctx, h, target)
		results = append(results, result)
		if result.Err == nil && i < count-1 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	return results
}
