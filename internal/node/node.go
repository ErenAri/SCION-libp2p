package node

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/erena/scion-libp2p/internal/cache"
	"github.com/erena/scion-libp2p/internal/content"
	"github.com/erena/scion-libp2p/internal/metrics"
	"github.com/erena/scion-libp2p/internal/pathpolicy"
	"github.com/erena/scion-libp2p/internal/protocol"
	"github.com/erena/scion-libp2p/internal/transport"
)

// Node is the top-level struct that wires all subsystems together.
type Node struct {
	Cfg                Config
	Host               host.Host
	DHT                *dht.IpfsDHT
	Metrics            *metrics.Metrics
	PathManager        *pathpolicy.Manager
	ContentStore       *content.Store
	ContentRouter      *content.ContentRouter
	BlockCache         *cache.LRUCache
	ReplicationTracker *content.ReplicationTracker

	ctx    context.Context
	cancel context.CancelFunc

	startedAt time.Time
}

// New creates and initializes a new Node but does not start networking.
func New(cfg Config) (*Node, error) {
	return &Node{Cfg: cfg}, nil
}

// Start initializes the libp2p host, DHT, discovery, and protocol handlers.
func (n *Node) Start(ctx context.Context) error {
	n.ctx, n.cancel = context.WithCancel(ctx)
	n.startedAt = time.Now()

	// Initialize metrics early so we can pass the registry to libp2p.
	n.Metrics = metrics.New()

	// Create libp2p host.
	h, err := transport.NewHost(n.ctx, transport.HostConfig{
		ListenAddrs:        n.Cfg.ListenAddrs,
		DataDir:            n.Cfg.DataDir,
		EnableRelay:        n.Cfg.EnableRelay,
		PrometheusRegistry: n.Metrics.Registry,
	})
	if err != nil {
		return fmt.Errorf("create host: %w", err)
	}
	n.Host = h

	// Register protocol handlers.
	pingHandler := &protocol.PingHandler{}
	pingHandler.Register(n.Host)

	probeHandler := &protocol.ProbeHandler{}
	probeHandler.Register(n.Host)

	// Initialize content store.
	store, err := content.NewStore(n.Cfg.DataDir)
	if err != nil {
		return fmt.Errorf("create content store: %w", err)
	}
	n.ContentStore = store

	// Initialize block cache.
	n.BlockCache = cache.NewLRUCache(n.Cfg.CacheMaxBytes)
	n.BlockCache.SetPinChecker(n.ContentStore)
	slog.Info("block cache initialized", "maxBytes", n.Cfg.CacheMaxBytes)

	// Register block transfer handler (with cache for NDN-style relay caching).
	blockHandler := &protocol.BlockTransferHandler{Store: n.ContentStore, Cache: n.BlockCache}
	blockHandler.Register(n.Host)

	// Register block push handler (for replication).
	pushHandler := &protocol.BlockPushHandler{Store: n.ContentStore, Cache: n.BlockCache}
	pushHandler.Register(n.Host)

	// Initialize replication tracker.
	n.ReplicationTracker = content.NewReplicationTracker(5) // replicate after 5 fetches

	// Parse bootstrap peers.
	var bootstrapPeers []peer.AddrInfo
	if len(n.Cfg.BootstrapPeers) > 0 {
		bootstrapPeers, err = transport.ParseBootstrapPeers(n.Cfg.BootstrapPeers)
		if err != nil {
			return fmt.Errorf("parse bootstrap peers: %w", err)
		}
	}

	// Initialize DHT.
	n.DHT, err = transport.SetupDHT(n.ctx, n.Host, bootstrapPeers)
	if err != nil {
		return fmt.Errorf("setup DHT: %w", err)
	}

	// Initialize content router (DHT-based content discovery).
	n.ContentRouter = content.NewContentRouter(n.DHT)

	// Enable relay service if configured.
	if n.Cfg.EnableRelay {
		if err := transport.EnableRelayService(n.Host); err != nil {
			slog.Warn("failed to enable relay service", "err", err)
		}
	}

	// Start mDNS if configured.
	if n.Cfg.EnableMDNS {
		if err := transport.SetupMDNS(n.Host); err != nil {
			slog.Warn("failed to start mDNS", "err", err)
		}
	}

	// Start metrics server.
	if n.Cfg.MetricsAddr != "" {
		go metrics.StartMetricsServer(n.Cfg.MetricsAddr, n.Metrics.Registry)
	}

	// Start path manager.
	pm, err := pathpolicy.NewManager(n.Host, pathpolicy.ManagerConfig{
		ProbeInterval: n.Cfg.ProbeInterval,
		ProbeTimeout:  n.Cfg.ProbeTimeout,
		PolicyName:    n.Cfg.PathPolicy,
		Epsilon:       n.Cfg.PathEpsilon,
	})
	if err != nil {
		return fmt.Errorf("create path manager: %w", err)
	}
	n.PathManager = pm
	n.PathManager.Start(n.ctx)

	// Start background replication of popular blocks.
	go n.replicationLoop()

	slog.Info("node started",
		"peerID", n.Host.ID().String(),
		"addrs", n.Host.Addrs(),
	)

	return nil
}

