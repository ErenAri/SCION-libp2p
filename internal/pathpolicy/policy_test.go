package pathpolicy_test

import (
	"testing"
	"time"

	"github.com/erena/scion-libp2p/internal/pathpolicy"
)

func TestLatencyPolicy(t *testing.T) {
	pol := pathpolicy.LatencyPolicy{}

	fast := &pathpolicy.Path{
		ID:   "fast",
		Type: pathpolicy.PathDirect,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      5 * time.Millisecond,
			SampleCount: 5,
		},
	}
	slow := &pathpolicy.Path{
		ID:   "slow",
		Type: pathpolicy.PathRelay,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      100 * time.Millisecond,
			SampleCount: 5,
		},
	}

	fastScore := pol.Score(fast)
	slowScore := pol.Score(slow)

	if fastScore <= slowScore {
		t.Errorf("expected fast path score (%f) > slow path score (%f)", fastScore, slowScore)
	}
}

func TestHopCountPolicy(t *testing.T) {
	pol := pathpolicy.HopCountPolicy{}

	direct := &pathpolicy.Path{
		ID:   "direct",
		Type: pathpolicy.PathDirect,
		Metrics: pathpolicy.PathMetrics{
			HopCount:    0,
			SampleCount: 5,
		},
	}
	relayed := &pathpolicy.Path{
		ID:   "relayed",
		Type: pathpolicy.PathRelay,
		Metrics: pathpolicy.PathMetrics{
			HopCount:    2,
			SampleCount: 5,
		},
	}

	directScore := pol.Score(direct)
	relayedScore := pol.Score(relayed)

	if directScore <= relayedScore {
		t.Errorf("expected direct path score (%f) > relayed path score (%f)", directScore, relayedScore)
	}
}

func TestReliabilityPolicy(t *testing.T) {
	pol := pathpolicy.ReliabilityPolicy{}

	reliable := &pathpolicy.Path{
		ID:   "reliable",
		Type: pathpolicy.PathDirect,
		Metrics: pathpolicy.PathMetrics{
			SuccessRate: 0.99,
			SampleCount: 100,
		},
	}
	unreliable := &pathpolicy.Path{
		ID:   "unreliable",
		Type: pathpolicy.PathRelay,
		Metrics: pathpolicy.PathMetrics{
			SuccessRate: 0.5,
			SampleCount: 100,
		},
	}

	reliableScore := pol.Score(reliable)
	unreliableScore := pol.Score(unreliable)

	if reliableScore <= unreliableScore {
		t.Errorf("expected reliable score (%f) > unreliable score (%f)", reliableScore, unreliableScore)
	}
}

func TestBalancedPolicy(t *testing.T) {
	pol := pathpolicy.DefaultBalancedPolicy()

	// Good on all dimensions.
	good := &pathpolicy.Path{
		ID:   "good",
		Type: pathpolicy.PathDirect,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      5 * time.Millisecond,
			HopCount:    0,
			SuccessRate: 0.99,
			SampleCount: 50,
		},
	}
	// Bad on all dimensions.
	bad := &pathpolicy.Path{
		ID:   "bad",
		Type: pathpolicy.PathRelay,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      500 * time.Millisecond,
			HopCount:    3,
			SuccessRate: 0.5,
			SampleCount: 50,
		},
	}

	goodScore := pol.Score(good)
	badScore := pol.Score(bad)

	if goodScore <= badScore {
		t.Errorf("expected good path score (%f) > bad path score (%f)", goodScore, badScore)
	}
}

func TestPathSetBest(t *testing.T) {
	ps := pathpolicy.NewPathSet()

	fast := &pathpolicy.Path{
		ID:   "fast",
		Type: pathpolicy.PathDirect,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      2 * time.Millisecond,
			SampleCount: 10,
		},
	}
	slow := &pathpolicy.Path{
		ID:   "slow",
		Type: pathpolicy.PathRelay,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      200 * time.Millisecond,
			SampleCount: 10,
		},
	}

	ps.Add(fast)
	ps.Add(slow)

	best := ps.Best(pathpolicy.LatencyPolicy{})
	if best == nil {
		t.Fatal("expected a best path, got nil")
	}
	if best.ID != "fast" {
		t.Errorf("expected best path to be 'fast', got %q", best.ID)
	}
}

func TestPathSetEmpty(t *testing.T) {
	ps := pathpolicy.NewPathSet()
	best := ps.Best(pathpolicy.LatencyPolicy{})
	if best != nil {
		t.Error("expected nil for empty path set")
	}
}

