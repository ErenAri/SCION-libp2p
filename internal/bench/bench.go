package bench

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/erena/scion-libp2p/internal/content"
	"github.com/erena/scion-libp2p/internal/protocol"
	"github.com/erena/scion-libp2p/testutil"
)

// Config configures a benchmark run.
type Config struct {
	NodeCount   int    `json:"node_count"`
	ContentSize int    `json:"content_size_bytes"` // bytes of test content
	Requests    int    `json:"requests"`           // number of fetch requests
	ChunkSize   int    `json:"chunk_size_bytes"`
	Policy      string `json:"policy"`  // path selection policy for this run
	Epsilon     float64 `json:"epsilon"` // epsilon for epsilon-greedy policy
}

// Results holds all benchmark results.
type Results struct {
	Config          Config          `json:"config"`
	Latency         LatencyResult   `json:"latency"`
	CacheComparison CacheResult     `json:"cache_comparison"`
	Resilience      ResilienceResult `json:"resilience"`
	TotalDuration   string          `json:"total_duration"`
}

// LatencyResult holds retrieval latency measurements.
type LatencyResult struct {
	AvgLatencyMs float64   `json:"avg_latency_ms"`
	P50LatencyMs float64   `json:"p50_latency_ms"`
	P95LatencyMs float64   `json:"p95_latency_ms"`
	P99LatencyMs float64   `json:"p99_latency_ms"`
	MinLatencyMs float64   `json:"min_latency_ms"`
	MaxLatencyMs float64   `json:"max_latency_ms"`
	ThroughputMBs float64  `json:"throughput_mbs"`
	Samples      int       `json:"samples"`
	AllMs        []float64 `json:"all_ms"`
}

// CacheResult compares performance with and without cache.
type CacheResult struct {
	CacheHits   int64   `json:"cache_hits"`
	CacheMisses int64   `json:"cache_misses"`
	HitRatio    float64 `json:"hit_ratio"`
}

// ResilienceResult shows content availability under node failures.
type ResilienceResult struct {
	TotalBlocks     int     `json:"total_blocks"`
	AvailableBlocks int     `json:"available_blocks"`
	Availability    float64 `json:"availability"`
	NodesKilled     int     `json:"nodes_killed"`
}

// ComparisonResult holds results for the three-way policy comparison.
type ComparisonResult struct {
	Configs []ComparisonEntry `json:"configs"`
}

