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
	case "decaying-epsilon":
		return DefaultDecayingEpsilonGreedyPolicy(), nil
	case "ucb1":
		return DefaultUCB1Policy(), nil
	case "random":
		return RandomPolicy{}, nil
	default:
		return nil, fmt.Errorf("unknown policy: %q (valid: latency, hop-count, reliability, balanced, epsilon-greedy, decaying-epsilon, ucb1, random)", name)
	}
}

// DecayingEpsilonGreedyPolicy uses a decaying exploration rate that starts
// high (ε₀) and decays toward a minimum (ε_min) over time. This provides
// more exploration early (when path quality is uncertain) and converges
// toward exploitation as confidence grows.
//
// Formula: ε(t) = max(ε_min, ε₀ × decay^t)
// where t is the number of selections made.
type DecayingEpsilonGreedyPolicy struct {
	EpsilonStart float64 // initial exploration rate (default: 0.3)
	EpsilonMin   float64 // minimum exploration rate (default: 0.05)
	DecayRate    float64 // decay multiplier per selection (default: 0.995)
	Delegate     Policy  // scoring policy for exploitation

	selections uint64 // number of selections made (for decay)
}

// DefaultDecayingEpsilonGreedyPolicy returns a DecayingEpsilonGreedyPolicy
// with sensible defaults: ε starts at 0.3, decays to 0.05 with rate 0.995.
func DefaultDecayingEpsilonGreedyPolicy() *DecayingEpsilonGreedyPolicy {
	return &DecayingEpsilonGreedyPolicy{
		EpsilonStart: 0.3,
		EpsilonMin:   0.05,
		DecayRate:    0.995,
		Delegate:     DefaultBalancedPolicy(),
	}
}

func (d *DecayingEpsilonGreedyPolicy) Name() string { return "decaying-epsilon" }

func (d *DecayingEpsilonGreedyPolicy) Score(p *Path) float64 {
	return d.Delegate.Score(p)
}

// CurrentEpsilon returns the current exploration rate after decay.
func (d *DecayingEpsilonGreedyPolicy) CurrentEpsilon() float64 {
	eps := d.EpsilonStart * math.Pow(d.DecayRate, float64(d.selections))
	if eps < d.EpsilonMin {
		return d.EpsilonMin
	}
	return eps
}

// SelectPath picks a path using decaying epsilon-greedy strategy.
func (d *DecayingEpsilonGreedyPolicy) SelectPath(paths []*Path) *Path {
	if len(paths) == 0 {
		return nil
	}

	d.selections++
	epsilon := d.CurrentEpsilon()

	// Filter to viable paths.
	viable := make([]*Path, 0, len(paths))
	for _, p := range paths {
		if p.Metrics.SampleCount > 0 {
			viable = append(viable, p)
		}
	}
	if len(viable) == 0 {
		return paths[0]
	}

	// Explore with current epsilon.
	if len(viable) > 1 && rand.Float64() < epsilon {
		return viable[rand.IntN(len(viable))]
	}

	// Exploit: pick best-scored path.
	var best *Path
	bestScore := -1.0
	for _, p := range viable {
		score := d.Delegate.Score(p)
		if score > bestScore {
			bestScore = score
			best = p
		}
	}
	return best
}

// UCB1Policy implements the Upper Confidence Bound (UCB1) algorithm for
// path selection. UCB1 provides asymptotically optimal regret bounds,
// automatically balancing exploration and exploitation without a tunable
// epsilon parameter.
//
// UCB1 score: delegate_score(i) + c * sqrt(ln(N) / n_i)
// where N = total selections, n_i = selections for path i, c = exploration constant.
type UCB1Policy struct {
	C        float64 // exploration constant (default: sqrt(2) ≈ 1.414)
	Delegate Policy  // base scoring policy

	totalSelections uint64
	pathSelections  map[string]uint64
}

// DefaultUCB1Policy returns a UCB1Policy with default exploration constant sqrt(2).
func DefaultUCB1Policy() *UCB1Policy {
	return &UCB1Policy{
		C:              math.Sqrt(2),
		Delegate:       DefaultBalancedPolicy(),
		pathSelections: make(map[string]uint64),
	}
}

func (u *UCB1Policy) Name() string { return "ucb1" }

func (u *UCB1Policy) Score(p *Path) float64 {
	return u.Delegate.Score(p)
}

// SelectPath picks a path using the UCB1 algorithm.
func (u *UCB1Policy) SelectPath(paths []*Path) *Path {
	if len(paths) == 0 {
		return nil
	}

	// Filter to viable paths.
	viable := make([]*Path, 0, len(paths))
	for _, p := range paths {
		if p.Metrics.SampleCount > 0 {
			viable = append(viable, p)
		}
	}
	if len(viable) == 0 {
		return paths[0]
	}

	u.totalSelections++

	// First, select any path that hasn't been tried yet (infinite UCB score).
	for _, p := range viable {
		if u.pathSelections[p.ID] == 0 {
			u.pathSelections[p.ID] = 1
			return p
		}
	}

	// Compute UCB1 score for each path.
	var best *Path
	bestUCB := -1.0
	logN := math.Log(float64(u.totalSelections))

	for _, p := range viable {
		ni := float64(u.pathSelections[p.ID])
		delegateScore := u.Delegate.Score(p)
		ucbScore := delegateScore + u.C*math.Sqrt(logN/ni)

		if ucbScore > bestUCB {
			bestUCB = ucbScore
			best = p
		}
	}

	if best != nil {
		u.pathSelections[best.ID]++
	}
	return best
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
