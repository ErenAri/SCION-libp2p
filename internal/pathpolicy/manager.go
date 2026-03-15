package pathpolicy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/erena/scion-libp2p/internal/protocol"
	"github.com/erena/scion-libp2p/internal/transport"
)

// Manager runs periodic path probing and maintains scored path sets.
type Manager struct {
	host   host.Host
	policy Policy

	probeInterval time.Duration
	probeTimeout  time.Duration

	mu       sync.RWMutex
	pathSets map[peer.ID]*PathSet // per-target

	ctx    context.Context
	cancel context.CancelFunc
}

// ManagerConfig configures the path manager.
type ManagerConfig struct {
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration
	PolicyName    string
	Epsilon       float64 // epsilon for epsilon-greedy policy (default 0.1)
}

// NewManager creates a new path manager.
func NewManager(h host.Host, cfg ManagerConfig) (*Manager, error) {
	epsilon := cfg.Epsilon
	if epsilon <= 0 {
		epsilon = 0.1
	}
	pol, err := PolicyFromNameWithEpsilon(cfg.PolicyName, epsilon)
	if err != nil {
		return nil, fmt.Errorf("create policy: %w", err)
	}

	return &Manager{
		host:          h,
		policy:        pol,
		probeInterval: cfg.ProbeInterval,
		probeTimeout:  cfg.ProbeTimeout,
		pathSets:      make(map[peer.ID]*PathSet),
	}, nil
}

// Start begins background path probing.
func (m *Manager) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)
	go m.probeLoop()
	slog.Info("path manager started",
		"policy", m.policy.Name(),
		"interval", m.probeInterval,
	)
}

// Stop halts the background probing.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// BestPath returns the best path to the given target peer, or nil if none.
// If the active policy is epsilon-greedy, uses randomized selection to
// avoid herd effects on the lowest-latency path.
func (m *Manager) BestPath(target peer.ID) *Path {
	m.mu.RLock()
	ps, ok := m.pathSets[target]
	m.mu.RUnlock()
	if !ok {
		return nil
	}

	// Use specialized selection for policies that implement it.
	switch p := m.policy.(type) {
	case EpsilonGreedyPolicy:
		return p.SelectPath(ps.All())
	case *DecayingEpsilonGreedyPolicy:
		return p.SelectPath(ps.All())
	case *UCB1Policy:
		return p.SelectPath(ps.All())
	}

	return ps.Best(m.policy)
}

// Paths returns all known paths to a target peer.
func (m *Manager) Paths(target peer.ID) []*Path {
	m.mu.RLock()
	ps, ok := m.pathSets[target]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return ps.All()
}

// AllPaths return all known paths across all peers.
func (m *Manager) AllPaths() map[peer.ID][]*Path {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[peer.ID][]*Path, len(m.pathSets))
	for pid, ps := range m.pathSets {
		result[pid] = ps.All()
	}
	return result
}

// PolicyName returns the active policy name.
func (m *Manager) PolicyName() string {
	return m.policy.Name()
}

// DisjointPaths returns up to n paths to the target that share no relay peers,
// preferring higher-scored paths. This avoids shared bottleneck links when
// fetching content in parallel (recommended by SCION MPQUIC IETF draft).
func (m *Manager) DisjointPaths(target peer.ID, n int) []*Path {
	m.mu.RLock()
	ps, ok := m.pathSets[target]
	m.mu.RUnlock()
	if !ok {
		return nil
	}

	all := ps.All()
	if len(all) == 0 {
		return nil
	}

	// Sort by score descending.
	type scored struct {
		path  *Path
		score float64
	}
	scoredPaths := make([]scored, 0, len(all))
	for _, p := range all {
		if p.Metrics.SampleCount > 0 {
			scoredPaths = append(scoredPaths, scored{path: p, score: m.policy.Score(p)})
		}
	}
	// Insertion sort (list is small).
	for i := 1; i < len(scoredPaths); i++ {
		for j := i; j > 0 && scoredPaths[j].score > scoredPaths[j-1].score; j-- {
			scoredPaths[j], scoredPaths[j-1] = scoredPaths[j-1], scoredPaths[j]
		}
	}

	// Greedy selection: pick paths that share no relay peers with already-selected paths.
	usedRelays := make(map[peer.ID]bool)
	var selected []*Path

	for _, sp := range scoredPaths {
		if len(selected) >= n {
			break
		}

		// Check if this path shares any relay with already-selected paths.
		conflict := false
		for _, relay := range sp.path.RelayChain {
			if usedRelays[relay] {
				conflict = true
				break
			}
		}
		if conflict {
			continue
		}

		// Direct paths are always disjoint from relay paths.
		selected = append(selected, sp.path)
		for _, relay := range sp.path.RelayChain {
			usedRelays[relay] = true
		}
	}

	return selected
}

