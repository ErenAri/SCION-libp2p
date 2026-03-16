package protocol

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"

	"github.com/ErenAri/PathAware-libp2p/internal/cache"
	"github.com/ErenAri/PathAware-libp2p/internal/content"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// BlockTransferHandler handles incoming block requests.
// It supports optional in-memory caching (NDN-style relay caching).
type BlockTransferHandler struct {
	Store *content.Store
	Cache *cache.LRUCache // optional: if set, blocks are cached in memory
}

// Register sets up the block transfer stream handler.
func (h *BlockTransferHandler) Register(host host.Host) {
	host.SetStreamHandler(BlockTransferID, h.handleStream)
	slog.Debug("registered block transfer protocol handler")
}

func (h *BlockTransferHandler) handleStream(s network.Stream) {
	defer s.Close()

	// Read CID length (2 bytes) + CID string.
	var cidLen uint16
	if err := binary.Read(s, binary.BigEndian, &cidLen); err != nil {
		if err != io.EOF {
			slog.Debug("block handler: read CID length error", "err", err)
		}
		return
	}

	if cidLen > 1024 {
		slog.Debug("block handler: CID too long", "len", cidLen)
		return
	}

	cidBuf := make([]byte, cidLen)
	if _, err := io.ReadFull(s, cidBuf); err != nil {
		slog.Debug("block handler: read CID error", "err", err)
		return
	}
	cid := string(cidBuf)

	// Look up block: cache first, then disk.
	block, found := h.lookupBlock(cid)
	if !found {
		// Send not-found response: [0x00] + error message.
		s.Write([]byte{0x00})
		errMsg := []byte("block not found: " + cid)
		binary.Write(s, binary.BigEndian, uint16(len(errMsg)))
		s.Write(errMsg)
		return
	}

	// Cache the served block (NDN-style: relay nodes cache passing content).
	if h.Cache != nil {
		h.Cache.Put(block)
	}

	// Send found response: [0x01] + [4B data length] + data.
	s.Write([]byte{0x01})
	binary.Write(s, binary.BigEndian, uint32(len(block.Data)))
	if _, err := s.Write(block.Data); err != nil {
		slog.Debug("block handler: write data error", "err", err)
	}
}

// lookupBlock checks the in-memory cache first, then falls back to disk store.
func (h *BlockTransferHandler) lookupBlock(cid string) (content.Block, bool) {
	// Check cache first.
	if h.Cache != nil {
		if block, ok := h.Cache.Get(cid); ok {
			slog.Debug("block served from cache", "cid", cid[:min(8, len(cid))])
			return block, true
		}
	}

	// Fall back to disk.
	block, err := h.Store.Get(cid)
	if err != nil {
		return content.Block{}, false
	}
	return block, true
}

// BlockPushID is the protocol for pushing blocks to peers.
const BlockPushID = "/pathaware-libp2p/block-push/1.0.0"

// BlockPushHandler handles incoming block push requests.
type BlockPushHandler struct {
	Store *content.Store
	Cache *cache.LRUCache
}

// Register sets up the block push stream handler.
func (h *BlockPushHandler) Register(host host.Host) {
	host.SetStreamHandler(BlockPushID, h.handleStream)
	slog.Debug("registered block push protocol handler")
}

func (h *BlockPushHandler) handleStream(s network.Stream) {
	defer s.Close()

	// Read CID length (2 bytes) + CID string.
	var cidLen uint16
	if err := binary.Read(s, binary.BigEndian, &cidLen); err != nil {
		return
	}
	if cidLen > 1024 {
		return
	}
	cidBuf := make([]byte, cidLen)
	if _, err := io.ReadFull(s, cidBuf); err != nil {
		return
	}
	cid := string(cidBuf)

	// Read data length (4 bytes) + data.
	var dataLen uint32
	if err := binary.Read(s, binary.BigEndian, &dataLen); err != nil {
		return
	}
	if dataLen > 16*1024*1024 { // 16 MB max
		return
	}
	data := make([]byte, dataLen)
	if _, err := io.ReadFull(s, data); err != nil {
		return
	}

	// Verify CID.
	if content.ComputeCID(data) != cid {
		slog.Debug("block push CID mismatch", "cid", cid[:min(8, len(cid))])
		s.Write([]byte{0x00}) // reject
		return
	}

	block := content.Block{CID: cid, Data: data}

	// Store on disk and in cache.
	_ = h.Store.Put(block)
	if h.Cache != nil {
		h.Cache.Put(block)
	}

	s.Write([]byte{0x01}) // ack
	slog.Debug("received pushed block", "cid", cid[:min(8, len(cid))], "size", dataLen)
}