// replicationLoop periodically pushes popular blocks to connected peers.
func (n *Node) replicationLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.replicatePopularBlocks()
		}
	}
}

func (n *Node) replicatePopularBlocks() {
	popular := n.ReplicationTracker.PopularBlocks()
	if len(popular) == 0 {
		return
	}

	peers := n.Host.Network().Peers()
	if len(peers) == 0 {
		return
	}

	replicated := 0
	for _, cid := range popular {
		block, err := n.ContentStore.Get(cid)
		if err != nil {
			continue
		}

		for _, p := range peers {
			if p == n.Host.ID() {
				continue
			}
			ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
			err := protocol.PushBlock(ctx, n.Host, p, block)
			cancel()
			if err != nil {
				slog.Debug("replication push failed", "peer", p.String()[:8], "cid", cid[:8], "err", err)
				continue
			}
			replicated++
		}
	}

	if replicated > 0 && n.Metrics != nil {
		n.Metrics.BlocksReplicated.Add(float64(replicated))
	}
	slog.Debug("replication cycle complete", "replicated", replicated, "popular", len(popular))
}

// Stop gracefully shuts down the node.
func (n *Node) Stop() error {
	slog.Info("stopping node")
	if n.PathManager != nil {
		n.PathManager.Stop()
	}
	if n.cancel != nil {
		n.cancel()
	}
	if n.DHT != nil {
		if err := n.DHT.Close(); err != nil {
			slog.Warn("error closing DHT", "err", err)
		}
	}
	if n.Host != nil {
		if err := n.Host.Close(); err != nil {
			return fmt.Errorf("close host: %w", err)
		}
	}
	return nil
}

// PeerID returns the node's peer ID as a string.
func (n *Node) PeerID() string {
	if n.Host == nil {
		return ""
	}
	return n.Host.ID().String()
}

// Uptime returns how long the node has been running.
func (n *Node) Uptime() time.Duration {
	return time.Since(n.startedAt)
}

// ConnectedPeers returns information about currently connected peers.
func (n *Node) ConnectedPeers() []PeerInfo {
	if n.Host == nil {
		return nil
	}
	peers := n.Host.Network().Peers()
	result := make([]PeerInfo, 0, len(peers))
	for _, p := range peers {
		conns := n.Host.Network().ConnsToPeer(p)
		addrs := make([]string, 0, len(conns))
		for _, c := range conns {
			addrs = append(addrs, c.RemoteMultiaddr().String())
		}

		protoIDs, _ := n.Host.Peerstore().GetProtocols(p)
		protos := make([]string, len(protoIDs))
		for i, pid := range protoIDs {
			protos[i] = string(pid)
		}

		result = append(result, PeerInfo{
			ID:        p.String(),
			Addrs:     addrs,
			Protocols: protos,
		})
	}
	return result
}

// PeerInfo holds displayable information about a connected peer.
type PeerInfo struct {
	ID        string   `json:"id"`
	Addrs     []string `json:"addrs"`
	Protocols []string `json:"protocols,omitempty"`
}

// Ed25519PrivateKey extracts the node's Ed25519 private key from the libp2p host.
// Returns nil if the key is not Ed25519 or not available.
func (n *Node) Ed25519PrivateKey() []byte {
	if n.Host == nil {
		return nil
	}
	key := n.Host.Peerstore().PrivKey(n.Host.ID())
	if key == nil {
		return nil
	}
	raw, err := key.Raw()
	if err != nil {
		return nil
	}
	return raw
}