func (m *Manager) probeLoop() {
	// Run an initial probe immediately.
	m.probeAll()

	ticker := time.NewTicker(m.probeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.probeAll()
		}
	}
}

func (m *Manager) probeAll() {
	peers := m.host.Network().Peers()
	if len(peers) == 0 {
		return
	}

	// Discover relay peers.
	relays := transport.EnumerateRelayPeers(m.host)

	for _, target := range peers {
		m.probeTarget(target, relays)
	}

	// Prune stale paths that haven't been successfully probed recently.
	// SCIONLab data shows average path lifetimes of ~8.6 hours, so paths
	// that haven't responded in StalePathTimeout are likely dead.
	m.pruneStalePaths()
}

// pruneStalePaths removes paths that haven't been successfully probed
// within StalePathTimeout.
func (m *Manager) pruneStalePaths() {
	m.mu.RLock()
	sets := make(map[peer.ID]*PathSet, len(m.pathSets))
	for k, v := range m.pathSets {
		sets[k] = v
	}
	m.mu.RUnlock()

	now := time.Now()
	for _, ps := range sets {
		for _, p := range ps.All() {
			if p.Metrics.SampleCount > 0 &&
				p.Metrics.SuccessRate < 0.1 &&
				now.Sub(p.Metrics.LastProbed) > StalePathTimeout {
				ps.Remove(p.ID)
				slog.Debug("pruned stale path",
					"path", p.ID,
					"target", p.Target.String()[:8],
					"last_probed", p.Metrics.LastProbed.Format(time.RFC3339),
				)
			}
		}
	}
}

func (m *Manager) probeTarget(target peer.ID, relays []peer.AddrInfo) {
	// Build candidate paths.
	var paths []protocol.PathInfo
	pathIDCounter := uint32(0)

	// Direct path.
	directPathID := fmt.Sprintf("direct-%s", target.String()[:8])
	paths = append(paths, protocol.PathInfo{
		ID:     pathIDCounter,
		Target: target,
	})
	pathIDCounter++

	// Relay paths (one per relay peer).
	relayPathIDs := make([]string, 0)
	for _, relay := range relays {
		if relay.ID == target || relay.ID == m.host.ID() {
			continue
		}
		rpid := fmt.Sprintf("relay-%s-via-%s", target.String()[:8], relay.ID.String()[:8])
		relayPathIDs = append(relayPathIDs, rpid)
		paths = append(paths, protocol.PathInfo{
			ID:         pathIDCounter,
			Target:     target,
			RelayChain: []peer.ID{relay.ID},
		})
		pathIDCounter++
	}

	// Probe all paths.
	ctx, cancel := context.WithTimeout(m.ctx, m.probeTimeout)
	defer cancel()

	results := protocol.SendProbes(ctx, m.host, paths)

	// Update path sets.
	m.mu.Lock()
	ps, ok := m.pathSets[target]
	if !ok {
		ps = NewPathSet()
		m.pathSets[target] = ps
	}
	m.mu.Unlock()

	// Process direct path result.
	if len(results) > 0 {
		dp, exists := ps.Get(directPathID)
		if !exists {
			dp = &Path{
				ID:     directPathID,
				Target: target,
				Type:   PathDirect,
			}
		}
		success := results[0].Err == nil
		rtt := results[0].RTT
		if success {
			dp.Metrics.HopCount = results[0].HopCount
		}
		dp.Metrics.RecordProbe(rtt, success)
		ps.Add(dp)

		if !success {
			slog.Debug("direct probe failed", "target", target.String()[:8], "err", results[0].Err)
		}
	}

	// Process relay path results.
	for i, rpid := range relayPathIDs {
		resultIdx := i + 1 // offset by 1 for the direct path
		if resultIdx >= len(results) {
			break
		}

		rp, exists := ps.Get(rpid)
		if !exists {
			rp = &Path{
				ID:         rpid,
				Target:     target,
				Type:       PathRelay,
				RelayChain: paths[resultIdx].RelayChain,
			}
		}
		success := results[resultIdx].Err == nil
		rtt := results[resultIdx].RTT
		if success {
			rp.Metrics.HopCount = results[resultIdx].HopCount
		}
		rp.Metrics.RecordProbe(rtt, success)
		ps.Add(rp)
	}
}
