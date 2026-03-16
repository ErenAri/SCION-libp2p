package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/erena/scion-libp2p/internal/node"
)

// Cluster manages multiple Node instances for integration testing.
type Cluster struct {
	Nodes    []*node.Node
	BasePort int
	tmpDirs  []string
	ctx      context.Context
	cancel   context.CancelFunc
}

// ClusterOptions allows configuring cluster nodes with specific policies.
type ClusterOptions struct {
	Policy        string        // path selection policy (default: "balanced")
	Epsilon       float64       // epsilon for epsilon-greedy policy
	DisableMDNS   bool          // disable mDNS (recommended for benchmarks to avoid shutdown races)
	ProbeInterval time.Duration // override probe interval (default: 30s; use 3-5s for benchmarks)
	DisableCache  bool          // set CacheMaxBytes=1 to effectively disable block caching
	DisableBloom  bool          // disable Bloom filter cooperative cache exchange
}

func clusterConfig(i, port int, tmpDir string) node.Config {
	return clusterConfigWithOptions(i, port, tmpDir, ClusterOptions{})
}

func clusterConfigWithOptions(i, port int, tmpDir string, opts ClusterOptions) node.Config {
	policy := opts.Policy
	if policy == "" {
		policy = "balanced"
	}
	epsilon := opts.Epsilon
	if epsilon <= 0 {
		epsilon = 0.1
	}
	probeInterval := opts.ProbeInterval
	if probeInterval <= 0 {
		probeInterval = 30 * time.Second
	}
	cacheBytes := int64(16 * 1024 * 1024)
	if opts.DisableCache {
		cacheBytes = 1 // effectively disable caching
	}
	return node.Config{
		ListenAddrs:          []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port)},
		BootstrapPeers:       nil,
		DataDir:              tmpDir,
		EnableRelay:          true,
		EnableMDNS:           !opts.DisableMDNS,
		APIAddr:              fmt.Sprintf("127.0.0.1:%d", port+1000),
		MetricsAddr:          "", // disable metrics to avoid port conflicts in benchmarks
		PathPolicy:           policy,
		PathEpsilon:          epsilon,
		CacheMaxBytes:        cacheBytes,
		ChunkSizeBytes:       256 * 1024,
		ProbeInterval:        probeInterval,
		ProbeTimeout:         3 * time.Second,
		DisableBloomExchange: opts.DisableBloom,
		LogLevel:             "warn",
	}
}

// NewCluster creates and starts n nodes on localhost with sequential ports.
func NewCluster(t *testing.T, n int) *Cluster {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	c := &Cluster{
		Nodes:    make([]*node.Node, 0, n),
		BasePort: 10000 + (os.Getpid() % 5000),
		ctx:      ctx,
		cancel:   cancel,
	}

	for i := 0; i < n; i++ {
		tmpDir, err := os.MkdirTemp("", fmt.Sprintf("scion-test-node-%d-*", i))
		if err != nil {
			t.Fatalf("create temp dir: %v", err)
		}
		c.tmpDirs = append(c.tmpDirs, tmpDir)

		port := c.BasePort + i
		cfg := clusterConfig(i, port, tmpDir)

		nd, err := node.New(cfg)
		if err != nil {
			t.Fatalf("create node %d: %v", i, err)
		}

		if err := nd.Start(ctx); err != nil {
			t.Fatalf("start node %d: %v", i, err)
		}

		c.Nodes = append(c.Nodes, nd)
	}

	// Manually connect nodes to each other (in addition to mDNS).
	for i := 1; i < len(c.Nodes); i++ {
		for j := 0; j < i; j++ {
			peerInfo := c.Nodes[j].Host.Peerstore().PeerInfo(c.Nodes[j].Host.ID())
			if err := c.Nodes[i].Host.Connect(ctx, peerInfo); err != nil {
				t.Logf("warning: node %d failed to connect to node %d: %v", i, j, err)
			}
		}
	}

	return c
}

