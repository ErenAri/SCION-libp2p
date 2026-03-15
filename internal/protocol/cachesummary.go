package protocol

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// CacheSummaryHandler handles incoming cache summary (Bloom filter) exchanges.
type CacheSummaryHandler struct {
	// OnReceive is called when a cache summary is received from a peer.
	// The data is a serialized BloomFilter.
	OnReceive func(from peer.ID, data []byte)
}

// Register sets up the stream handler for cache summary protocol.
func (h *CacheSummaryHandler) Register(host host.Host) {
	host.SetStreamHandler(CacheSummaryID, func(s network.Stream) {
		defer s.Close()

		// Read length-prefixed Bloom filter data.
		var length uint32
		if err := binary.Read(s, binary.LittleEndian, &length); err != nil {
			slog.Debug("cache summary: read length failed", "err", err)
			return
		}
		if length > 1<<20 { // 1 MB max
			slog.Warn("cache summary: data too large", "length", length)
			return
		}

		data := make([]byte, length)
		if _, err := io.ReadFull(s, data); err != nil {
			slog.Debug("cache summary: read data failed", "err", err)
			return
		}

		if h.OnReceive != nil {
			h.OnReceive(s.Conn().RemotePeer(), data)
		}
	})
}

// SendCacheSummary sends a serialized Bloom filter to a peer.
func SendCacheSummary(ctx context.Context, h host.Host, target peer.ID, data []byte) error {
	s, err := h.NewStream(ctx, target, CacheSummaryID)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	defer s.Close()

	// Write length-prefixed data.
	if err := binary.Write(s, binary.LittleEndian, uint32(len(data))); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := s.Write(data); err != nil {
		return fmt.Errorf("write data: %w", err)
	}

	return nil
}
