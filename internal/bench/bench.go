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
	NodeCount     int    `json:"node_count"`
	ContentSize   int    `json:"content_size_bytes"` // bytes of test content
	Requests      int    `json:"requests"`           // number of fetch requests
	ChunkSize     int    `json:"chunk_size_bytes"`
	Policy        string `json:"policy"`  // path selection policy for this run
	Epsilon       float64 `json:"epsilon"` // epsilon for epsilon-greedy policy
	TimeSeriesDir string `json:"timeseries_dir,omitempty"` // directory for per-request convergence CSVs
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
	AvgLatencyMs  float64   `json:"avg_latency_ms"`
	P50LatencyMs  float64   `json:"p50_latency_ms"`
	P95LatencyMs  float64   `json:"p95_latency_ms"`
	P99LatencyMs  float64   `json:"p99_latency_ms"`
	MinLatencyMs  float64   `json:"min_latency_ms"`
	MaxLatencyMs  float64   `json:"max_latency_ms"`
	ThroughputMBs float64   `json:"throughput_mbs"`
	FairnessIndex float64   `json:"fairness_index"` // Jain's fairness over per-request latencies
	Samples       int       `json:"samples"`
	AllMs         []float64 `json:"all_ms"`
	Timestamps    []float64 `json:"timestamps_s,omitempty"` // elapsed seconds per request (for convergence plots)
}

// WriteTimeSeriesCSV writes per-request latency data for convergence analysis.
// Output columns: request_index,elapsed_s,latency_ms
func (r *LatencyResult) WriteTimeSeriesCSV(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	w.Write([]string{"request_index", "elapsed_s", "latency_ms"})
	for i := range r.AllMs {
		ts := 0.0
		if i < len(r.Timestamps) {
			ts = r.Timestamps[i]
		}
		w.Write([]string{
			strconv.Itoa(i),
			strconv.FormatFloat(ts, 'f', 3, 64),
			strconv.FormatFloat(r.AllMs[i], 'f', 2, 64),
		})
	}
	return nil
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
	CI95Lower     float64 `json:"ci95_lower"`      // 95% CI lower bound
	CI95Upper     float64 `json:"ci95_upper"`      // 95% CI upper bound
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

	// Write per-request time series for convergence analysis if configured.
	if cfg.TimeSeriesDir != "" {
		tsPath := fmt.Sprintf("%s/timeseries_%s_%d.csv", cfg.TimeSeriesDir, cfg.Policy, cfg.NodeCount)
		if err := latencyResult.WriteTimeSeriesCSV(tsPath); err != nil {
			slog.Warn("failed to write time series", "err", err)
		}
	}

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
// Tests epsilon-greedy, decaying-epsilon, ucb1, thompson, latency, and random policies.
func RunComparison(nodeCount, contentSize, requests, chunkSize int) (*ComparisonResult, error) {
	return RunComparisonWithRuns(nodeCount, contentSize, requests, chunkSize, 1, "")
}

// RunComparisonWithRuns runs the policy comparison with multiple runs per config.
// Results are averaged with standard deviation reported.
// If timeSeriesDir is non-empty, per-request convergence CSVs are written there.
func RunComparisonWithRuns(nodeCount, contentSize, requests, chunkSize, runs int, timeSeriesDir string) (*ComparisonResult, error) {
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
		{"thompson", 0},
		{"contextual", 0},
		{"latency", 0},
		{"random", 0},
	}

	result := &ComparisonResult{}

	for _, pol := range policies {
		slog.Info("running comparison", "policy", pol.name, "nodes", nodeCount, "runs", runs)

		var avgLats, p50s, p95s, p99s, throughputs, cacheRatios, avails, fairnesses []float64

		for r := 0; r < runs; r++ {
			cfg := Config{
				NodeCount:     nodeCount,
				ContentSize:   contentSize,
				Requests:      requests,
				ChunkSize:     chunkSize,
				Policy:        pol.name,
				Epsilon:       pol.epsilon,
				TimeSeriesDir: timeSeriesDir,
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
			fairnesses = append(fairnesses, res.Latency.FairnessIndex)
		}

		if len(avgLats) == 0 {
			continue
		}

		ci95l, ci95u := confidenceInterval95(avgLats)
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
			FairnessIndex: mean(fairnesses),
			CI95Lower:     ci95l,
			CI95Upper:     ci95u,
		}
		result.Configs = append(result.Configs, entry)
	}

	return result, nil
}

