package pathpolicy

import (
	"math"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// PathType describes whether a path is direct or relayed.
type PathType string

const (
	PathDirect PathType = "direct"
	PathRelay  PathType = "relay"
)

// Path represents a network path to a target peer.
type Path struct {
	ID         string
	Target     peer.ID
	Type       PathType
	RelayChain []peer.ID
	Metrics    PathMetrics
}

// PathMetrics holds measured quality metrics for a path.
type PathMetrics struct {
	AvgRTT             time.Duration
	P95RTT             time.Duration
	Jitter             time.Duration // RTT variance (stddev)
	ThroughputEstimate float64       // estimated bytes/sec from probe payload size / RTT
	HopCount           int
	SuccessRate        float64
	LastProbed         time.Time
	SampleCount        int

	// Internal: ring buffer of recent RTT samples for EWMA.
	rttSamples []time.Duration
}

const maxRTTSamples = 20

// RecordProbe updates the path metrics with a new probe result.
func (m *PathMetrics) RecordProbe(rtt time.Duration, success bool) {
	m.LastProbed = time.Now()
	m.SampleCount++

	if success {
		// Update RTT samples (ring buffer).
		m.rttSamples = append(m.rttSamples, rtt)
		if len(m.rttSamples) > maxRTTSamples {
			m.rttSamples = m.rttSamples[1:]
		}

		// Recompute average RTT.
		var total time.Duration
		for _, s := range m.rttSamples {
			total += s
		}
		m.AvgRTT = total / time.Duration(len(m.rttSamples))

		// Compute P95 RTT (simple: take the max from recent samples as approximation).
		m.P95RTT = m.rttSamples[0]
		for _, s := range m.rttSamples {
			if s > m.P95RTT {
				m.P95RTT = s
			}
		}

		// Compute jitter as RTT standard deviation.
		if len(m.rttSamples) > 1 {
			avgNs := float64(m.AvgRTT.Nanoseconds())
			var sumSqDiff float64
			for _, s := range m.rttSamples {
				diff := float64(s.Nanoseconds()) - avgNs
				sumSqDiff += diff * diff
			}
			m.Jitter = time.Duration(math.Sqrt(sumSqDiff / float64(len(m.rttSamples))))
		}

		// Estimate throughput: probe payload size (53B) / half-RTT.
		const probePayloadSize = 53
		if rtt > 0 {
			m.ThroughputEstimate = float64(probePayloadSize) / rtt.Seconds() * 2
		}
	}

	// Update success rate with EWMA (alpha=0.3).
	const alpha = 0.3
	observed := 0.0
	if success {
		observed = 1.0
	}
	if m.SampleCount == 1 {
		m.SuccessRate = observed
	} else {
		m.SuccessRate = alpha*observed + (1-alpha)*m.SuccessRate
	}
}

// PathSet is a thread-safe collection of paths to a target peer.
type PathSet struct {
	mu    sync.RWMutex
	paths map[string]*Path // keyed by Path.ID
}

// NewPathSet creates an empty PathSet.
func NewPathSet() *PathSet {
	return &PathSet{
		paths: make(map[string]*Path),
	}
}

// Add adds or updates a path in the set.
func (ps *PathSet) Add(p *Path) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.paths[p.ID] = p
}

// Remove removes a path by ID.
func (ps *PathSet) Remove(id string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	delete(ps.paths, id)
}

// Get returns a path by ID.
func (ps *PathSet) Get(id string) (*Path, bool) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	p, ok := ps.paths[id]
	return p, ok
}

// All returns all paths in the set.
func (ps *PathSet) All() []*Path {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	result := make([]*Path, 0, len(ps.paths))
	for _, p := range ps.paths {
		result = append(result, p)
	}
	return result
}

// Best returns the best path according to the given policy.
// Returns nil if the set is empty.
func (ps *PathSet) Best(pol Policy) *Path {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var best *Path
	bestScore := -1.0

	for _, p := range ps.paths {
		score := pol.Score(p)
		if score > bestScore {
			bestScore = score
			best = p
		}
	}

	return best
}

// Len returns the number of paths.
func (ps *PathSet) Len() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.paths)
}
