package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/erena/scion-libp2p/internal/bench"
	"github.com/spf13/cobra"
)

var benchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Run evaluation benchmarks",
	Long: `Run benchmark experiments for evaluation.

Experiments:
  single      Run a single benchmark with the specified policy
  compare     Three-way comparison (epsilon-greedy vs latency vs random)
  scalability Run comparison at multiple node counts (5,10,25)
`,
	RunE: runBench,
}

var (
	benchNodes          int
	benchContentSize    int
	benchRequests       int
	benchChunkSize      int
	benchPolicy         string
	benchEpsilon        float64
	benchExperiment     string
	benchOutputJSON     string
	benchOutputCSV      string
	benchRuns           int
	benchTimeSeriesDir  string
)

func init() {
	benchCmd.Flags().IntVar(&benchNodes, "nodes", 5, "number of nodes")
	benchCmd.Flags().IntVar(&benchContentSize, "size", 1024*1024, "content size in bytes")
	benchCmd.Flags().IntVar(&benchRequests, "requests", 10, "number of fetch requests")
	benchCmd.Flags().IntVar(&benchChunkSize, "chunk-size", 256*1024, "chunk size in bytes")
	benchCmd.Flags().StringVar(&benchPolicy, "policy", "epsilon-greedy", "path policy for single run")
	benchCmd.Flags().Float64Var(&benchEpsilon, "epsilon", 0.1, "epsilon for epsilon-greedy")
	benchCmd.Flags().StringVar(&benchExperiment, "experiment", "compare", "experiment type: single, compare, scalability")
	benchCmd.Flags().StringVar(&benchOutputJSON, "output-json", "", "output JSON results to file")
	benchCmd.Flags().StringVar(&benchOutputCSV, "output-csv", "", "output CSV results to file")
	benchCmd.Flags().IntVar(&benchRuns, "runs", 1, "number of runs per configuration (results averaged)")
	benchCmd.Flags().StringVar(&benchTimeSeriesDir, "output-timeseries", "", "directory for per-request convergence time series CSVs")

	rootCmd.AddCommand(benchCmd)
}

func runBench(cmd *cobra.Command, args []string) error {
	switch strings.ToLower(benchExperiment) {
	case "single":
		return runSingleBench()
	case "compare":
		return runCompareBench()
	case "scalability":
		return runScalabilityBench()
	default:
		return fmt.Errorf("unknown experiment: %s (valid: single, compare, scalability)", benchExperiment)
	}
}

func runSingleBench() error {
	if benchTimeSeriesDir != "" {
		os.MkdirAll(benchTimeSeriesDir, 0o755)
	}

	cfg := bench.Config{
		NodeCount:     benchNodes,
		ContentSize:   benchContentSize,
		Requests:      benchRequests,
		ChunkSize:     benchChunkSize,
		Policy:        benchPolicy,
		Epsilon:       benchEpsilon,
		TimeSeriesDir: benchTimeSeriesDir,
	}

	results, err := bench.Run(cfg)
	if err != nil {
		return fmt.Errorf("benchmark failed: %w", err)
	}

	fmt.Println("\n=== Benchmark Results ===")
	fmt.Printf("Policy:     %s\n", cfg.Policy)
	fmt.Printf("Nodes:      %d\n", cfg.NodeCount)
	fmt.Printf("Duration:   %s\n", results.TotalDuration)
	fmt.Printf("\nLatency:\n")
	fmt.Printf("  Avg:  %.1f ms\n", results.Latency.AvgLatencyMs)
	fmt.Printf("  P50:  %.1f ms\n", results.Latency.P50LatencyMs)
	fmt.Printf("  P95:  %.1f ms\n", results.Latency.P95LatencyMs)
	fmt.Printf("  P99:  %.1f ms\n", results.Latency.P99LatencyMs)
	fmt.Printf("  Throughput: %.2f MB/s\n", results.Latency.ThroughputMBs)
	fmt.Printf("\nCache:\n")
	fmt.Printf("  Hit Ratio: %.1f%%\n", results.CacheComparison.HitRatio*100)
	fmt.Printf("\nResilience:\n")
	fmt.Printf("  Availability: %.1f%% (%d/%d blocks, %d nodes killed)\n",
		results.Resilience.Availability*100,
		results.Resilience.AvailableBlocks, results.Resilience.TotalBlocks,
		results.Resilience.NodesKilled)

	if benchOutputJSON != "" {
		if err := results.WriteJSON(benchOutputJSON); err != nil {
			return fmt.Errorf("write JSON: %w", err)
		}
		fmt.Printf("\nResults written to %s\n", benchOutputJSON)
	}

	return nil
}

func runCompareBench() error {
	slog.Info("running policy comparison", "nodes", benchNodes, "runs", benchRuns)

	if benchTimeSeriesDir != "" {
		os.MkdirAll(benchTimeSeriesDir, 0o755)
	}

	results, err := bench.RunComparisonWithRuns(benchNodes, benchContentSize, benchRequests, benchChunkSize, benchRuns, benchTimeSeriesDir)
	if err != nil {
		return fmt.Errorf("comparison failed: %w", err)
	}

	printComparisonTable(results)

	if benchOutputCSV != "" {
		if err := results.WriteCSV(benchOutputCSV); err != nil {
			return fmt.Errorf("write CSV: %w", err)
		}
		fmt.Printf("\nCSV results written to %s\n", benchOutputCSV)
	}
	if benchOutputJSON != "" {
		if err := results.WriteJSON(benchOutputJSON); err != nil {
			return fmt.Errorf("write JSON: %w", err)
		}
		fmt.Printf("JSON results written to %s\n", benchOutputJSON)
	}

	return nil
}

func runScalabilityBench() error {
	nodeCounts := []int{5, 10, 25}
	slog.Info("running scalability experiment", "node_counts", nodeCounts)

	if benchTimeSeriesDir != "" {
		os.MkdirAll(benchTimeSeriesDir, 0o755)
	}

	results, err := bench.RunScalability(nodeCounts, benchContentSize, benchRequests, benchChunkSize, benchRuns, benchTimeSeriesDir)
	if err != nil {
		return fmt.Errorf("scalability experiment failed: %w", err)
	}

	printComparisonTable(results)

	if benchOutputCSV != "" {
		if err := results.WriteCSV(benchOutputCSV); err != nil {
			return fmt.Errorf("write CSV: %w", err)
		}
		fmt.Printf("\nCSV results written to %s\n", benchOutputCSV)
	}
	if benchOutputJSON != "" {
		if err := results.WriteJSON(benchOutputJSON); err != nil {
			return fmt.Errorf("write JSON: %w", err)
		}
		fmt.Printf("JSON results written to %s\n", benchOutputJSON)
	}

	return nil
}

func printComparisonTable(results *bench.ComparisonResult) {
	fmt.Println("\n=== Comparison Results ===")
	fmt.Printf("%-16s %5s %8s %8s %8s %8s %10s %8s %8s\n",
		"Policy", "Nodes", "Avg(ms)", "P50(ms)", "P95(ms)", "P99(ms)", "MB/s", "Cache%", "Avail%")
	fmt.Println(strings.Repeat("-", 90))

	for _, e := range results.Configs {
		fmt.Printf("%-16s %5d %8.1f %8.1f %8.1f %8.1f %10.2f %7.1f%% %7.1f%%\n",
			e.Policy, e.NodeCount,
			e.AvgLatencyMs, e.P50LatencyMs, e.P95LatencyMs, e.P99LatencyMs,
			e.ThroughputMBs,
			e.CacheHitRatio*100, e.Availability*100)
	}
}