func TestRecordProbe(t *testing.T) {
	m := pathpolicy.PathMetrics{}

	// Record 5 successful probes.
	for i := 0; i < 5; i++ {
		m.RecordProbe(10*time.Millisecond, true)
	}

	if m.SampleCount != 5 {
		t.Errorf("expected 5 samples, got %d", m.SampleCount)
	}
	if m.AvgRTT != 10*time.Millisecond {
		t.Errorf("expected avg RTT 10ms, got %v", m.AvgRTT)
	}
	if m.SuccessRate < 0.9 {
		t.Errorf("expected high success rate, got %f", m.SuccessRate)
	}

	// Record a failure.
	m.RecordProbe(0, false)
	if m.SuccessRate >= 1.0 {
		t.Errorf("expected success rate < 1.0 after failure, got %f", m.SuccessRate)
	}
}

func TestPolicyFromName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"latency", false},
		{"hop-count", false},
		{"reliability", false},
		{"balanced", false},
		{"epsilon-greedy", false},
		{"unknown", true},
	}

	for _, tc := range cases {
		p, err := pathpolicy.PolicyFromName(tc.name)
		if tc.wantErr {
			if err == nil {
				t.Errorf("PolicyFromName(%q): expected error", tc.name)
			}
		} else {
			if err != nil {
				t.Errorf("PolicyFromName(%q): unexpected error: %v", tc.name, err)
			}
			if p == nil {
				t.Errorf("PolicyFromName(%q): expected non-nil policy", tc.name)
			}
		}
	}
}

func TestEpsilonGreedyPolicy(t *testing.T) {
	pol := pathpolicy.DefaultEpsilonGreedyPolicy()

	if pol.Name() != "epsilon-greedy" {
		t.Errorf("expected name 'epsilon-greedy', got %q", pol.Name())
	}

	// Score should delegate to the balanced policy.
	path := &pathpolicy.Path{
		ID:   "test",
		Type: pathpolicy.PathDirect,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      5 * time.Millisecond,
			HopCount:    0,
			SuccessRate: 0.99,
			SampleCount: 10,
		},
	}

	score := pol.Score(path)
	if score <= 0 {
		t.Errorf("expected positive score, got %f", score)
	}
}

func TestEpsilonGreedySelectPath(t *testing.T) {
	pol := pathpolicy.DefaultEpsilonGreedyPolicy()

	fast := &pathpolicy.Path{
		ID:   "fast",
		Type: pathpolicy.PathDirect,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      2 * time.Millisecond,
			HopCount:    0,
			SuccessRate: 0.99,
			SampleCount: 20,
		},
	}
	slow := &pathpolicy.Path{
		ID:   "slow",
		Type: pathpolicy.PathRelay,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      200 * time.Millisecond,
			HopCount:    2,
			SuccessRate: 0.8,
			SampleCount: 20,
		},
	}

	paths := []*pathpolicy.Path{fast, slow}

	// Run many selections. With ε=0.1, we should mostly get "fast"
	// but occasionally get "slow".
	fastCount := 0
	slowCount := 0
	n := 1000

	for i := 0; i < n; i++ {
		selected := pol.SelectPath(paths)
		if selected == nil {
			t.Fatal("SelectPath returned nil")
		}
		switch selected.ID {
		case "fast":
			fastCount++
		case "slow":
			slowCount++
		}
	}

	// Expect ~90% fast, ~10% slow (with tolerance).
	fastRatio := float64(fastCount) / float64(n)
	if fastRatio < 0.75 || fastRatio > 0.98 {
		t.Errorf("expected ~90%% fast selections, got %.1f%% (fast=%d, slow=%d)",
			fastRatio*100, fastCount, slowCount)
	}

	// The slow path should be selected at least sometimes (exploration).
	if slowCount == 0 {
		t.Error("epsilon-greedy never explored the slow path — exploration is broken")
	}

	t.Logf("epsilon-greedy distribution: fast=%.1f%% slow=%.1f%% (n=%d)",
		fastRatio*100, float64(slowCount)/float64(n)*100, n)
}

func TestEpsilonGreedySelectPathEmpty(t *testing.T) {
	pol := pathpolicy.DefaultEpsilonGreedyPolicy()

	if p := pol.SelectPath(nil); p != nil {
		t.Error("expected nil for empty path list")
	}

	if p := pol.SelectPath([]*pathpolicy.Path{}); p != nil {
		t.Error("expected nil for empty path list")
	}
}

