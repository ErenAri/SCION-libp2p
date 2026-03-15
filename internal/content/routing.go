package content

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/peer"
	mh "github.com/multiformats/go-multihash"

	"github.com/ipfs/go-cid"
)

// ContentRouter provides DHT-based content routing (Provide/FindProviders).
type ContentRouter struct {
	DHT *dht.IpfsDHT
}

// NewContentRouter creates a content router backed by the given DHT.
func NewContentRouter(d *dht.IpfsDHT) *ContentRouter {
	return &ContentRouter{DHT: d}
}

// cidFromString converts a hex SHA-256 hash to a proper CID for DHT operations.
func cidFromString(s string) (cid.Cid, error) {
	// Our CIDs are hex-encoded SHA-256 hashes. Wrap them in a proper CIDv1.
	raw := []byte(s)
	hash, err := mh.Sum(raw, mh.SHA2_256, -1)
	if err != nil {
		return cid.Undef, fmt.Errorf("compute multihash: %w", err)
	}
	return cid.NewCidV1(cid.Raw, hash), nil
}

// Provide announces to the DHT that this node holds the given content CID.
func (cr *ContentRouter) Provide(ctx context.Context, contentCID string) error {
	c, err := cidFromString(contentCID)
	if err != nil {
		return fmt.Errorf("create CID: %w", err)
	}

	if err := cr.DHT.Provide(ctx, c, true); err != nil {
		return fmt.Errorf("provide CID %s: %w", contentCID, err)
	}

	slog.Debug("announced content to DHT", "cid", contentCID)
	return nil
}

// FindProviders queries the DHT for peers that hold the given content.
func (cr *ContentRouter) FindProviders(ctx context.Context, contentCID string, maxPeers int) ([]peer.AddrInfo, error) {
	c, err := cidFromString(contentCID)
	if err != nil {
		return nil, fmt.Errorf("create CID: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	peerCh := cr.DHT.FindProvidersAsync(ctx, c, maxPeers)

	var providers []peer.AddrInfo
	for p := range peerCh {
		if p.ID == "" {
			continue
		}
		providers = append(providers, p)
	}

	slog.Debug("found providers", "cid", contentCID, "count", len(providers))
	return providers, nil
}

// ProvideBlocks announces all block CIDs for a manifest to the DHT.
func (cr *ContentRouter) ProvideBlocks(ctx context.Context, manifest Manifest) error {
	// Provide the root CID.
	if err := cr.Provide(ctx, manifest.RootCID); err != nil {
		slog.Warn("failed to provide root CID", "cid", manifest.RootCID, "err", err)
	}

	// Provide each chunk CID.
	for _, chunkCID := range manifest.ChunkCIDs {
		if err := cr.Provide(ctx, chunkCID); err != nil {
			slog.Warn("failed to provide chunk CID", "cid", chunkCID, "err", err)
		}
	}

	return nil
}
