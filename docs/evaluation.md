# Evaluation Guide

This document describes how to run the evaluation experiments, interpret results, and reproduce the findings for the paper.

## Experiments Overview

The evaluation framework supports three experiment types:

```
  Experiment        What it tests                    Key finding
  ----------------------------------------------------------------
  compare           3 policies at fixed node count   epsilon-greedy
                                                     has better tail
                                                     latency than
                                                     greedy min-RTT

  scalability       3 policies at 5/10/25 nodes      greedy degrades
                                                     faster at scale

  single            1 policy, detailed metrics        deep-dive into
                                                     one configuration
```

## Running Experiments

### Prerequisites

```bash
go build -o scion-libp2p .
```

### Three-Way Comparison

This is the core experiment. It compares three path selection strategies:

```
  Config A:  epsilon-greedy (epsilon=0.1)
             Path-aware with exploration. Expected to have
             slightly higher avg latency but much better
             tail latency (p95, p99).

  Config B:  latency (greedy min-RTT)
             Always picks lowest-latency path. Expected to
             cause congestion under load (herd effect).

  Config C:  random
             Path-oblivious baseline. Expected to have
             highest average latency but demonstrate the
             value of path awareness.
```

```bash
# Run with 5 nodes, 1 MB content, 20 fetch requests
./scion-libp2p bench \
  --experiment compare \
  --nodes 5 \
  --size 1048576 \
  --requests 20 \
  --output-csv comparison.csv \
  --output-json comparison.json
```

Output format:

```
=== Comparison Results ===
Policy           Nodes  Avg(ms)  P50(ms)  P95(ms)  P99(ms)      MB/s  Cache%  Avail%
------------------------------------------------------------------------------------------
epsilon-greedy       5     12.3     11.0     18.5     22.1      8.21   50.0%  100.0%
latency              5     11.8     10.5     24.2     31.5      7.95   50.0%  100.0%
random               5     18.7     17.2     28.9     35.3      5.43   50.0%  100.0%
```

### Scalability Experiment

Runs the three-way comparison at multiple node counts to show how each policy scales:

```bash
./scion-libp2p bench \
  --experiment scalability \
  --size 1048576 \
  --requests 10 \
  --output-csv scalability.csv
```

This runs the comparison at 5, 10, and 25 nodes. The CSV output contains rows for each (policy, node_count) pair, suitable for plotting latency-vs-nodes graphs.

Expected pattern:

```
  Latency
    ^
    |         random
    |        /
    |       / latency (greedy)
    |      / /
    |     / /  epsilon-greedy
    |    //  /
    |   //  /
    |  // /
    | ///
    +---+---+---+----> Nodes
       5  10  25  50
```

The greedy min-RTT policy degrades faster at scale because all nodes converge on the same path (herd effect), while epsilon-greedy distributes load via exploration.

### Single Policy Benchmark

For detailed analysis of a specific configuration:

```bash
./scion-libp2p bench \
  --experiment single \
  --policy epsilon-greedy \
  --epsilon 0.15 \
  --nodes 10 \
  --size 2097152 \
  --requests 30 \
  --output-json detailed.json
```

Output includes per-request latency samples for plotting distributions.

## Metrics Collected

Each experiment run collects:

```
  Metric                  Description
  ----------------------------------------------------------
  avg_latency_ms          Mean retrieval latency across all requests
  p50_latency_ms          Median latency
  p95_latency_ms          95th percentile latency
  p99_latency_ms          99th percentile latency
  throughput_mbs           Aggregate throughput (MB/s)
  cache_hit_ratio          Fraction of block lookups served from cache
  availability             Fraction of blocks accessible after killing
                           30% of nodes
```

## Interpreting Results

### Latency

- **p50 (median)**: Typical user experience. All policies should be similar at low load.
- **p95 and p99 (tail)**: Where the herd effect shows up. Greedy min-RTT should have worse tail latency because congestion spikes affect the "best" path that everyone uses simultaneously.
- **Gap between p50 and p99**: Smaller gap = more consistent performance. Epsilon-greedy should have a smaller gap.

### Throughput

Measured as total bytes fetched divided by total time. Higher is better. Parallel fetching (batches of 4) contributes to throughput. Path-oblivious (random) wastes time on bad paths.

### Cache Hit Ratio

After the first round of fetches, subsequent requests should hit the cache. The ratio depends on content size relative to cache size and the number of unique blocks.

### Availability

After killing 30% of nodes, measures how many blocks are still retrievable from surviving nodes. With pre-replication to all nodes, availability should be 100% unless too many nodes are killed.

## Statistical Significance

For publication, run each experiment configuration multiple times:

```bash
for run in 1 2 3 4 5; do
  ./scion-libp2p bench \
    --experiment compare \
    --nodes 10 \
    --requests 50 \
    --output-csv "results_run${run}.csv"
done
```

Then compute mean and standard deviation across runs for each metric. Five runs is the minimum; ten is recommended.

## Experiment Parameters

Recommended parameter ranges for evaluation:

```
  Parameter       Demo    Paper (small)   Paper (large)
  -------------------------------------------------------
  Nodes           3-5     10              25-50
  Content size    256 KB  1 MB            4 MB
  Requests        5       20              50-100
  Chunk size      64 KB   256 KB          256 KB
```

Larger experiments take longer due to cluster setup time. A 25-node comparison with 50 requests takes approximately 5-10 minutes.

## Prometheus Monitoring During Experiments

For real-time observation during experiments, start the monitoring stack:

```bash
# Start Prometheus + Grafana
docker compose up -d

# Run daemon nodes with metrics enabled
./scion-libp2p daemon --metrics-addr :2112 ...
./scion-libp2p daemon --metrics-addr :2113 ...
./scion-libp2p daemon --metrics-addr :2114 ...

# Open Grafana dashboard
# http://localhost:3000 (admin/scion)
```

The Grafana dashboard shows real-time:
- Path RTT distribution (direct vs relay, p50 and p95)
- Block fetch latency by source (local, cache, network)
- Cache hit ratio over time
- Path selection distribution
- Connected peers

Note: The `bench` command creates ephemeral clusters with metrics disabled (to avoid port conflicts). For monitored experiments, use manual daemon processes instead.

## CSV Schema

```
Column              Type     Description
-----------------------------------------------------
policy              string   Policy name
epsilon             float    Epsilon value (0 for non-epsilon policies)
node_count          int      Number of nodes in cluster
avg_latency_ms      float    Mean retrieval latency
p50_latency_ms      float    Median retrieval latency
p95_latency_ms      float    95th percentile latency
p99_latency_ms      float    99th percentile latency
throughput_mbs      float    Aggregate throughput (MB/s)
cache_hit_ratio     float    Cache hit fraction (0.0-1.0)
availability        float    Block availability after failures (0.0-1.0)
```

## Paper Figures

From the CSV output, the following figures can be generated (using Python/matplotlib, R, or gnuplot):

1. **Bar chart**: avg/p50/p95/p99 latency for three policies at N=10
2. **Line plot**: p95 latency vs node count for three policies (scalability)
3. **Bar chart**: throughput comparison for three policies
4. **Stacked bar**: cache hit ratio breakdown (local / cache / network)
5. **Table**: resilience results (availability after X% node failure)