// ComparisonEntry is one row in the comparison.
type ComparisonEntry struct {
	Policy        string  `json:"policy"`
	Epsilon       float64 `json:"epsilon,omitempty"`
	NodeCount     int     `json:"node_count"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
	P50LatencyMs  float64 `json:"p50_latency_ms"`
	P95LatencyMs  float64 `json:"p95_latency_ms"`
	P99LatencyMs  float64 `json:"p99_latency_ms"`
	ThroughputMBs float64 `json:"throughput_mbs"`
	CacheHitRatio float64 `json:"cache_hit_ratio"`
	Availability  float64 `json:"availability"`
	FairnessIndex float64 `json:"fairness_index"` // Jain's fairness index for path selection distribution
	StddevLatMs   float64 `json:"stddev_lat_ms"`  // Standard deviation of avg latency across runs
}

// Run executes the full benchmark suite.
func Run(cfg Config) (*Results, error) {
	start := time.Now()
	results := &Results{Config: cfg}

	slog.Info("starting benchmark", "nodes", cfg.NodeCount, "content_size", cfg.ContentSize, "policy", cfg.Policy)

	latencyResult, err := runLatencyBench(cfg)
	if err != nil {
		return nil, fmt.Errorf("latency bench: %w", err)
	}
	results.Latency = *latencyResult

	cacheResult, err := runCacheBench(cfg)
	if err != nil {
		return nil, fmt.Errorf("cache bench: %w", err)
	}
	results.CacheComparison = *cacheResult

	resilienceResult, err := runResilienceBench(cfg)
	if err != nil {
		return nil, fmt.Errorf("resilience bench: %w", err)
	}
	results.Resilience = *resilienceResult

	results.TotalDuration = time.Since(start).String()
	return results, nil
}

// RunComparison runs the policy comparison experiment.
// Tests epsilon-greedy, decaying-epsilon, ucb1, latency, and random policies.
func RunComparison(nodeCount, contentSize, requests, chunkSize int) (*ComparisonResult, error) {
	return RunComparisonWithRuns(nodeCount, contentSize, requests, chunkSize, 1)
}

// RunComparisonWithRuns runs the policy comparison with multiple runs per config.
// Results are averaged with standard deviation reported.
func RunComparisonWithRuns(nodeCount, contentSize, requests, chunkSize, runs int) (*ComparisonResult, error) {
	if runs < 1 {
		runs = 1
	}

	policies := []struct {
		name    string
		epsilon float64
	}{
		{"epsilon-greedy", 0.1},
		{"decaying-epsilon", 0},
		{"ucb1", 0},
		{"latency", 0},
		{"random", 0},
	}

	result := &ComparisonResult{}

	for _, pol := range policies {
		slog.Info("running comparison", "policy", pol.name, "nodes", nodeCount, "runs", runs)

		var avgLats, p50s, p95s, p99s, throughputs, cacheRatios, avails []float64

		for r := 0; r < runs; r++ {
			cfg := Config{
				NodeCount:   nodeCount,
				ContentSize: contentSize,
				Requests:    requests,
				ChunkSize:   chunkSize,
				Policy:      pol.name,
				Epsilon:     pol.epsilon,
			}

			res, err := Run(cfg)
			if err != nil {
				slog.Error("comparison run failed", "policy", pol.name, "run", r+1, "err", err)
				continue
			}

			avgLats = append(avgLats, res.Latency.AvgLatencyMs)
			p50s = append(p50s, res.Latency.P50LatencyMs)
			p95s = append(p95s, res.Latency.P95LatencyMs)
			p99s = append(p99s, res.Latency.P99LatencyMs)
			throughputs = append(throughputs, res.Latency.ThroughputMBs)
			cacheRatios = append(cacheRatios, res.CacheComparison.HitRatio)
			avails = append(avails, res.Resilience.Availability)
		}

		if len(avgLats) == 0 {
			continue
		}

		entry := ComparisonEntry{
			Policy:        pol.name,
			Epsilon:       pol.epsilon,
			NodeCount:     nodeCount,
			AvgLatencyMs:  mean(avgLats),
			P50LatencyMs:  mean(p50s),
			P95LatencyMs:  mean(p95s),
			P99LatencyMs:  mean(p99s),
			ThroughputMBs: mean(throughputs),
			CacheHitRatio: mean(cacheRatios),
			Availability:  mean(avails),
			StddevLatMs:   stddev(avgLats),
			FairnessIndex: jainFairness(avgLats),
		}
		result.Configs = append(result.Configs, entry)
	}

	return result, nil
}

// RunScalability runs the comparison at multiple node counts.
func RunScalability(nodeCounts []int, contentSize, requests, chunkSize, runs int) (*ComparisonResult, error) {
	result := &ComparisonResult{}

	for _, n := range nodeCounts {
		slog.Info("scalability experiment", "nodes", n)
		comp, err := RunComparisonWithRuns(n, contentSize, requests, chunkSize, runs)
		if err != nil {
			slog.Error("scalability run failed", "nodes", n, "err", err)
			continue
		}
		result.Configs = append(result.Configs, comp.Configs...)
	}

	return result, nil
}

// WriteJSON writes results as formatted JSON to the given path.
func (r *Results) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// WriteCSV writes comparison results as CSV.
func (r *ComparisonResult) WriteCSV(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	// Header.
	w.Write([]string{
		"policy", "epsilon", "node_count",
		"avg_latency_ms", "p50_latency_ms", "p95_latency_ms", "p99_latency_ms",
		"throughput_mbs", "cache_hit_ratio", "availability",
		"fairness_index", "stddev_latency_ms",
	})

	for _, e := range r.Configs {
		w.Write([]string{
			e.Policy,
			strconv.FormatFloat(e.Epsilon, 'f', 2, 64),
			strconv.Itoa(e.NodeCount),
			strconv.FormatFloat(e.AvgLatencyMs, 'f', 2, 64),
			strconv.FormatFloat(e.P50LatencyMs, 'f', 2, 64),
			strconv.FormatFloat(e.P95LatencyMs, 'f', 2, 64),
			strconv.FormatFloat(e.P99LatencyMs, 'f', 2, 64),
			strconv.FormatFloat(e.ThroughputMBs, 'f', 4, 64),
			strconv.FormatFloat(e.CacheHitRatio, 'f', 4, 64),
			strconv.FormatFloat(e.Availability, 'f', 4, 64),
			strconv.FormatFloat(e.FairnessIndex, 'f', 4, 64),
			strconv.FormatFloat(e.StddevLatMs, 'f', 2, 64),
		})
	}

	return nil
}

// WriteJSON writes comparison results as JSON.
func (r *ComparisonResult) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// --- Benchmark implementations ---

func runLatencyBench(cfg Config) (*LatencyResult, error) {
	cluster, cleanup, err := createBenchCluster(cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	testData := generateTestData(cfg.ContentSize)
	blocks, manifest, err := publishContent(cluster, 0, testData, cfg.ChunkSize)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
	_ = blocks

	var latencies []float64
	ctx := context.Background()
	totalBytes := int64(0)
	benchStart := time.Now()

	for i := 0; i < cfg.Requests; i++ {
		fetcherIdx := (i % (cfg.NodeCount - 1)) + 1
		fetcherHost := cluster.Nodes[fetcherIdx].Host
		publisherID := cluster.Nodes[0].Host.ID()

		start := time.Now()
		for _, cid := range manifest.ChunkCIDs {
			block, err := protocol.FetchBlock(ctx, fetcherHost, publisherID, cid)
			if err != nil {
				slog.Debug("latency fetch error", "err", err)
			} else {
				totalBytes += int64(len(block.Data))
			}
		}
		elapsed := time.Since(start)
		latencies = append(latencies, float64(elapsed.Milliseconds()))
	}

	benchDuration := time.Since(benchStart)

	result := &LatencyResult{
		Samples: len(latencies),
		AllMs:   latencies,
	}

	if len(latencies) > 0 {
		sorted := make([]float64, len(latencies))
		copy(sorted, latencies)
		sort.Float64s(sorted)

		result.MinLatencyMs = sorted[0]
		result.MaxLatencyMs = sorted[len(sorted)-1]
		result.P50LatencyMs = percentile(sorted, 0.50)
		result.P95LatencyMs = percentile(sorted, 0.95)
		result.P99LatencyMs = percentile(sorted, 0.99)

		var sum float64
		for _, l := range latencies {
			sum += l
		}
		result.AvgLatencyMs = sum / float64(len(latencies))
	}

	if benchDuration.Seconds() > 0 {
		result.ThroughputMBs = float64(totalBytes) / benchDuration.Seconds() / (1024 * 1024)
	}

	slog.Info("latency benchmark complete",
		"policy", cfg.Policy,
		"avg_ms", fmt.Sprintf("%.1f", result.AvgLatencyMs),
		"p50_ms", fmt.Sprintf("%.1f", result.P50LatencyMs),
		"p95_ms", fmt.Sprintf("%.1f", result.P95LatencyMs),
		"throughput_mbs", fmt.Sprintf("%.2f", result.ThroughputMBs),
		"samples", result.Samples,
	)

	return result, nil
}

func runCacheBench(cfg Config) (*CacheResult, error) {
	cluster, cleanup, err := createBenchCluster(cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	testData := generateTestData(cfg.ContentSize)
	_, manifest, err := publishContent(cluster, 0, testData, cfg.ChunkSize)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}

	ctx := context.Background()
	publisherID := cluster.Nodes[0].Host.ID()

	// Fetch all blocks twice — second time should hit cache.
	for round := 0; round < 2; round++ {
		fetcherHost := cluster.Nodes[1].Host
		for _, cid := range manifest.ChunkCIDs {
			protocol.FetchBlock(ctx, fetcherHost, publisherID, cid)
		}
	}

	stats := cluster.Nodes[0].BlockCache.Stats()

	result := &CacheResult{
		CacheHits:   stats.Hits,
		CacheMisses: stats.Misses,
	}
	total := stats.Hits + stats.Misses
	if total > 0 {
		result.HitRatio = float64(stats.Hits) / float64(total)
	}

	slog.Info("cache benchmark complete",
		"hits", result.CacheHits,
		"misses", result.CacheMisses,
		"hit_ratio", fmt.Sprintf("%.1f%%", result.HitRatio*100),
	)

	return result, nil
}

func runResilienceBench(cfg Config) (*ResilienceResult, error) {
	if cfg.NodeCount < 3 {
		return &ResilienceResult{}, nil
	}

	cluster, cleanup, err := createBenchCluster(cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	testData := generateTestData(cfg.ContentSize)
	blocks, _, err := publishContent(cluster, 0, testData, cfg.ChunkSize)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}

	// Replicate blocks to all nodes.
	for i := 1; i < cfg.NodeCount; i++ {
		for _, b := range blocks {
			cluster.Nodes[i].ContentStore.Put(b)
		}
	}

	// Kill ~30% of nodes.
	killCount := max(1, cfg.NodeCount/3)
	killed := 0
	for i := cfg.NodeCount - 1; i >= 1 && killed < killCount; i-- {
		cluster.Nodes[i].Host.Close()
		killed++
	}

	ctx := context.Background()
	available := 0

	for _, b := range blocks {
		_, err := cluster.Nodes[0].ContentStore.Get(b.CID)
		if err == nil {
			available++
			continue
		}
		for j := 1; j < cfg.NodeCount-killed; j++ {
			_, err := protocol.FetchBlock(ctx, cluster.Nodes[0].Host, cluster.Nodes[j].Host.ID(), b.CID)
			if err == nil {
				available++
				break
			}
		}
	}

	result := &ResilienceResult{
		TotalBlocks:     len(blocks),
		AvailableBlocks: available,
		NodesKilled:     killed,
	}
	if len(blocks) > 0 {
		result.Availability = float64(available) / float64(len(blocks))
	}

	slog.Info("resilience benchmark complete",
		"available", available,
		"total", len(blocks),
		"availability", fmt.Sprintf("%.1f%%", result.Availability*100),
		"nodes_killed", killed,
	)

	return result, nil
}

// --- Helpers ---

func createBenchCluster(cfg Config) (*testutil.Cluster, func(), error) {
	opts := testutil.ClusterOptions{
		Policy:      cfg.Policy,
		Epsilon:     cfg.Epsilon,
		DisableMDNS: true, // benchmarks connect nodes manually; mDNS causes shutdown races
	}
	cluster := testutil.NewClusterWithOptions(cfg.NodeCount, opts)
	if cluster == nil {
		return nil, nil, fmt.Errorf("failed to create cluster")
	}

	cleanup := func() {
		cluster.Cleanup()
	}

	// Wait for mesh formation.
	time.Sleep(2 * time.Second)
	return cluster, cleanup, nil
}

func generateTestData(size int) []byte {
	data := make([]byte, size)
	rng := rand.New(rand.NewSource(42))
	rng.Read(data)
	return data
}

func publishContent(cluster *testutil.Cluster, nodeIdx int, data []byte, chunkSize int) ([]content.Block, content.Manifest, error) {
	if chunkSize <= 0 {
		chunkSize = 256 * 1024
	}

	blocks, err := content.Chunk(bytes.NewReader(data), chunkSize)
	if err != nil {
		return nil, content.Manifest{}, err
	}

	store := cluster.Nodes[nodeIdx].ContentStore
	for _, b := range blocks {
		if err := store.Put(b); err != nil {
			return nil, content.Manifest{}, err
		}
	}

	manifest := content.BuildManifest("bench-data", int64(len(data)), blocks)
	if err := store.PutManifest(manifest); err != nil {
		return nil, content.Manifest{}, err
	}

	return blocks, manifest, nil
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper || upper >= len(sorted) {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

// mean computes the arithmetic mean of a float64 slice.
func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

// stddev computes the sample standard deviation.
func stddev(vals []float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	m := mean(vals)
	var sumSq float64
	for _, v := range vals {
		d := v - m
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(vals)-1))
}

// jainFairness computes Jain's fairness index: J = (Σx)² / (n × Σx²).
// Returns 1.0 for perfectly fair (all equal), 1/n for maximally unfair.
func jainFairness(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum, sumSq float64
	for _, v := range vals {
		sum += v
		sumSq += v * v
	}
	n := float64(len(vals))
	if sumSq == 0 {
		return 1.0
	}
	return (sum * sum) / (n * sumSq)
}