// NewClusterWithOptions creates a cluster with specific policy options (for benchmarks).
// Returns nil on error.
func NewClusterWithOptions(n int, opts ClusterOptions) *Cluster {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Cluster{
		Nodes:    make([]*node.Node, 0, n),
		BasePort: 12000 + (os.Getpid() % 5000),
		ctx:      ctx,
		cancel:   cancel,
	}

	for i := 0; i < n; i++ {
		tmpDir, err := os.MkdirTemp("", fmt.Sprintf("scion-bench-node-%d-*", i))
		if err != nil {
			slog.Error("create temp dir", "err", err)
			cancel()
			return nil
		}
		c.tmpDirs = append(c.tmpDirs, tmpDir)

		port := c.BasePort + i
		cfg := clusterConfigWithOptions(i, port, tmpDir, opts)

		nd, err := node.New(cfg)
		if err != nil {
			slog.Error("create node", "node", i, "err", err)
			cancel()
			return nil
		}

		if err := nd.Start(ctx); err != nil {
			slog.Error("start node", "node", i, "err", err)
			cancel()
			return nil
		}

		c.Nodes = append(c.Nodes, nd)
	}

	// Connect nodes.
	for i := 1; i < len(c.Nodes); i++ {
		for j := 0; j < i; j++ {
			peerInfo := c.Nodes[j].Host.Peerstore().PeerInfo(c.Nodes[j].Host.ID())
			if err := c.Nodes[i].Host.Connect(ctx, peerInfo); err != nil {
				slog.Warn("connect failed", "from", i, "to", j, "err", err)
			}
		}
	}

	return c
}

// NewClusterDirect creates a cluster without *testing.T (for benchmarks/CLI).
// Returns nil on error.
func NewClusterDirect(n int) *Cluster {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Cluster{
		Nodes:    make([]*node.Node, 0, n),
		BasePort: 11000 + (os.Getpid() % 5000),
		ctx:      ctx,
		cancel:   cancel,
	}

	for i := 0; i < n; i++ {
		tmpDir, err := os.MkdirTemp("", fmt.Sprintf("scion-bench-node-%d-*", i))
		if err != nil {
			slog.Error("create temp dir", "err", err)
			cancel()
			return nil
		}
		c.tmpDirs = append(c.tmpDirs, tmpDir)

		port := c.BasePort + i
		cfg := clusterConfig(i, port, tmpDir)

		nd, err := node.New(cfg)
		if err != nil {
			slog.Error("create node", "node", i, "err", err)
			cancel()
			return nil
		}

		if err := nd.Start(ctx); err != nil {
			slog.Error("start node", "node", i, "err", err)
			cancel()
			return nil
		}

		c.Nodes = append(c.Nodes, nd)
	}

	// Connect nodes.
	for i := 1; i < len(c.Nodes); i++ {
		for j := 0; j < i; j++ {
			peerInfo := c.Nodes[j].Host.Peerstore().PeerInfo(c.Nodes[j].Host.ID())
			if err := c.Nodes[i].Host.Connect(ctx, peerInfo); err != nil {
				slog.Warn("connect failed", "from", i, "to", j, "err", err)
			}
		}
	}

	return c
}

// Stop shuts down all nodes and cleans up temp directories.
func (c *Cluster) Stop(t *testing.T) {
	t.Helper()
	c.cancel()

	for i, n := range c.Nodes {
		if err := n.Stop(); err != nil {
			t.Logf("warning: error stopping node %d: %v", i, err)
		}
	}

	for _, dir := range c.tmpDirs {
		os.RemoveAll(dir)
	}
}

// Cleanup shuts down all nodes without *testing.T (for benchmarks).
func (c *Cluster) Cleanup() {
	// Node.Stop() handles mDNS closure, context cancel, and drain internally.
	for _, n := range c.Nodes {
		n.Stop()
	}
	c.cancel() // cancel cluster context after all nodes are stopped
	for _, dir := range c.tmpDirs {
		os.RemoveAll(dir)
	}
}

// WaitForMesh blocks until all nodes can see all other nodes as peers.
func (c *Cluster) WaitForMesh(t *testing.T, timeout time.Duration) {
	t.Helper()
	expected := len(c.Nodes) - 1

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allConnected := true
		for _, n := range c.Nodes {
			if len(n.Host.Network().Peers()) < expected {
				allConnected = false
				break
			}
		}
		if allConnected {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Log peer counts for debugging.
	for i, n := range c.Nodes {
		t.Logf("node %d (%s): %d peers", i, n.PeerID(), len(n.Host.Network().Peers()))
	}
	t.Fatalf("mesh not formed within %s (expected %d peers per node)", timeout, expected)
}