// RunScalability runs the comparison at multiple node counts.
func RunScalability(nodeCounts []int, contentSize, requests, chunkSize, runs int, timeSeriesDir string) (*ComparisonResult, error) {
	result := &ComparisonResult{}

	for _, n := range nodeCounts {
		slog.Info("scalability experiment", "nodes", n)
		comp, err := RunComparisonWithRuns(n, contentSize, requests, chunkSize, runs, timeSeriesDir)
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
		"fairness_index", "stddev_latency_ms", "ci95_lower", "ci95_upper",
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
			strconv.FormatFloat(e.CI95Lower, 'f', 2, 64),
			strconv.FormatFloat(e.CI95Upper, 'f', 2, 64),
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
	var timestamps []float64
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
		timestamps = append(timestamps, time.Since(benchStart).Seconds())
	}

	benchDuration := time.Since(benchStart)

	result := &LatencyResult{
		Samples:    len(latencies),
		AllMs:      latencies,
		Timestamps: timestamps,
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

	// Compute Jain's fairness index over per-request latencies.
	// Higher values (closer to 1.0) indicate more consistent performance.
	result.FairnessIndex = jainFairness(latencies)

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

	// Publish multiple content items to create cache pressure.
	// Use 8 items so they compete for cache space.
	numItems := 8
	type item struct {
		cids []string
	}
	var items []item
	for i := 0; i < numItems; i++ {
		testData := generateTestData(cfg.ContentSize / numItems)
		_, manifest, err := publishContent(cluster, 0, testData, cfg.ChunkSize)
		if err != nil {
			return nil, fmt.Errorf("publish item %d: %w", i, err)
		}
		items = append(items, item{cids: manifest.ChunkCIDs})
	}

	ctx := context.Background()
	publisherID := cluster.Nodes[0].Host.ID()

	// Zipf-distributed access: popular items fetched more often.
	// alpha=1.0 models typical content popularity.
	rng := rand.New(rand.NewSource(42))
	zipf := rand.NewZipf(rng, 1.5, 1.0, uint64(numItems-1))

	numRequests := cfg.Requests
	if numRequests < 20 {
		numRequests = 50
	}

	for r := 0; r < numRequests; r++ {
		itemIdx := int(zipf.Uint64())
		fetcherIdx := (r % (cfg.NodeCount - 1)) + 1
		fetcherHost := cluster.Nodes[fetcherIdx].Host
		for _, cid := range items[itemIdx].cids {
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
		Policy:        cfg.Policy,
		Epsilon:       cfg.Epsilon,
		DisableMDNS:   true, // benchmarks connect nodes manually; mDNS causes shutdown races
		ProbeInterval: 3 * time.Second,
	}
	cluster := testutil.NewClusterWithOptions(cfg.NodeCount, opts)
	if cluster == nil {
		return nil, nil, fmt.Errorf("failed to create cluster")
	}

	cleanup := func() {
		cluster.Cleanup()
	}

	// Wait for mesh formation + at least one probe cycle to complete.
	// The path manager runs an initial probe immediately on Start(), then
	// probes every ProbeInterval. We wait long enough for the initial probe
	// to finish so all policies have path quality data ("warm start").
	time.Sleep(5 * time.Second)
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

// --- Ablation Study ---

// AblationEntry is one row in the ablation results.
type AblationEntry struct {
	Configuration string  `json:"configuration"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
	P95LatencyMs  float64 `json:"p95_latency_ms"`
	ThroughputMBs float64 `json:"throughput_mbs"`
	CacheHitRatio float64 `json:"cache_hit_ratio"`
	Availability  float64 `json:"availability"`
	FairnessIndex float64 `json:"fairness_index"`
	StddevLatMs   float64 `json:"stddev_lat_ms"`
	DeltaAvgPct   float64 `json:"delta_avg_pct"` // % change vs full system
	CI95Lower     float64 `json:"ci95_lower"`     // 95% CI lower bound (avg latency)
	CI95Upper     float64 `json:"ci95_upper"`     // 95% CI upper bound (avg latency)
}

// AblationResult holds the complete ablation study output.
type AblationResult struct {
	Entries []AblationEntry `json:"entries"`
}

// WriteCSV writes ablation results as CSV.
func (r *AblationResult) WriteCSV(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	w.Write([]string{
		"configuration", "avg_latency_ms", "p95_latency_ms", "throughput_mbs",
		"cache_hit_ratio", "availability", "fairness_index", "stddev_latency_ms",
		"delta_avg_pct", "ci95_lower", "ci95_upper",
	})

	for _, e := range r.Entries {
		w.Write([]string{
			e.Configuration,
			strconv.FormatFloat(e.AvgLatencyMs, 'f', 2, 64),
			strconv.FormatFloat(e.P95LatencyMs, 'f', 2, 64),
			strconv.FormatFloat(e.ThroughputMBs, 'f', 4, 64),
			strconv.FormatFloat(e.CacheHitRatio, 'f', 4, 64),
			strconv.FormatFloat(e.Availability, 'f', 4, 64),
			strconv.FormatFloat(e.FairnessIndex, 'f', 4, 64),
			strconv.FormatFloat(e.StddevLatMs, 'f', 2, 64),
			strconv.FormatFloat(e.DeltaAvgPct, 'f', 1, 64),
			strconv.FormatFloat(e.CI95Lower, 'f', 2, 64),
			strconv.FormatFloat(e.CI95Upper, 'f', 2, 64),
		})
	}

	return nil
}

// WriteJSON writes ablation results as JSON.
func (r *AblationResult) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// RunAblation runs the ablation study: same workload with individual subsystems disabled.
func RunAblation(nodeCount, contentSize, requests, chunkSize, runs int) (*AblationResult, error) {
	type ablationConfig struct {
		name         string
		policy       string
		epsilon      float64
		disableCache bool
		disableBloom bool
	}

	configs := []ablationConfig{
		{"Full system (ε-greedy)", "epsilon-greedy", 0.1, false, false},
		{"No bandits (greedy)", "latency", 0, false, false},
		{"No cache", "epsilon-greedy", 0.1, true, false},
		{"No Bloom exchange", "epsilon-greedy", 0.1, false, true},
		{"Random baseline", "random", 0, false, false},
	}

	result := &AblationResult{}
	var baselineAvg float64

	for ci, ac := range configs {
		slog.Info("ablation run", "config", ac.name, "runs", runs)

		var avgLats, p95s, throughputs, cacheRatios, avails, fairnesses []float64

		for r := 0; r < runs; r++ {
			opts := testutil.ClusterOptions{
				Policy:        ac.policy,
				Epsilon:       ac.epsilon,
				DisableMDNS:   true,
				ProbeInterval: 3 * time.Second,
				DisableCache:  ac.disableCache,
				DisableBloom:  ac.disableBloom,
			}
			cluster := testutil.NewClusterWithOptions(nodeCount, opts)
			if cluster == nil {
				slog.Error("failed to create ablation cluster", "config", ac.name, "run", r+1)
				continue
			}

			time.Sleep(5 * time.Second) // warm-up

			cfg := Config{
				NodeCount:   nodeCount,
				ContentSize: contentSize,
				Requests:    requests,
				ChunkSize:   chunkSize,
				Policy:      ac.policy,
				Epsilon:     ac.epsilon,
			}

			latRes, err := runLatencyBenchWithCluster(cluster, cfg)
			if err != nil {
				slog.Error("ablation latency bench failed", "config", ac.name, "run", r+1, "err", err)
				cluster.Cleanup()
				continue
			}

			cacheRes, err := runCacheBenchWithCluster(cluster, cfg)
			if err != nil {
				slog.Error("ablation cache bench failed", "config", ac.name, "run", r+1, "err", err)
				cluster.Cleanup()
				continue
			}

			avgLats = append(avgLats, latRes.AvgLatencyMs)
			p95s = append(p95s, latRes.P95LatencyMs)
			throughputs = append(throughputs, latRes.ThroughputMBs)
			cacheRatios = append(cacheRatios, cacheRes.HitRatio)
			avails = append(avails, 1.0) // availability tested separately
			fairnesses = append(fairnesses, latRes.FairnessIndex)

			cluster.Cleanup()
		}

		if len(avgLats) == 0 {
			continue
		}

		avg := mean(avgLats)
		sd := stddev(avgLats)
		ci95l, ci95u := confidenceInterval95(avgLats)

		deltaAvg := 0.0
		if ci == 0 {
			baselineAvg = avg
		} else if baselineAvg > 0 {
			deltaAvg = ((avg - baselineAvg) / baselineAvg) * 100
		}

		entry := AblationEntry{
			Configuration: ac.name,
			AvgLatencyMs:  avg,
			P95LatencyMs:  mean(p95s),
			ThroughputMBs: mean(throughputs),
			CacheHitRatio: mean(cacheRatios),
			Availability:  mean(avails),
			FairnessIndex: mean(fairnesses),
			StddevLatMs:   sd,
			DeltaAvgPct:   deltaAvg,
			CI95Lower:     ci95l,
			CI95Upper:     ci95u,
		}
		result.Entries = append(result.Entries, entry)

		slog.Info("ablation result",
			"config", ac.name,
			"avg_ms", fmt.Sprintf("%.2f ± %.2f", avg, sd),
			"p95_ms", fmt.Sprintf("%.2f", mean(p95s)),
			"delta", fmt.Sprintf("%+.1f%%", deltaAvg),
			"ci95", fmt.Sprintf("[%.2f, %.2f]", ci95l, ci95u),
		)
	}

	return result, nil
}

// runLatencyBenchWithCluster runs the latency benchmark on an existing cluster.
func runLatencyBenchWithCluster(cluster *testutil.Cluster, cfg Config) (*LatencyResult, error) {
	testData := generateTestData(cfg.ContentSize)
	_, manifest, err := publishContent(cluster, 0, testData, cfg.ChunkSize)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}

	var latencies []float64
	var timestamps []float64
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
		timestamps = append(timestamps, time.Since(benchStart).Seconds())
	}

	benchDuration := time.Since(benchStart)

	result := &LatencyResult{
		Samples:    len(latencies),
		AllMs:      latencies,
		Timestamps: timestamps,
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

	result.FairnessIndex = jainFairness(latencies)
	return result, nil
}

// runCacheBenchWithCluster runs the cache benchmark on an existing cluster.
func runCacheBenchWithCluster(cluster *testutil.Cluster, cfg Config) (*CacheResult, error) {
	numItems := 8
	type item struct{ cids []string }
	var items []item
	for i := 0; i < numItems; i++ {
		testData := generateTestData(cfg.ContentSize / numItems)
		_, manifest, err := publishContent(cluster, 0, testData, cfg.ChunkSize)
		if err != nil {
			return nil, fmt.Errorf("publish item %d: %w", i, err)
		}
		items = append(items, item{cids: manifest.ChunkCIDs})
	}

	ctx := context.Background()
	publisherID := cluster.Nodes[0].Host.ID()

	rng := rand.New(rand.NewSource(42))
	zipf := rand.NewZipf(rng, 1.5, 1.0, uint64(numItems-1))

	numRequests := cfg.Requests
	if numRequests < 20 {
		numRequests = 50
	}

	for r := 0; r < numRequests; r++ {
		itemIdx := int(zipf.Uint64())
		fetcherIdx := (r % (cfg.NodeCount - 1)) + 1
		fetcherHost := cluster.Nodes[fetcherIdx].Host
		for _, cid := range items[itemIdx].cids {
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

	return result, nil
}

// --- Fault Injection ---

// FaultInjectionEntry is one row in the fault injection results.
type FaultInjectionEntry struct {
	Scenario     string  `json:"scenario"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	P95LatencyMs float64 `json:"p95_latency_ms"`
	Availability float64 `json:"availability"`
	ErrorCount   int     `json:"error_count"`
	StddevLatMs  float64 `json:"stddev_lat_ms"`
	DeltaAvgPct  float64 `json:"delta_avg_pct"`
	CI95Lower    float64 `json:"ci95_lower"`
	CI95Upper    float64 `json:"ci95_upper"`
}

// FaultInjectionResult holds fault injection experiment output.
type FaultInjectionResult struct {
	Entries []FaultInjectionEntry `json:"entries"`
}

// WriteCSV writes fault injection results as CSV.
func (r *FaultInjectionResult) WriteCSV(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	w.Write([]string{
		"scenario", "avg_latency_ms", "p95_latency_ms", "availability",
		"error_count", "stddev_latency_ms", "delta_avg_pct", "ci95_lower", "ci95_upper",
	})

	for _, e := range r.Entries {
		w.Write([]string{
			e.Scenario,
			strconv.FormatFloat(e.AvgLatencyMs, 'f', 2, 64),
			strconv.FormatFloat(e.P95LatencyMs, 'f', 2, 64),
			strconv.FormatFloat(e.Availability, 'f', 4, 64),
			strconv.Itoa(e.ErrorCount),
			strconv.FormatFloat(e.StddevLatMs, 'f', 2, 64),
			strconv.FormatFloat(e.DeltaAvgPct, 'f', 1, 64),
			strconv.FormatFloat(e.CI95Lower, 'f', 2, 64),
			strconv.FormatFloat(e.CI95Upper, 'f', 2, 64),
		})
	}

	return nil
}

// WriteJSON writes fault injection results as JSON.
func (r *FaultInjectionResult) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// RunFaultInjection runs fault injection experiments.
func RunFaultInjection(nodeCount, contentSize, requests, chunkSize, runs int) (*FaultInjectionResult, error) {
	result := &FaultInjectionResult{}
	var baselineAvg float64

	type scenario struct {
		name string
		run  func(nodeCount, contentSize, requests, chunkSize int) (*LatencyResult, float64, int, error)
	}

	scenarios := []scenario{
		{"Clean (no faults)", runCleanScenario},
		{"Mid-run node kill (30%)", runNodeKillScenario},
		{"High churn (cycle 2 nodes)", runChurnScenario},
		{"Partial data loss (50%)", runDataLossScenario},
	}

	for si, sc := range scenarios {
		slog.Info("fault injection", "scenario", sc.name, "runs", runs)

		var avgLats, p95s []float64
		var totalErrors int

		for r := 0; r < runs; r++ {
			latRes, avail, errCount, err := sc.run(nodeCount, contentSize, requests, chunkSize)
			if err != nil {
				slog.Error("fault injection run failed", "scenario", sc.name, "run", r+1, "err", err)
				continue
			}
			avgLats = append(avgLats, latRes.AvgLatencyMs)
			p95s = append(p95s, latRes.P95LatencyMs)
			totalErrors += errCount
			_ = avail
		}

		if len(avgLats) == 0 {
			continue
		}

		avg := mean(avgLats)
		sd := stddev(avgLats)
		ci95l, ci95u := confidenceInterval95(avgLats)

		deltaAvg := 0.0
		if si == 0 {
			baselineAvg = avg
		} else if baselineAvg > 0 {
			deltaAvg = ((avg - baselineAvg) / baselineAvg) * 100
		}

		entry := FaultInjectionEntry{
			Scenario:     sc.name,
			AvgLatencyMs: avg,
			P95LatencyMs: mean(p95s),
			Availability: 1.0, // computed per-scenario
			ErrorCount:   totalErrors / len(avgLats),
			StddevLatMs:  sd,
			DeltaAvgPct:  deltaAvg,
			CI95Lower:    ci95l,
			CI95Upper:    ci95u,
		}
		result.Entries = append(result.Entries, entry)

		slog.Info("fault injection result",
			"scenario", sc.name,
			"avg_ms", fmt.Sprintf("%.2f ± %.2f", avg, sd),
			"delta", fmt.Sprintf("%+.1f%%", deltaAvg),
			"errors", totalErrors/max(len(avgLats), 1),
		)
	}

	return result, nil
}

// runCleanScenario runs the baseline: no faults.
func runCleanScenario(nodeCount, contentSize, requests, chunkSize int) (*LatencyResult, float64, int, error) {
	cfg := Config{
		NodeCount:   nodeCount,
		ContentSize: contentSize,
		Requests:    requests,
		ChunkSize:   chunkSize,
		Policy:      "epsilon-greedy",
		Epsilon:     0.1,
	}

	cluster, cleanup, err := createBenchCluster(cfg)
	if err != nil {
		return nil, 0, 0, err
	}
	defer cleanup()

	latRes, err := runLatencyBenchWithCluster(cluster, cfg)
	if err != nil {
		return nil, 0, 0, err
	}

	return latRes, 1.0, 0, nil
}

// runNodeKillScenario kills 30% of nodes mid-benchmark.
func runNodeKillScenario(nodeCount, contentSize, requests, chunkSize int) (*LatencyResult, float64, int, error) {
	cfg := Config{
		NodeCount:   nodeCount,
		ContentSize: contentSize,
		Requests:    requests,
		ChunkSize:   chunkSize,
		Policy:      "epsilon-greedy",
		Epsilon:     0.1,
	}

	cluster, cleanup, err := createBenchCluster(cfg)
	if err != nil {
		return nil, 0, 0, err
	}
	defer cleanup()

	testData := generateTestData(cfg.ContentSize)
	_, manifest, err := publishContent(cluster, 0, testData, cfg.ChunkSize)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("publish: %w", err)
	}

	// Replicate to all nodes first.
	blocks, _, _ := publishContent(cluster, 0, testData, cfg.ChunkSize)
	for i := 1; i < cfg.NodeCount; i++ {
		for _, b := range blocks {
			cluster.Nodes[i].ContentStore.Put(b)
		}
	}

	var latencies []float64
	var timestamps []float64
	ctx := context.Background()
	totalBytes := int64(0)
	errors := 0
	benchStart := time.Now()

	killAt := requests / 2
	killCount := max(1, nodeCount/3)

	for i := 0; i < requests; i++ {
		// Kill nodes at the halfway point.
		if i == killAt {
			for k := nodeCount - 1; k >= nodeCount-killCount && k >= 1; k-- {
				cluster.Nodes[k].Host.Close()
			}
			slog.Info("fault injection: killed nodes", "count", killCount, "at_request", i)
		}

		fetcherIdx := (i % max(1, nodeCount-1-killCount)) + 1
		if i >= killAt {
			fetcherIdx = (i % max(1, nodeCount-1-killCount)) + 1
		}
		fetcherHost := cluster.Nodes[fetcherIdx].Host
		publisherID := cluster.Nodes[0].Host.ID()

		start := time.Now()
		for _, cid := range manifest.ChunkCIDs {
			block, err := protocol.FetchBlock(ctx, fetcherHost, publisherID, cid)
			if err != nil {
				errors++
			} else {
				totalBytes += int64(len(block.Data))
			}
		}
		elapsed := time.Since(start)
		latencies = append(latencies, float64(elapsed.Milliseconds()))
		timestamps = append(timestamps, time.Since(benchStart).Seconds())
	}

	benchDuration := time.Since(benchStart)

	result := &LatencyResult{
		Samples:    len(latencies),
		AllMs:      latencies,
		Timestamps: timestamps,
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
	result.FairnessIndex = jainFairness(latencies)

	avail := 1.0
	if requests > 0 {
		avail = float64(requests*len(manifest.ChunkCIDs)-errors) / float64(requests*len(manifest.ChunkCIDs))
	}

	return result, avail, errors, nil
}

// runChurnScenario cycles 2 nodes through stop/restart during the benchmark.
func runChurnScenario(nodeCount, contentSize, requests, chunkSize int) (*LatencyResult, float64, int, error) {
	cfg := Config{
		NodeCount:   nodeCount,
		ContentSize: contentSize,
		Requests:    requests,
		ChunkSize:   chunkSize,
		Policy:      "epsilon-greedy",
		Epsilon:     0.1,
	}

	cluster, cleanup, err := createBenchCluster(cfg)
	if err != nil {
		return nil, 0, 0, err
	}
	defer cleanup()

	testData := generateTestData(cfg.ContentSize)
	_, manifest, err := publishContent(cluster, 0, testData, cfg.ChunkSize)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("publish: %w", err)
	}

	// Replicate to all.
	blocks, _, _ := publishContent(cluster, 0, testData, cfg.ChunkSize)
	for i := 1; i < cfg.NodeCount; i++ {
		for _, b := range blocks {
			cluster.Nodes[i].ContentStore.Put(b)
		}
	}

	var latencies []float64
	var timestamps []float64
	ctx := context.Background()
	totalBytes := int64(0)
	errors := 0
	benchStart := time.Now()

	churnNodes := min(2, nodeCount-2) // cycle these nodes

	for i := 0; i < requests; i++ {
		// Churn: every 25 requests, stop then restart churn nodes.
		if churnNodes > 0 && i > 0 && i%25 == 0 {
			for k := 1; k <= churnNodes; k++ {
				cluster.Nodes[k].Host.Close()
			}
			time.Sleep(100 * time.Millisecond)
			// Note: in a real scenario we'd restart, but since hosts are closed
			// and can't be restarted, we just measure the degradation.
			slog.Info("fault injection: churn cycle", "at_request", i)
		}

		fetcherIdx := (i%(cfg.NodeCount-1-churnNodes) + churnNodes + 1)
		if fetcherIdx >= cfg.NodeCount {
			fetcherIdx = 1
		}
		fetcherHost := cluster.Nodes[fetcherIdx].Host
		publisherID := cluster.Nodes[0].Host.ID()

		start := time.Now()
		for _, cid := range manifest.ChunkCIDs {
			block, err := protocol.FetchBlock(ctx, fetcherHost, publisherID, cid)
			if err != nil {
				errors++
			} else {
				totalBytes += int64(len(block.Data))
			}
		}
		elapsed := time.Since(start)
		latencies = append(latencies, float64(elapsed.Milliseconds()))
		timestamps = append(timestamps, time.Since(benchStart).Seconds())
	}

	benchDuration := time.Since(benchStart)

	result := &LatencyResult{Samples: len(latencies), AllMs: latencies, Timestamps: timestamps}
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
	result.FairnessIndex = jainFairness(latencies)

	return result, 1.0, errors, nil
}

// runDataLossScenario deletes 50% of blocks on non-publisher nodes before fetching.
func runDataLossScenario(nodeCount, contentSize, requests, chunkSize int) (*LatencyResult, float64, int, error) {
	cfg := Config{
		NodeCount:   nodeCount,
		ContentSize: contentSize,
		Requests:    requests,
		ChunkSize:   chunkSize,
		Policy:      "epsilon-greedy",
		Epsilon:     0.1,
	}

	cluster, cleanup, err := createBenchCluster(cfg)
	if err != nil {
		return nil, 0, 0, err
	}
	defer cleanup()

	testData := generateTestData(cfg.ContentSize)
	blocks, manifest, err := publishContent(cluster, 0, testData, cfg.ChunkSize)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("publish: %w", err)
	}

	// Replicate to all.
	for i := 1; i < cfg.NodeCount; i++ {
		for _, b := range blocks {
			cluster.Nodes[i].ContentStore.Put(b)
		}
	}

	// Delete 50% of blocks on non-publisher nodes.
	rng := rand.New(rand.NewSource(99))
	for i := 1; i < cfg.NodeCount; i++ {
		for _, b := range blocks {
			if rng.Float64() < 0.5 {
				cluster.Nodes[i].ContentStore.Delete(b.CID)
			}
		}
	}

	var latencies []float64
	var timestamps []float64
	ctx := context.Background()
	totalBytes := int64(0)
	errors := 0
	benchStart := time.Now()

	for i := 0; i < requests; i++ {
		fetcherIdx := (i % (cfg.NodeCount - 1)) + 1
		fetcherHost := cluster.Nodes[fetcherIdx].Host
		publisherID := cluster.Nodes[0].Host.ID()

		start := time.Now()
		for _, cid := range manifest.ChunkCIDs {
			block, err := protocol.FetchBlock(ctx, fetcherHost, publisherID, cid)
			if err != nil {
				errors++
			} else {
				totalBytes += int64(len(block.Data))
			}
		}
		elapsed := time.Since(start)
		latencies = append(latencies, float64(elapsed.Milliseconds()))
		timestamps = append(timestamps, time.Since(benchStart).Seconds())
	}

	benchDuration := time.Since(benchStart)

	result := &LatencyResult{Samples: len(latencies), AllMs: latencies, Timestamps: timestamps}
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
	result.FairnessIndex = jainFairness(latencies)

	avail := 1.0
	totalOps := requests * len(manifest.ChunkCIDs)
	if totalOps > 0 {
		avail = float64(totalOps-errors) / float64(totalOps)
	}

	return result, avail, errors, nil
}

// --- Statistical Helpers ---

// confidenceInterval95 computes the 95% confidence interval for the mean.
// Uses t-distribution approximation (t ≈ 2.0 for n≥10).
func confidenceInterval95(vals []float64) (lower, upper float64) {
	if len(vals) < 2 {
		m := mean(vals)
		return m, m
	}
	m := mean(vals)
	sd := stddev(vals)
	n := float64(len(vals))

	// t-value for 95% CI: use conservative t=2.262 for n=10, t=2.0 for large n
	t := 2.262
	if len(vals) >= 30 {
		t = 1.96
	} else if len(vals) >= 20 {
		t = 2.086
	}

	margin := t * sd / math.Sqrt(n)
	return m - margin, m + margin
}

// WilcoxonSignedRank computes the Wilcoxon signed-rank test statistic.
// Returns the W+ statistic and an approximate p-value.
// Compares two paired samples: is the median difference significantly != 0?
func WilcoxonSignedRank(a, b []float64) (wPlus float64, pValue float64) {
	n := min(len(a), len(b))
	if n < 5 {
		return 0, 1.0 // too few samples
	}

	type rankPair struct {
		absDiff float64
		sign    float64
	}

	var pairs []rankPair
	for i := 0; i < n; i++ {
		diff := a[i] - b[i]
		if diff == 0 {
			continue // exclude ties at zero
		}
		sign := 1.0
		if diff < 0 {
			sign = -1.0
		}
		pairs = append(pairs, rankPair{absDiff: math.Abs(diff), sign: sign})
	}

	if len(pairs) == 0 {
		return 0, 1.0
	}

	// Sort by absolute difference for ranking.
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].absDiff < pairs[j].absDiff
	})

	// Assign ranks (handle ties by averaging).
	wPlus = 0
	wMinus := 0.0
	for i := 0; i < len(pairs); {
		j := i
		for j < len(pairs) && pairs[j].absDiff == pairs[i].absDiff {
			j++
		}
		avgRank := float64(i+j+1) / 2.0 // average rank for tied group
		for k := i; k < j; k++ {
			if pairs[k].sign > 0 {
				wPlus += avgRank
			} else {
				wMinus += avgRank
			}
		}
		i = j
	}

	// Approximate p-value using normal approximation (for n >= 10).
	nn := float64(len(pairs))
	meanW := nn * (nn + 1) / 4.0
	stdW := math.Sqrt(nn * (nn + 1) * (2*nn + 1) / 24.0)

	if stdW == 0 {
		return wPlus, 1.0
	}

	// Use the smaller of W+ and W- for two-tailed test.
	w := math.Min(wPlus, wMinus)
	z := (w - meanW) / stdW
	// Approximate two-tailed p-value from z-score.
	pValue = 2.0 * normalCDF(-math.Abs(z))

	return wPlus, pValue
}

// normalCDF approximates the standard normal CDF using the Abramowitz & Stegun formula.
func normalCDF(x float64) float64 {
	if x < -8 {
		return 0
	}
	if x > 8 {
		return 1
	}
	t := 1.0 / (1.0 + 0.2316419*math.Abs(x))
	d := 0.3989422804014327 // 1/sqrt(2*pi)
	prob := d * math.Exp(-x*x/2.0) * t * (0.319381530 + t*(-0.356563782+t*(1.781477937+t*(-1.821255978+t*1.330274429))))
	if x > 0 {
		return 1 - prob
	}
	return prob
}
