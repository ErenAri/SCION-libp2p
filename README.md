# scion-libp2p

A path-aware peer-to-peer content overlay built on [libp2p](https://libp2p.io), inspired by [SCION](https://scion-architecture.net)'s end-host path control. Combines path-quality probing, policy-based route selection, and epsilon-greedy exploration with content-addressed storage, NDN-style in-network caching, and Ed25519-signed manifests.

> This is an experimental overlay prototype, not an implementation of the real SCION architecture. It borrows SCION's philosophy of end-host path control and applies it at the libp2p relay layer. There are no AS-level path segments, ISD isolation domains, or cryptographic path validation in the SCION sense.

## Problem

Standard libp2p treats all network paths equally. When multiple relay paths exist between two peers, libp2p picks one arbitrarily. This leads to suboptimal content delivery: high-latency paths get used when low-latency alternatives exist, and under load all peers pile onto the same "best" path (the herd effect), causing congestion collapse.

scion-libp2p adds path awareness to content delivery:

- Continuous probing of direct and relay paths with RTT, jitter, hop count, and success rate tracking
- Six scoring policies including epsilon-greedy selection that avoids herd effects
- Path-aware provider ranking for content fetches
- Disjoint path selection for parallel fetching to avoid shared bottlenecks
- NDN-style relay caching that reduces backbone load
- Popularity-aware cache eviction and proactive block replication

## Architecture

```
+------------------------------------------------------------------+
|                         CLI Layer                                |
|  daemon | peers | ping | paths | publish | fetch | find          |
|  pin | unpin | pins | bench                                      |
+------------------------------------------------------------------+
         |
         v
+------------------------------------------------------------------+
|                      HTTP API Server                             |
|  /api/v1/peers  /ping  /paths  /publish  /fetch  /find           |
|  /api/v1/pin    /pins  /manifest  /status  /health               |
+------------------------------------------------------------------+
         |
         v
+------------------------------------------------------------------+
|                     Node Orchestrator                            |
|  Wires all subsystems, manages lifecycle                         |
+--------+---------+---------+----------+---------+--------+-------+
         |         |         |          |         |        |
         v         v         v          v         v        v
+--------+  +---------+  +---------+  +-------+ +-------+  +---------+
| libp2p |  | Path    |  | Content |  | Block | | DHT   | |Prometheus|
| Host   |  | Manager |  | Store   |  | Cache | |Router | |us        |
| TCP    |  |         |  | On-disk |  | LRU   | |Provide| |Metrics   |
| Relay  |  | Probe   |  | Blocks  |  | Pin-  | |Find   | |14        |
|        |  | Score   |  | Manifst |  | aware | |       | |counters  |
|        |  | Select  |  | Pins    |  |       | |       | |          |
+---+----+  +----+----+  +----+----+  +---+---+ +--+---+  +----------+
    |            |            |          |        |
    +-----+------+------+------+-----+-----+
          |             |            |
          v             v            v
+------------------------------------------------------------------+
|                   Wire Protocols                                 |
|  /scion-libp2p/ping/1.0.0        Echo with nanosecond timestamp  |
|  /scion-libp2p/probe/1.0.0       53B path probe (RTT, hops,      |
|                                   jitter, throughput)            |
|  /scion-libp2p/block/1.0.0       Request/response block fetch    |
|  /scion-libp2p/block-push/1.0.0  Push-based replication          |
+------------------------------------------------------------------+
```

## Content Delivery Flow

```
  Publisher                    Relay Node                   Fetcher
  ---------                   ----------                   -------
      |                           |                           |
  1.  |-- chunk file ------+      |                           |
      |-- compute CIDs     |      |                           |
      |-- sign manifest    |      |                           |
      |-- store blocks --->|      |                           |
      |                    |      |                           |
  2.  |-- DHT.Provide(CIDs) ----> |                           |
      |                    |      |                           |
      |                    |      |    3. DHT.FindProviders() |
      |                    |      |<--------------------------|
      |                    |      |-------- providers ------->|
      |                    |      |                           |
      |                    |      |    4. Path Manager probes |
      |<---- probe (direct) ------|<--- probe (via relay) --- |
      |---- echo (hop=1) -------->|--- echo (hop=2) --------> |
      |                    |      |                           |
      |                    |      |    5. Sort providers by   |
      |                    |      |       path quality score  |
      |                    |      |                           |
      |                    |      |    6. Fetch blocks via    |
      |                    |      |       best-scored path    |
      |<-- FetchBlock(cid) -------|<-- FetchBlock(cid) ------ |
      |--- block data ----------->|--- block data ----------> |
      |                    |      |                           |
      |                    | 7. Cache block (NDN-style)       |
      |                    |      |                           |
      |                    |      |    8. Next fetch of same  |
      |                    |      |       block hits cache    |
      |                    |      |<-- FetchBlock(cid) ------ |
      |                    |      |--- cached block --------> |
```

## Path Selection

### Scoring Policies

```
                          Path Scoring Pipeline

  Probe Results              Policy                    Selection
  +------------+         +-----------+              +-------------+
  | AvgRTT     |-------->| latency   | Score = 1/RTT              |
  | P95RTT     |         +-----------+              |             |
  | Jitter     |-------->| balanced  | Weighted:    | Best path   |
  | HopCount   |         |           | 35% latency  | (or random  |
  | SuccessRate|         |           | 25% reliab.  |  with prob. |
  | Throughput |         |           | 25% hops     |  epsilon)   |
  +------------+         |           | 15% jitter   |             |
                         +-----------+              +-------------+
                         | epsilon-  | With prob 1-e|             |
                         | greedy    | pick best;   |             |
                         |           | with prob e  |             |
                         |           | pick random  |             |
                         +-----------+              +-------------+
```

| Policy | Strategy | Use Case |
|--------|----------|----------|
| `latency` | Lowest EWMA RTT | When latency is the only concern |
| `hop-count` | Fewest hops | Minimize traversal through relays |
| `reliability` | Highest success rate | Unstable networks |
| `balanced` | Weighted: 35% latency, 25% reliability, 25% hops, 15% jitter | General purpose (default) |
| `epsilon-greedy` | Best path 90% of the time, random path 10% | Avoids herd effects under load |
| `random` | Uniform random selection | Evaluation baseline |

The epsilon-greedy policy addresses the herd effect problem described in "An Axiomatic Analysis of Path Selection Strategies for Multipath Transport in Path-Aware Networks" (arXiv 2509.05938, 2025). When all peers greedily select the lowest-latency path, they cause congestion collapse on that path. Epsilon-greedy exploration distributes load across viable paths while still preferring high-quality routes.

### Probe Wire Format

```
Byte offset:   0       8      12  13      17      21              53
              +--------+------+---+-------+-------+---------------+
              |  8B    | 4B   |1B |  4B   |  4B   |     32B       |
              |timestamp|pathID|hop|through-|jitter |    nonce    |
              |  (ns)  |      |cnt| put   |  (us) |   (random)    |
              +--------+------+---+-------+-------+---------------+
                                 ^
                          incremented by each hop
```

### Path Disjointness

When fetching content in parallel, the path manager selects disjoint paths that share no relay peers. This avoids shared bottleneck links, following recommendations from the SCION MPQUIC IETF draft.

```
  Target Peer X
       |
  +----+----+----+
  |    |    |    |
  v    v    v    v
Path1 Path2 Path3 Path4
(direct) (R-A) (R-B) (R-A)

DisjointPaths(X, 3) returns:
  Path1 (direct) -- no relays
  Path2 (via A)  -- relay A
  Path3 (via B)  -- relay B (disjoint from Path2)

Path4 is excluded: shares relay A with Path2.
```

## Caching

### Popularity-Aware LRU Eviction

The block cache uses a modified LRU strategy informed by Kangasharju et al.'s research on adaptive P2P caching. Instead of always evicting the least-recently-used entry, it scans from the LRU end and skips entries with high fetch counts, giving popular blocks a "second chance."

```
Cache (front = most recent, back = LRU):

  [Block-D]  [Block-C]  [Block-B]  [Block-A]
  fetches:2   fetches:1   fetches:8   fetches:1
                                       ^
  Eviction scan starts here ----------+

  Block-A: fetchCount=1 < threshold(3) --> EVICT

  If Block-A had fetchCount=8:
    halve to 4, skip, check Block-B next

  Pinned blocks are never evicted regardless of position.
```

### Proactive Replication

A background goroutine runs every 60 seconds:

1. Queries the replication tracker for popular blocks (fetched 5+ times)
2. Pushes those blocks to connected peers via the block-push protocol
3. Records `scion_libp2p_blocks_replicated_total` metric

This ensures popular content survives publisher disconnection.

## CLI Commands

| Command | Description |
|---------|-------------|
| `scion-libp2p daemon` | Start a node |
| `scion-libp2p peers [-v]` | List connected peers |
| `scion-libp2p ping <peer-id> [-c N]` | Ping a peer, show RTT |
| `scion-libp2p paths [--peer <id>]` | Show paths with quality metrics |
| `scion-libp2p publish <file>` | Chunk, sign, and announce content |
| `scion-libp2p fetch <cid> [-o file]` | Fetch content (parallel batched) |
| `scion-libp2p find <cid>` | Find peers holding a CID via DHT |
| `scion-libp2p pin <cid>` | Pin a CID to prevent eviction |
| `scion-libp2p unpin <cid>` | Remove a pin |
| `scion-libp2p pins` | List all pinned CIDs |
| `scion-libp2p bench` | Run evaluation benchmarks |

### Daemon Flags

```
--listen          Listen multiaddrs (default: /ip4/127.0.0.1/tcp/9000)
--bootstrap       Bootstrap peer multiaddrs
--data-dir        Data directory (default: ~/.scion-libp2p)
--enable-relay    Act as relay server (default: true)
--enable-mdns     Enable mDNS discovery (default: true)
--api-addr        HTTP API address (default: 127.0.0.1:9090)
--metrics-addr    Prometheus metrics address (default: :2112)
--policy          Path policy: latency, hop-count, reliability, balanced,
                  epsilon-greedy, random (default: balanced)
--epsilon         Epsilon-greedy exploration rate 0.0-1.0 (default: 0.1)
--log-level       Log level: debug, info, warn, error (default: info)
```

### Bench Flags

```
--experiment      Experiment type: single, compare, scalability
--nodes           Number of nodes (default: 5)
--size            Content size in bytes (default: 1048576)
--requests        Number of fetch requests (default: 10)
--policy          Policy for single runs (default: epsilon-greedy)
--epsilon         Epsilon for epsilon-greedy (default: 0.1)
--output-json     Write results as JSON to file
--output-csv      Write results as CSV to file
```

## HTTP API

All endpoints served on the daemon's `--api-addr` (default `127.0.0.1:9090`).

| Endpoint | Method | Parameters | Description |
|----------|--------|------------|-------------|
| `/api/v1/peers` | GET | -- | List connected peers |
| `/api/v1/ping` | GET | `peer`, `count` | Ping a peer |
| `/api/v1/paths` | GET | `peer` (optional) | List paths with metrics |
| `/api/v1/publish` | POST | Body: `{file_path, name}` | Publish a file |
| `/api/v1/fetch` | GET | `cid` | Fetch content (binary stream) |
| `/api/v1/manifest` | GET | `cid` | Inspect manifest metadata |
| `/api/v1/find` | GET | `cid` | Find providers via DHT |
| `/api/v1/pin` | POST | Body: `{cid}` | Pin a CID |
| `/api/v1/pin` | DELETE | Body: `{cid}` | Unpin a CID |
| `/api/v1/pins` | GET | -- | List pinned CIDs |
| `/api/v1/status` | GET | -- | Node status |
| `/health` | GET | -- | Liveness check |

## Quick Start

### Two-Node Local Demo

```bash
# Build
go build -o scion-libp2p .

# Terminal 1: Start node A
./scion-libp2p daemon \
  --listen /ip4/127.0.0.1/tcp/9000 \
  --api 127.0.0.1:9090 \
  --metrics-addr :2112 \
  --policy epsilon-greedy

# Terminal 2: Start node B (discovers A via mDNS)
./scion-libp2p daemon \
  --listen /ip4/127.0.0.1/tcp/9001 \
  --api 127.0.0.1:9091 \
  --metrics-addr :2113 \
  --policy epsilon-greedy

# Terminal 3: Operations
./scion-libp2p publish myfile.txt --api 127.0.0.1:9090
# Output: Root CID: abc123...

./scion-libp2p fetch abc123... -o downloaded.txt --api 127.0.0.1:9091
./scion-libp2p paths --api 127.0.0.1:9091
./scion-libp2p pin abc123... --api 127.0.0.1:9090
./scion-libp2p pins --api 127.0.0.1:9090
```

### Three-Node Relay Demo

```bash
# Node A (publisher)
./scion-libp2p daemon --listen /ip4/127.0.0.1/tcp/9000 --api 127.0.0.1:9090

# Node R (relay)
./scion-libp2p daemon --listen /ip4/127.0.0.1/tcp/9001 --api 127.0.0.1:9091

# Node B (fetcher, discovers A directly and via R)
./scion-libp2p daemon --listen /ip4/127.0.0.1/tcp/9002 --api 127.0.0.1:9092

# Publish on A, fetch on B -- paths command shows direct and relay paths
./scion-libp2p publish largefile.bin --api 127.0.0.1:9090
./scion-libp2p fetch <cid> -o output.bin --api 127.0.0.1:9092
./scion-libp2p paths --api 127.0.0.1:9092
```

### Running Benchmarks

```bash
# Three-way policy comparison (epsilon-greedy vs latency vs random)
./scion-libp2p bench --experiment compare --nodes 5 --output-csv results.csv

# Scalability experiment (5, 10, 25 nodes)
./scion-libp2p bench --experiment scalability --output-csv scale.csv

# Single policy benchmark
./scion-libp2p bench --experiment single --policy epsilon-greedy --nodes 10
```

## Monitoring

### Prometheus Metrics

14 application-level metrics plus libp2p built-in transport metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `scion_libp2p_peers_connected` | gauge | Connected peers |
| `scion_libp2p_relay_peers_available` | gauge | Relay-capable peers |
| `scion_libp2p_probe_rtt_seconds` | histogram | Probe RTT by path type |
| `scion_libp2p_probe_failures_total` | counter | Failed probes |
| `scion_libp2p_ping_rtt_seconds` | histogram | Ping RTT by target |
| `scion_libp2p_cache_hits_total` | counter | Cache hits |
| `scion_libp2p_cache_misses_total` | counter | Cache misses |
| `scion_libp2p_cache_bytes` | gauge | Cache size in bytes |
| `scion_libp2p_block_fetch_duration_seconds` | histogram | Fetch time by source |
| `scion_libp2p_blocks_transferred_total` | counter | Blocks by direction |
| `scion_libp2p_content_retrievals_total` | counter | Content retrievals |
| `scion_libp2p_path_selections_total` | counter | Selections by path type |
| `scion_libp2p_stale_paths_pruned_total` | counter | Pruned stale paths |
| `scion_libp2p_blocks_replicated_total` | counter | Replicated blocks |

### Grafana Dashboard

A pre-built Grafana dashboard is included with panels for:

- Connected peers gauge
- Content retrieval rate
- Cache hit ratio
- Path RTT distribution (direct vs relay, p50 and p95)
- Block fetch latency by source (local, cache, network)
- Path selections by type (pie chart)
- Blocks transferred rate
- Probe failures and stale path pruning

```bash
# Start Prometheus + Grafana
docker compose up -d

# Access Grafana at http://localhost:3000 (admin/scion)
# Dashboard auto-provisioned as "SCION-libp2p"
```

## Evaluation Framework

The `bench` command supports three experiment types for systematic evaluation:

### Three-Way Comparison

Compares epsilon-greedy, latency (greedy min-RTT), and random (path-oblivious baseline) policies across these metrics:

```
+------------------+-------+--------+--------+--------+-------+--------+--------+
| Policy           | Nodes | Avg(ms)| P50(ms)| P95(ms)| P99(ms)| MB/s  | Avail% |
+------------------+-------+--------+--------+--------+--------+-------+--------+
| epsilon-greedy   |     5 |   12.3 |   11.0 |   18.5 |   22.1 |  8.21 |  100.0 |
| latency          |     5 |   11.8 |   10.5 |   24.2 |   31.5 |  7.95 |  100.0 |
| random           |     5 |   18.7 |   17.2 |   28.9 |   35.3 |  5.43 |  100.0 |
+------------------+-------+--------+--------+--------+--------+-------+--------+
```

Expected finding: epsilon-greedy has slightly higher average latency than pure latency-greedy but significantly better tail latency (p95, p99) because it avoids congestion on the "best" path.

### Scalability Experiment

Runs the three-way comparison at increasing node counts (5, 10, 25) to measure latency degradation. Expected finding: greedy min-RTT degrades faster at scale because all nodes converge on the same path.

### Resilience Experiment

Publishes content, replicates to all nodes, kills 30% of nodes, then measures block availability. Shows that replication + caching maintains content availability under churn.

### CSV Output

All experiments support `--output-csv` for data analysis:

```
policy,epsilon,node_count,avg_latency_ms,p50_latency_ms,p95_latency_ms,p99_latency_ms,throughput_mbs,cache_hit_ratio,availability
epsilon-greedy,0.10,5,12.30,11.00,18.50,22.10,8.2100,0.5000,1.0000
latency,0.00,5,11.80,10.50,24.20,31.50,7.9500,0.5000,1.0000
random,0.00,5,18.70,17.20,28.90,35.30,5.4300,0.5000,1.0000
```

## Configuration

Configuration is read from (in order of precedence):

1. CLI flags
2. Explicit `--config <path>` JSON file
3. `~/.scion-libp2p/config.json`
4. `./scion-libp2p.json`
5. Built-in defaults

Example `scion-libp2p.json`:

```json
{
  "listen_addrs": ["/ip4/0.0.0.0/tcp/9000"],
  "api_addr": "127.0.0.1:9090",
  "metrics_addr": ":2112",
  "data_dir": "./data",
  "bootstrap_peers": [],
  "enable_relay": true,
  "enable_mdns": true,
  "path_policy": "epsilon-greedy",
  "path_epsilon": 0.1,
  "cache_max_bytes": 134217728,
  "chunk_size_bytes": 262144,
  "log_level": "info"
}
```

## Building and Testing

```bash
# Build
make build
# or: go build -o scion-libp2p .

# Unit tests
make test
# or: go test ./...

# Integration tests (spins up multi-node clusters)
make test-integration
# or: go test -tags=integration -timeout 120s ./...

# Static analysis
make vet
# or: go vet ./...
```

## Project Structure

```
scion-libp2p/
|-- cmd/                          CLI commands (Cobra)
|   |-- root.go                     Root command, config loading, logging
|   |-- daemon.go                   Start a node daemon
|   |-- peers.go                    List connected peers
|   |-- ping.go                     Ping a peer
|   |-- paths.go                    Show paths with metrics
|   |-- publish.go                  Publish a file
|   |-- fetch.go                    Fetch content by CID
|   |-- find.go                     Find providers via DHT
|   |-- pin.go                      Pin/unpin/list pins
|   |-- bench.go                    Run evaluation benchmarks
|
|-- internal/
|   |-- node/                     Node orchestrator
|   |   |-- node.go                 Lifecycle, subsystem wiring, replication loop
|   |   |-- config.go               Configuration struct and defaults
|   |   |-- api.go                  HTTP API (12 endpoints)
|   |
|   |-- content/                  Content management
|   |   |-- chunker.go              File chunking, CID computation, manifests
|   |   |-- store.go                On-disk block storage, pin persistence
|   |   |-- signing.go              Ed25519 manifest signing/verification
|   |   |-- routing.go              DHT content discovery (Provide/Find)
|   |   |-- replication.go          Popularity tracking, replication candidates
|   |
|   |-- pathpolicy/               Path-aware selection
|   |   |-- path.go                 Path model, metrics (RTT, jitter, throughput)
|   |   |-- policy.go               6 scoring policies, epsilon-greedy
|   |   |-- manager.go              Background probing, stale pruning, disjoint paths
|   |
|   |-- protocol/                 Wire protocols
|   |   |-- ids.go                   Protocol ID constants
|   |   |-- ping.go                  Ping echo protocol
|   |   |-- probe.go                 53-byte path probe protocol
|   |   |-- blocktransfer.go         Block fetch + block push protocols
|   |
|   |-- transport/                libp2p networking
|   |   |-- host.go                  Host creation, key management, Prometheus
|   |   |-- discovery.go             Kademlia DHT, mDNS setup
|   |   |-- relay.go                 Circuit relay v2, relay enumeration
|   |
|   |-- cache/                    Block caching
|   |   |-- lru.go                   Popularity-aware LRU with pin support
|   |
|   |-- metrics/                  Observability
|   |   |-- metrics.go               Prometheus registry (14 metrics)
|   |
|   |-- bench/                    Evaluation
|       |-- bench.go                 Latency, cache, resilience, comparison
|
|-- testutil/
|   |-- cluster.go                Multi-node test cluster helper
|
|-- dashboards/
|   |-- scion-libp2p.json         Grafana dashboard (10 panels)
|
|-- monitoring/
|   |-- prometheus.yml            Prometheus scrape config
|   |-- grafana-datasources.yml   Grafana datasource provisioning
|   |-- grafana-dashboards.yml    Grafana dashboard provisioning
|
|-- docker-compose.yml            Prometheus + Grafana stack
|-- main.go                       Entry point
|-- go.mod                        Module definition (Go 1.24)
|-- Makefile                      Build targets
```

## Limitations

This is an experimental prototype:

- **Not real SCION** -- no AS-level path segments, ISD isolation, PCB beaconing, or cryptographic path validation
- **Overlay only** -- runs on standard IP/TCP, cannot bypass IP routing decisions
- **Relay approximation** -- uses libp2p circuit relay v2, not SCION's native forwarding plane
- **No incentive model** -- no tit-for-tat, token economics, or resource accounting
- **Single-process scale** -- tested at 2-25 nodes, not Internet scale
- **TCP only on Windows** -- QUIC disabled due to quic-go v0.49.0 crypto/tls bug
- **In-memory publish** -- files loaded fully into memory for chunking

## Research Context

This project fills a gap in existing work: no prior system combines SCION-style path awareness with libp2p content delivery. Related work includes:

- **SCION** (Perrig et al., 2017) -- path-aware Internet architecture with cryptographic forwarding
- **NDN/ICN** (Jacobson et al., 2009) -- named data networking with in-network caching
- **IPFS** (Benet, 2014) -- content-addressed P2P file system on libp2p
- **SCION MPQUIC** (De Coninck et al., IETF draft) -- multipath QUIC over SCION with disjoint path selection
- **Path Selection Axioms** (arXiv 2509.05938, 2025) -- analysis of herd effects in path-aware networks

The key contribution is demonstrating that epsilon-greedy path selection produces better tail latency than greedy min-RTT under load, while popularity-aware caching outperforms pure LRU for skewed (Zipf) content distributions.

## License

MIT