// PushBlock sends a block to a remote peer for replication.
func PushBlock(ctx context.Context, h host.Host, target peer.ID, block content.Block) error {
	s, err := h.NewStream(ctx, target, BlockPushID)
	if err != nil {
		return fmt.Errorf("open push stream: %w", err)
	}
	defer s.Close()

	// Send CID.
	cidBytes := []byte(block.CID)
	if err := binary.Write(s, binary.BigEndian, uint16(len(cidBytes))); err != nil {
		return fmt.Errorf("write CID length: %w", err)
	}
	if _, err := s.Write(cidBytes); err != nil {
		return fmt.Errorf("write CID: %w", err)
	}

	// Send data.
	if err := binary.Write(s, binary.BigEndian, uint32(len(block.Data))); err != nil {
		return fmt.Errorf("write data length: %w", err)
	}
	if _, err := s.Write(block.Data); err != nil {
		return fmt.Errorf("write data: %w", err)
	}

	if err := s.CloseWrite(); err != nil {
		return fmt.Errorf("close write: %w", err)
	}

	// Read ack.
	var ack [1]byte
	if _, err := io.ReadFull(s, ack[:]); err != nil {
		return fmt.Errorf("read ack: %w", err)
	}
	if ack[0] != 0x01 {
		return fmt.Errorf("block rejected by peer")
	}

	return nil
}

// FetchBlock retrieves a block by CID from a remote peer.
func FetchBlock(ctx context.Context, h host.Host, target peer.ID, cid string) (content.Block, error) {
	s, err := h.NewStream(ctx, target, BlockTransferID)
	if err != nil {
		return content.Block{}, fmt.Errorf("open stream: %w", err)
	}
	defer s.Close()

	// Send CID request.
	cidBytes := []byte(cid)
	if err := binary.Write(s, binary.BigEndian, uint16(len(cidBytes))); err != nil {
		return content.Block{}, fmt.Errorf("write CID length: %w", err)
	}
	if _, err := s.Write(cidBytes); err != nil {
		return content.Block{}, fmt.Errorf("write CID: %w", err)
	}
	if err := s.CloseWrite(); err != nil {
		return content.Block{}, fmt.Errorf("close write: %w", err)
	}

	// Read response.
	var status [1]byte
	if _, err := io.ReadFull(s, status[:]); err != nil {
		return content.Block{}, fmt.Errorf("read status: %w", err)
	}

	if status[0] == 0x00 {
		// Not found — read error message.
		var errLen uint16
		binary.Read(s, binary.BigEndian, &errLen)
		errBuf := make([]byte, errLen)
		io.ReadFull(s, errBuf)
		return content.Block{}, fmt.Errorf("block not found: %s", string(errBuf))
	}

	// Found — read data.
	var dataLen uint32
	if err := binary.Read(s, binary.BigEndian, &dataLen); err != nil {
		return content.Block{}, fmt.Errorf("read data length: %w", err)
	}

	data := make([]byte, dataLen)
	if _, err := io.ReadFull(s, data); err != nil {
		return content.Block{}, fmt.Errorf("read data: %w", err)
	}

	// Verify CID.
	computedCID := content.ComputeCID(data)
	if computedCID != cid {
		return content.Block{}, fmt.Errorf("CID mismatch: expected %s, got %s", cid, computedCID)
	}

	return content.Block{CID: cid, Data: data}, nil
}