func TestDecayingEpsilonGreedyPolicy(t *testing.T) {
	pol := pathpolicy.DefaultDecayingEpsilonGreedyPolicy()

	if pol.Name() != "decaying-epsilon" {
		t.Errorf("expected name 'decaying-epsilon', got %q", pol.Name())
	}

	// Initial epsilon should be near EpsilonStart (0.3).
	if eps := pol.CurrentEpsilon(); eps < 0.29 || eps > 0.31 {
		t.Errorf("expected initial epsilon ~0.3, got %f", eps)
	}

	fast := &pathpolicy.Path{
		ID:   "fast",
		Type: pathpolicy.PathDirect,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      2 * time.Millisecond,
			HopCount:    0,
			SuccessRate: 0.99,
			SampleCount: 20,
		},
	}
	slow := &pathpolicy.Path{
		ID:   "slow",
		Type: pathpolicy.PathRelay,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      200 * time.Millisecond,
			HopCount:    2,
			SuccessRate: 0.8,
			SampleCount: 20,
		},
	}

	paths := []*pathpolicy.Path{fast, slow}

	// Make many selections -- epsilon should decay.
	for i := 0; i < 500; i++ {
		selected := pol.SelectPath(paths)
		if selected == nil {
			t.Fatal("SelectPath returned nil")
		}
	}

	// After 500 selections, epsilon should have decayed significantly.
	eps := pol.CurrentEpsilon()
	if eps > 0.1 {
		t.Errorf("expected decayed epsilon < 0.1 after 500 selections, got %f", eps)
	}
	if eps < pol.EpsilonMin {
		t.Errorf("epsilon %f should not be below minimum %f", eps, pol.EpsilonMin)
	}
}

func TestUCB1Policy(t *testing.T) {
	pol := pathpolicy.DefaultUCB1Policy()

	if pol.Name() != "ucb1" {
		t.Errorf("expected name 'ucb1', got %q", pol.Name())
	}

	fast := &pathpolicy.Path{
		ID:   "fast",
		Type: pathpolicy.PathDirect,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      2 * time.Millisecond,
			HopCount:    0,
			SuccessRate: 0.99,
			SampleCount: 20,
		},
	}
	slow := &pathpolicy.Path{
		ID:   "slow",
		Type: pathpolicy.PathRelay,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      200 * time.Millisecond,
			HopCount:    2,
			SuccessRate: 0.8,
			SampleCount: 20,
		},
	}

	paths := []*pathpolicy.Path{fast, slow}

	// UCB1 should first try each path once, then mostly exploit "fast".
	fastCount := 0
	slowCount := 0
	n := 1000

	for i := 0; i < n; i++ {
		selected := pol.SelectPath(paths)
		if selected == nil {
			t.Fatal("SelectPath returned nil")
		}
		switch selected.ID {
		case "fast":
			fastCount++
		case "slow":
			slowCount++
		}
	}

	// UCB1 should predominantly select the fast path but still explore slow.
	fastRatio := float64(fastCount) / float64(n)
	if fastRatio < 0.7 {
		t.Errorf("expected UCB1 to favor fast path (got %.1f%% fast, %.1f%% slow)",
			fastRatio*100, float64(slowCount)/float64(n)*100)
	}
	if slowCount == 0 {
		t.Error("UCB1 never explored the slow path")
	}

	t.Logf("UCB1 distribution: fast=%.1f%% slow=%.1f%% (n=%d)",
		fastRatio*100, float64(slowCount)/float64(n)*100, n)
}

func TestPolicyFromNameNewPolicies(t *testing.T) {
	for _, name := range []string{"decaying-epsilon", "ucb1"} {
		p, err := pathpolicy.PolicyFromName(name)
		if err != nil {
			t.Errorf("PolicyFromName(%q): unexpected error: %v", name, err)
		}
		if p == nil {
			t.Errorf("PolicyFromName(%q): expected non-nil policy", name)
		}
	}
}

func TestEpsilonGreedySelectPathSingle(t *testing.T) {
	pol := pathpolicy.DefaultEpsilonGreedyPolicy()

	only := &pathpolicy.Path{
		ID:   "only",
		Type: pathpolicy.PathDirect,
		Metrics: pathpolicy.PathMetrics{
			AvgRTT:      5 * time.Millisecond,
			SuccessRate: 1.0,
			SampleCount: 10,
		},
	}

	for i := 0; i < 100; i++ {
		selected := pol.SelectPath([]*pathpolicy.Path{only})
		if selected == nil || selected.ID != "only" {
			t.Fatal("expected 'only' path to always be selected when it's the only option")
		}
	}
}
