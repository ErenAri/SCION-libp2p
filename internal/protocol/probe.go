package protocol

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Probe wire format:
//   [8B timestamp_ns][4B path_id][1B hop_count][4B throughput_estimate][4B jitter_us][32B nonce]
// Total: 53 bytes.
const probePayloadSize = 53

// ProbeHandler handles incoming probe requests by echoing back the payload
// with an incremented hop count.
type ProbeHandler struct{}

// Register sets up the probe stream handler on the host.
func (h *ProbeHandler) Register(host host.Host) {
	host.SetStreamHandler(PathProbeID, h.handleStream)
	slog.Debug("registered probe protocol handler")
}

func (h *ProbeHandler) handleStream(s network.Stream) {
	defer s.Close()

	buf := make([]byte, probePayloadSize)
	if _, err := io.ReadFull(s, buf); err != nil {
		if err != io.EOF {
			slog.Debug("probe handler read error", "err", err)
		}
		return
	}

	// Increment hop count at byte offset 12.
	buf[12]++

	if _, err := s.Write(buf); err != nil {
		slog.Debug("probe handler write error", "err", err)
		return
	}
}

// ProbeResult holds the result of a single path probe.
type ProbeResult struct {
	RTT        time.Duration
	HopCount   int
	PathID     uint32
	Target     peer.ID
	RelayPeers []peer.ID
	Err        error
}

// PathInfo describes a path to probe — either direct or through relays.
type PathInfo struct {
	ID         uint32
	Target     peer.ID
	RelayChain []peer.ID // empty for direct path
}

// SendProbe sends a probe along the specified path and measures RTT.
// For direct paths, it opens a stream directly to the target.
// For relay paths, it opens a stream to the first relay in the chain.
func SendProbe(ctx context.Context, h host.Host, path PathInfo) ProbeResult {
	result := ProbeResult{
		PathID:     path.ID,
		Target:     path.Target,
		RelayPeers: path.RelayChain,
	}

	// Determine the stream target (direct or first relay hop).
	streamTarget := path.Target
	if len(path.RelayChain) > 0 {
		streamTarget = path.RelayChain[0]
	}

	s, err := h.NewStream(ctx, streamTarget, PathProbeID)
	if err != nil {
		result.Err = fmt.Errorf("open probe stream: %w", err)
		return result
	}
	defer s.Close()

	// Build probe payload.
	payload := make([]byte, probePayloadSize)
	binary.BigEndian.PutUint64(payload[0:8], uint64(time.Now().UnixNano()))
	binary.BigEndian.PutUint32(payload[8:12], path.ID)
	payload[12] = 0 // hop count starts at 0
	// bytes 13-16: throughput_estimate (filled by responder, 0 initially)
	// bytes 17-20: jitter_us (filled by responder, 0 initially)

	// Fill nonce bytes (21..52) with random data.
	if _, err := rand.Read(payload[21:]); err != nil {
		result.Err = fmt.Errorf("generate nonce: %w", err)
		return result
	}

	sent := time.Now()

	if _, err := s.Write(payload); err != nil {
		result.Err = fmt.Errorf("write probe: %w", err)
		return result
	}

	if err := s.CloseWrite(); err != nil {
		result.Err = fmt.Errorf("close write: %w", err)
		return result
	}

	// Read echo.
	echo := make([]byte, probePayloadSize)
	if _, err := io.ReadFull(s, echo); err != nil {
		result.Err = fmt.Errorf("read probe response: %w", err)
		return result
	}

	result.RTT = time.Since(sent)

	// Verify nonce matches (bytes 21..52).
	for i := 21; i < probePayloadSize; i++ {
		if echo[i] != payload[i] {
			result.Err = fmt.Errorf("nonce mismatch at byte %d", i)
			return result
		}
	}

	// Read hop count from response.
	result.HopCount = int(echo[12])

	// Verify path ID matches.
	echoPathID := binary.BigEndian.Uint32(echo[8:12])
	if echoPathID != path.ID {
		result.Err = fmt.Errorf("path ID mismatch: sent %d, got %d", path.ID, echoPathID)
		return result
	}

	return result
}

// SendProbes probes multiple paths in parallel and returns all results.
func SendProbes(ctx context.Context, h host.Host, paths []PathInfo) []ProbeResult {
	results := make([]ProbeResult, len(paths))
	var wg sync.WaitGroup

	for i, path := range paths {
		wg.Add(1)
		go func(idx int, p PathInfo) {
			defer wg.Done()
			results[idx] = SendProbe(ctx, h, p)
		}(i, path)
	}

	wg.Wait()
	return results
}
