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
	"github.com/erena/scion-libp2p/internal/erasure"
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
	PeerBlooms         *cache.PeerBloomStore // per-peer cache Bloom filters

	ctx    context.Context
	cancel context.CancelFunc

	mdns      transport.MDNSService
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
	n.BlockCache.SetMetricsHook(cache.MetricsHook{
		OnHit:  func() { n.Metrics.CacheHits.Inc() },
		OnMiss: func() { n.Metrics.CacheMisses.Inc() },
	})
	slog.Info("block cache initialized", "maxBytes", n.Cfg.CacheMaxBytes)

	// Register block transfer handler (with cache for NDN-style relay caching).
	blockHandler := &protocol.BlockTransferHandler{Store: n.ContentStore, Cache: n.BlockCache}
	blockHandler.Register(n.Host)

	// Register block push handler (for replication).
	pushHandler := &protocol.BlockPushHandler{Store: n.ContentStore, Cache: n.BlockCache}
	pushHandler.Register(n.Host)

	// Initialize replication tracker.
	n.ReplicationTracker = content.NewReplicationTracker(5) // replicate after 5 fetches

	// Initialize cooperative caching (Bloom filter exchange).
	n.PeerBlooms = cache.NewPeerBloomStore()
	cacheSummaryHandler := &protocol.CacheSummaryHandler{
		OnReceive: func(from peer.ID, data []byte) {
			bf := cache.BloomFromBytes(data)
			if bf != nil {
				n.PeerBlooms.Set(from, bf)
				slog.Debug("received cache summary", "from", from.String()[:8], "size", len(data))
			}
		},
	}
	cacheSummaryHandler.Register(n.Host)

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
		svc, err := transport.SetupMDNS(n.ctx, n.Host)
		if err != nil {
			slog.Warn("failed to start mDNS", "err", err)
		} else {
			n.mdns = svc
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

	// Start periodic Bloom filter exchange for cooperative caching.
	if !n.Cfg.DisableBloomExchange {
		go n.cacheSummaryLoop()
	}

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

	// Use erasure-coded replication if enabled.
	if n.Cfg.EnableErasure {
		n.replicateWithErasure(popular, peers)
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

// replicateWithErasure encodes popular blocks into fragments and distributes
// them across peers. Each peer gets 1-2 fragments instead of the full block,
// achieving the same resilience with ~1.5x storage overhead (for k=4, m=2)
// instead of N× overhead from full replication.
func (n *Node) replicateWithErasure(popular []string, peers []peer.ID) {
	dataShards := n.Cfg.DataShards
	parityShards := n.Cfg.ParityShards
	if dataShards <= 0 {
		dataShards = 4
	}
	if parityShards <= 0 {
		parityShards = 2
	}

	replicated := 0
	for _, cid := range popular {
		block, err := n.ContentStore.Get(cid)
		if err != nil {
			continue
		}

		encStart := time.Now()
		fragments, err := erasure.Encode(block.Data, cid, dataShards, parityShards)
		if err != nil {
			slog.Warn("erasure encode failed", "cid", cid[:8], "err", err)
			continue
		}
		if n.Metrics != nil {
			n.Metrics.ErasureEncodeSeconds.Observe(time.Since(encStart).Seconds())
		}

		// Store all fragments locally.
		for _, f := range fragments {
			if err := n.ContentStore.PutFragment(cid, f.Index, f.Data); err != nil {
				slog.Debug("store fragment failed", "cid", cid[:8], "index", f.Index, "err", err)
			}
		}

		// Distribute fragments round-robin across peers.
		// Each peer gets one fragment; if we have more fragments than peers,
		// some peers get multiple fragments.
		eligiblePeers := make([]peer.ID, 0, len(peers))
		for _, p := range peers {
			if p != n.Host.ID() {
				eligiblePeers = append(eligiblePeers, p)
			}
		}
		if len(eligiblePeers) == 0 {
			continue
		}

		for i, f := range fragments {
			target := eligiblePeers[i%len(eligiblePeers)]
			fragBlock := content.Block{
				CID:  f.CID,
				Data: f.Data,
			}
			ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
			err := protocol.PushBlock(ctx, n.Host, target, fragBlock)
			cancel()
			if err != nil {
				slog.Debug("fragment push failed", "peer", target.String()[:8], "index", f.Index, "err", err)
				continue
			}
			replicated++
		}
	}

	if replicated > 0 && n.Metrics != nil {
		n.Metrics.FragmentsStored.Add(float64(replicated))
	}
	slog.Debug("erasure replication cycle complete", "fragments_pushed", replicated, "popular", len(popular))
}

// cacheSummaryLoop periodically broadcasts cache Bloom filters to all peers.
func (n *Node) cacheSummaryLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.broadcastCacheSummary()
		}
	}
}

func (n *Node) broadcastCacheSummary() {
	if n.BlockCache == nil {
		return
	}

	cids := n.BlockCache.CIDs()
	if len(cids) == 0 {
		return
	}

	bf := cache.NewBloomFilter(max(len(cids), 100), 0.01)
	for _, cid := range cids {
		bf.Add(cid)
	}
	data := bf.Bytes()

	peers := n.Host.Network().Peers()
	for _, p := range peers {
		if p == n.Host.ID() {
			continue
		}
		ctx, cancel := context.WithTimeout(n.ctx, 5*time.Second)
		err := protocol.SendCacheSummary(ctx, n.Host, p, data)
		cancel()
		if err != nil {
			slog.Debug("cache summary send failed", "peer", p.String()[:8], "err", err)
		}
	}
}

// Stop gracefully shuts down the node.
// Shutdown order matters: stop discovery first so no new connections are
// initiated, cancel the context so in-flight operations abort, then close
// the DHT and host.
func (n *Node) Stop() error {
	slog.Info("stopping node")
	if n.PathManager != nil {
		n.PathManager.Stop()
	}
	// Stop mDNS discovery first to prevent new connection attempts.
	if n.mdns != nil {
		if err := n.mdns.Close(); err != nil {
			slog.Warn("error closing mDNS", "err", err)
		}
	}
	// Cancel context so in-flight connections and goroutines stop.
	if n.cancel != nil {
		n.cancel()
	}
	// Brief drain period for in-flight connection upgrades to finish.
	time.Sleep(50 * time.Millisecond)
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

