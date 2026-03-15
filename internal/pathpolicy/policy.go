package pathpolicy

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"
)

// Policy defines how paths are scored for selection.
type Policy interface {
	// Score returns a score for the path. Higher is better.
	Score(p *Path) float64
	// Name returns the policy name.
	Name() string
}

// LatencyPolicy scores paths inversely proportional to average RTT.
type LatencyPolicy struct{}

func (LatencyPolicy) Name() string { return "latency" }

func (LatencyPolicy) Score(p *Path) float64 {
	if p.Metrics.AvgRTT <= 0 || p.Metrics.SampleCount == 0 {
		return 0
	}
	return 1.0 / p.Metrics.AvgRTT.Seconds()
}

// HopCountPolicy scores paths inversely proportional to hop count.
type HopCountPolicy struct{}

func (HopCountPolicy) Name() string { return "hop-count" }

func (HopCountPolicy) Score(p *Path) float64 {
	if p.Metrics.SampleCount == 0 {
		return 0
	}
	return 1.0 / float64(p.Metrics.HopCount+1)
}

// ReliabilityPolicy scores paths by their success rate.
type ReliabilityPolicy struct{}

func (ReliabilityPolicy) Name() string { return "reliability" }

func (ReliabilityPolicy) Score(p *Path) float64 {
	if p.Metrics.SampleCount == 0 {
		return 0
	}
	return p.Metrics.SuccessRate
}

// BalancedPolicy combines latency, reliability, hop-count, and jitter with weights.
type BalancedPolicy struct {
	LatencyWeight     float64
	ReliabilityWeight float64
	HopCountWeight    float64
	JitterWeight      float64
}

// DefaultBalancedPolicy returns a BalancedPolicy with default weights.
func DefaultBalancedPolicy() BalancedPolicy {
	return BalancedPolicy{
		LatencyWeight:     0.35,
		ReliabilityWeight: 0.25,
		HopCountWeight:    0.25,
		JitterWeight:      0.15,
	}
}

func (b BalancedPolicy) Name() string { return "balanced" }

func (b BalancedPolicy) Score(p *Path) float64 {
	if p.Metrics.SampleCount == 0 {
		return 0
	}

	// Normalize each component to [0, 1] range.
	latencyScore := 0.0
	if p.Metrics.AvgRTT > 0 {
		// Use inverse log scale so small RTT differences still matter.
		latencyScore = 1.0 / (1.0 + math.Log1p(p.Metrics.AvgRTT.Seconds()*1000))
	}

	hopScore := 1.0 / float64(p.Metrics.HopCount+1)
	reliabilityScore := p.Metrics.SuccessRate

	// Jitter score: lower jitter is better. Normalize using inverse log scale.
	jitterScore := 1.0
	if p.Metrics.Jitter > 0 {
		jitterScore = 1.0 / (1.0 + math.Log1p(p.Metrics.Jitter.Seconds()*1000))
	}

	return b.LatencyWeight*latencyScore +
		b.ReliabilityWeight*reliabilityScore +
		b.HopCountWeight*hopScore +
		b.JitterWeight*jitterScore
}

// EpsilonGreedyPolicy selects the best path with probability (1-epsilon),
// and a random path with probability epsilon. This avoids the "herd effect"
// where all agents pile onto the lowest-latency path, causing catastrophic
// congestion. Based on: "An Axiomatic Analysis of Path Selection Strategies
// for Multipath Transport in Path-Aware Networks" (arXiv 2509.05938, 2025).
type EpsilonGreedyPolicy struct {
	Epsilon  float64 // exploration probability (recommended: 0.1)
	Delegate Policy  // scoring policy for exploitation (e.g., BalancedPolicy)
}

// DefaultEpsilonGreedyPolicy returns an EpsilonGreedyPolicy with ε=0.1
// and a BalancedPolicy as the delegate scorer.
func DefaultEpsilonGreedyPolicy() EpsilonGreedyPolicy {
	return EpsilonGreedyPolicy{
		Epsilon:  0.1,
		Delegate: DefaultBalancedPolicy(),
	}
}

func (e EpsilonGreedyPolicy) Name() string { return "epsilon-greedy" }

// Score returns the delegate's score. The randomized selection happens in
// PathSet.BestEpsilonGreedy, not here, so Score remains deterministic for
// path ranking and display purposes.
func (e EpsilonGreedyPolicy) Score(p *Path) float64 {
	return e.Delegate.Score(p)
}

// SelectPath picks a path using epsilon-greedy strategy from the given set.
// With probability (1-epsilon), returns the highest-scored path.
// With probability epsilon, returns a uniformly random viable path.
func (e EpsilonGreedyPolicy) SelectPath(paths []*Path) *Path {
	if len(paths) == 0 {
		return nil
	}

	// Filter to viable paths (at least one probe sample).
	viable := make([]*Path, 0, len(paths))
	for _, p := range paths {
		if p.Metrics.SampleCount > 0 {
			viable = append(viable, p)
		}
	}
	if len(viable) == 0 {
		return paths[0] // fallback: return any path
	}

	// Explore: pick random path with probability epsilon.
	if len(viable) > 1 && rand.Float64() < e.Epsilon {
		return viable[rand.IntN(len(viable))]
	}

	// Exploit: pick the best-scored path.
	var best *Path
	bestScore := -1.0
	for _, p := range viable {
		score := e.Delegate.Score(p)
		if score > bestScore {
			bestScore = score
			best = p
		}
	}
	return best
}

// StalePathTimeout is the duration after which a path with no successful
// probes is considered stale and should be pruned. Based on SCIONLab
// measurement data showing average path lifetimes of ~8.6 hours.
const StalePathTimeout = 30 * time.Minute

// PolicyFromName returns a Policy by name. Returns an error for unknown names.
func PolicyFromName(name string) (Policy, error) {
	return PolicyFromNameWithEpsilon(name, 0.1)
}

// PolicyFromNameWithEpsilon returns a Policy by name with a configurable epsilon
// for the epsilon-greedy policy. For other policies, epsilon is ignored.
func PolicyFromNameWithEpsilon(name string, epsilon float64) (Policy, error) {
	switch strings.ToLower(name) {
	case "latency":
		return LatencyPolicy{}, nil
	case "hop-count":
		return HopCountPolicy{}, nil
	case "reliability":
		return ReliabilityPolicy{}, nil
	case "balanced":
		return DefaultBalancedPolicy(), nil
	case "epsilon-greedy":
		return EpsilonGreedyPolicy{
			Epsilon:  epsilon,
			Delegate: DefaultBalancedPolicy(),
		}, nil
	case "random":
		return RandomPolicy{}, nil
	default:
		return nil, fmt.Errorf("unknown policy: %q (valid: latency, hop-count, reliability, balanced, epsilon-greedy, random)", name)
	}
}

// RandomPolicy selects paths uniformly at random (baseline for evaluation).
type RandomPolicy struct{}

func (RandomPolicy) Name() string { return "random" }

func (RandomPolicy) Score(p *Path) float64 {
	if p.Metrics.SampleCount == 0 {
		return 0
	}
	return rand.Float64()
}
