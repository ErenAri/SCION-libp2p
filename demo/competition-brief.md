# scion-libp2p -- Competition Brief

## One-Line Summary

Path-aware peer-to-peer content delivery that avoids congestion collapse through epsilon-greedy path selection, NDN-style relay caching, and proactive replication.

## Problem

Standard P2P content delivery is path-oblivious: when multiple network paths exist between peers, the system either picks one arbitrarily or all peers greedily converge on the lowest-latency route. This causes the "herd effect" -- rational individual optimization produces collectively suboptimal outcomes, where the best path becomes congested and performance degrades for everyone.

## Solution

scion-libp2p adds SCION-inspired path awareness to libp2p content delivery:

- Continuous probing of direct and relay paths measuring RTT, jitter, hop count, and success rate
- Epsilon-greedy path selection (90% best path, 10% exploration) that distributes load across viable paths
- NDN-style in-network caching at relay nodes, reducing backbone load
- Popularity-aware cache eviction that retains frequently accessed content
- Proactive replication of popular blocks for fault tolerance

## Key Results

                                Avg Latency    P95 Latency    Throughput
    Epsilon-greedy (ours)       12.3 ms        18.5 ms        8.21 MB/s
    Greedy min-RTT              11.8 ms        24.2 ms        7.95 MB/s
    Random (baseline)           18.7 ms        28.9 ms        5.43 MB/s

- 24% lower tail latency (P95) than greedy selection under contention
- 38% higher cache hit ratio vs pure LRU for Zipf-distributed workloads
- 100% content availability after 30% node failure with replication enabled
- Greedy selection degrades 183% at 25 nodes; epsilon-greedy only 75%

## Technical Innovation

1. First system combining SCION-style path awareness with libp2p content delivery
2. Multi-armed bandit approach (epsilon-greedy) applied to P2P path selection
3. Disjoint path selection for parallel fetching (no shared relay bottlenecks)
4. Popularity-aware eviction adapted from Kangasharju et al. for overlay networks
5. 53-byte probe protocol with hop counting, jitter, and throughput estimation

## Architecture

    +--------------------------------------------------+
    |  CLI: daemon | publish | fetch | bench | pin     |
    +--------------------------------------------------+
    |  HTTP API: 12 endpoints                          |
    +--------------------------------------------------+
    |  Content Layer        |  Path Layer              |
    |  - SHA-256 CIDs       |  - Probe every 10s       |
    |  - Ed25519 signing    |  - 6 scoring policies    |
    |  - DHT routing        |  - Epsilon-greedy        |
    |  - Replication        |  - Disjoint paths        |
    +--------------------------------------------------+
    |  Transport: TCP, Circuit Relay v2, Kademlia DHT  |
    +--------------------------------------------------+

## Technology

- Go 1.24, libp2p v0.40.0, Kademlia DHT
- 4 custom wire protocols (ping, probe, block transfer, block push)
- Prometheus metrics (14 app + 30 libp2p) with Grafana dashboard
- Built-in evaluation framework with CSV/JSON output
- ~3,500 lines of application code + tests

## Impact

The herd effect is a fundamental problem in path-aware networks (arXiv 2509.05938, 2025). As SCION deployment grows and more networks offer path diversity, the tools for exploiting that diversity must handle multi-agent coordination. scion-libp2p demonstrates a practical, deployable solution using well-understood bandit algorithms, without requiring changes to network infrastructure.

## Status

- Fully implemented and tested (M1-M4 complete)
- Open source: https://github.com/ErenAri/SCION-libp2p
- Evaluation framework with reproducible experiments
- Academic paper draft targeting ACM CoNEXT / IEEE INFOCOM workshop

## Team

Eren Ari -- Independent researcher

## Competition Targets

- Red Bull Basement 2026: Technology and innovation category
- SCION Association Academic Program: Path-aware networking research
- University research funding / thesis project

## Demo

2-minute live demo available:

    bash demo/run-demo.sh

Shows three-node cluster, content publishing, path-aware fetching,
resilience under node failure, and policy comparison benchmarks.
With `--with-monitoring` flag, includes real-time Grafana dashboard.
