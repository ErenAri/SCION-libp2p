package transport

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/prometheus/client_golang/prometheus"
)

// HostConfig configures the libp2p host.
type HostConfig struct {
	ListenAddrs        []string
	DataDir            string
	EnableRelay        bool
	ForcePrivate       bool
	PrometheusRegistry *prometheus.Registry // optional: enables libp2p built-in metrics
}

// NewHost creates a libp2p host with QUIC transport and relay support.
func NewHost(ctx context.Context, cfg HostConfig) (host.Host, error) {
	priv, err := loadOrGenerateKey(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("load/generate key: %w", err)
	}

	// Build resource manager with defaults.
	limiter := rcmgr.DefaultLimits
	scaled := limiter.AutoScale()
	rm, err := rcmgr.NewResourceManager(rcmgr.NewFixedLimiter(scaled))
	if err != nil {
		return nil, fmt.Errorf("resource manager: %w", err)
	}

	opts := []libp2p.Option{
		libp2p.Identity(priv),
		libp2p.ListenAddrStrings(cfg.ListenAddrs...),
		libp2p.ResourceManager(rm),
		libp2p.EnableHolePunching(),
	}

	if cfg.EnableRelay {
		opts = append(opts, libp2p.EnableRelay())
	}

	if cfg.ForcePrivate {
		opts = append(opts, libp2p.ForceReachabilityPrivate())
	}

	if cfg.PrometheusRegistry != nil {
		opts = append(opts, libp2p.PrometheusRegisterer(cfg.PrometheusRegistry))
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create host: %w", err)
	}

	slog.Info("libp2p host created",
		"peerID", h.ID().String(),
		"addrs", h.Addrs(),
	)

	return h, nil
}

const keyFileName = "peer.key"

func loadOrGenerateKey(dataDir string) (crypto.PrivKey, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}

	keyPath := filepath.Join(dataDir, keyFileName)

	// Try to load existing key.
	data, err := os.ReadFile(keyPath)
	if err == nil {
		priv, err := crypto.UnmarshalPrivateKey(data)
		if err == nil {
			slog.Debug("loaded existing peer key", "path", keyPath)
			return priv, nil
		}
		slog.Warn("failed to unmarshal existing key, generating new one", "err", err)
	}

	// Generate new Ed25519 key.
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	raw, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}

	if err := os.WriteFile(keyPath, raw, 0o600); err != nil {
		return nil, fmt.Errorf("write key: %w", err)
	}

	slog.Info("generated new peer key", "path", keyPath)
	return priv, nil
}
