# Architecture

This document describes the internal architecture of pathaware-libp2p, its subsystems, data flows, and design rationale.

## System Overview

pathaware-libp2p is structured as five layers, each with clear responsibilities:

```
Layer 5:  CLI + HTTP API
          User-facing commands and REST endpoints. Stateless; talks
          to the daemon via HTTP.

Layer 4:  Node Orchestrator
          Wires subsystems together. Manages lifecycle (start/stop),
          background goroutines (probing, replication), and configuration.

Layer 3:  Content Layer
          Chunking, CID computation, manifests, signing, pinning,
          DHT content routing (Provide/FindProviders), replication tracking.

Layer 2:  Path Layer
          Path probing, metrics collection (RTT, jitter, hops, success rate),
          policy-based scoring, epsilon-greedy selection, disjoint path analysis,
          stale path pruning.

Layer 1:  Transport Layer
          libp2p host (TCP + relay), Kademlia DHT, mDNS discovery,
          circuit relay v2 service, key management.
```

### Subsystem Dependency Graph

```
                    +------------------+
                    |   Node (node.go) |
                    +--------+---------+
                             |
          +------+------+----+----+------+------+------+
          |      |      |         |      |      |      |
          v      v      v         v      v      v      v
       Host    DHT   PathMgr  Content  Cache  Router  Metrics
       (L1)   (L1)   (L2)    Store    (L3)   (L3)    (L4)
                               (L3)
                       |         |
                       v         v
                    Policies   Replication
                    (L2)       Tracker (L3)
```

Dependencies flow downward. The Node orchestrator holds references to all subsystems. Subsystems do not reference each other directly except through the Node.

## Transport Layer (Layer 1)

### Host Creation

File: `internal/transport/host.go`

The libp2p host is configured with:

- Ed25519 identity (persisted to `{DataDir}/peer.key`)
- TCP transport (QUIC disabled on Windows)
- Resource manager with auto-scaled limits
- Hole punching support
- Optional relay client
- Optional Prometheus metrics registration

```
  loadOrGenerateKey(dataDir)
          |
          v
  libp2p.New(
    Identity(key),
    ListenAddrStrings(...),
    ResourceManager(rm),
    EnableHolePunching(),
    EnableRelay(),                  // if configured
    PrometheusRegisterer(registry)  // if registry provided
  )
          |
          v
      host.Host
```

### Discovery

File: `internal/transport/discovery.go`

Two discovery mechanisms run in parallel:

```
  +----------+     +--------+
  | Kademlia |     |  mDNS  |
  |   DHT    |     | (LAN)  |
  +----+-----+     +---+----+
       |               |
       v               v
  Bootstrap         HandlePeerFound
  peers              callback
       |               |
       +-------+-------+
               |
               v
         Host.Connect(peerInfo)
```

The DHT uses protocol prefix `/pathaware-libp2p` to isolate from other DHT networks. It runs in `ModeAutoServer` (acts as both client and server).

### Relay

File: `internal/transport/relay.go`

When relay is enabled, the node starts a circuit relay v2 service. Other peers can relay connections through this node. `EnumerateRelayPeers()` finds connected peers that support the relay hop protocol (`/libp2p/circuit/relay/0.2.0/hop`).

## Path Layer (Layer 2)

### Path Model

File: `internal/pathpolicy/path.go`

Each path to a target peer is identified by a string ID (e.g., `direct-abc12345` or `relay-abc12345-via-def67890`) and carries:

```
  Path
  +-- ID           string          unique identifier
  +-- Target       peer.ID         destination peer
  +-- Type         "direct"|"relay"
  +-- RelayChain   []peer.ID       relay hops (empty for direct)
  +-- Metrics
      +-- AvgRTT             EWMA of recent RTT samples
      +-- P95RTT             approximation from sample max
      +-- Jitter             standard deviation of RTT samples
      +-- ThroughputEstimate computed from probe payload / RTT
      +-- HopCount           from probe response
      +-- SuccessRate        EWMA with alpha=0.3
      +-- LastProbed         timestamp
      +-- SampleCount        total probes sent
      +-- rttSamples         ring buffer (20 samples)
```

Metrics are updated via `RecordProbe(rtt, success)`. The EWMA smooths out transient spikes while remaining responsive to sustained changes.

### Probing Loop

File: `internal/pathpolicy/manager.go`

The Manager runs a background goroutine that:

```
  probeLoop()
      |
      +---> probeAll() every ProbeInterval (default 10s)
      |         |
      |         +---> for each connected peer:
      |         |         probeTarget(peer, relayPeers)
      |         |             |
      |         |             +-- build PathInfo for direct path
      |         |             +-- build PathInfo for each relay path
      |         |             +-- SendProbes() (parallel)
      |         |             +-- update PathSet with results
      |         |
      |         +---> pruneStalePaths()
      |                   |
      |                   +-- remove paths with SuccessRate < 0.1
      |                       and LastProbed > 30 minutes ago
      |
      +---> repeat until ctx.Done()
```

### Policy Selection

File: `internal/pathpolicy/policy.go`

The `Policy` interface defines `Score(path) float64` and `Name() string`. Six implementations exist:

```
  PolicyFromNameWithEpsilon(name, epsilon)
      |
      +-- "latency"        --> LatencyPolicy{}
      +-- "hop-count"      --> HopCountPolicy{}
      +-- "reliability"    --> ReliabilityPolicy{}
      +-- "balanced"       --> BalancedPolicy{0.35, 0.25, 0.25, 0.15}
      +-- "epsilon-greedy" --> EpsilonGreedyPolicy{epsilon, BalancedPolicy}
      +-- "random"         --> RandomPolicy{}
```

The epsilon-greedy policy wraps a delegate (balanced by default):

```
  SelectPath(paths):
      filter to viable paths (SampleCount > 0)
      if rand() < epsilon:
          return random viable path      // explore
      else:
          return max(delegate.Score(p))   // exploit
```

### Disjoint Path Selection

```
  DisjointPaths(target, n):
      1. Get all paths to target
      2. Score and sort descending
      3. Greedy selection:
         for each path (best-first):
             if no relay in path overlaps with already-selected:
                 select it
                 mark its relays as used
         until n paths selected or no more candidates
```

## Content Layer (Layer 3)

### Chunking and CIDs

File: `internal/content/chunker.go`

```
  Chunk(reader, chunkSize)
      |
      +-- read chunkSize bytes at a time
      +-- for each chunk:
      |       CID = hex(SHA-256(data))
      |       yield Block{CID, Data}
      |
      +-- BuildManifest(name, totalSize, blocks)
              RootCID = hex(SHA-256(concat(all chunk CIDs)))
              return Manifest{RootCID, Name, TotalSize, ChunkCIDs}
```

CIDs are hex-encoded SHA-256 hashes. The root CID is the hash of all chunk CIDs concatenated, providing a Merkle-like integrity guarantee.

### Storage

File: `internal/content/store.go`

Blocks are stored as individual files on disk:

```
  {DataDir}/blocks/
      {cid}.block              raw block data
      {rootCID}.manifest.json  JSON manifest
  {DataDir}/pins.json          pinned CID list
```

The Store is protected by a `sync.RWMutex`. Pin state is persisted to `pins.json` and loaded on startup.

### Content Routing

File: `internal/content/routing.go`

```
  ProvideBlocks(manifest):
      DHT.Provide(rootCID)
      for each chunkCID:
          DHT.Provide(chunkCID)

  FindProviders(cid, maxPeers):
      DHT.FindProvidersAsync(cid, maxPeers)
      collect from channel with 15s timeout
```

CIDs are converted from hex strings to CIDv1 (multihash-wrapped) for DHT compatibility.

### Replication

File: `internal/content/replication.go`

The `ReplicationTracker` maintains a map of CID to fetch count. When a block's fetch count crosses the threshold (default: 5), it becomes eligible for proactive replication.

```
  RecordFetch(cid):
      count = atomicAdd(fetchCount[cid], 1)
      return count == threshold   // trigger exactly once

  PopularBlocks():
      return all CIDs where fetchCount >= threshold
```

The Node's replication loop (every 60s) calls `PopularBlocks()`, fetches each block from the local store, and pushes it to connected peers via the block-push protocol.

## Wire Protocols

### Ping (`/pathaware-libp2p/ping/1.0.0`)

File: `internal/protocol/ping.go`

```
  Client                          Server
    |                               |
    |-- 8B timestamp (ns, BE) ---->|
    |                               |
    |<-- 8B echo ------------------|
    |                               |
  RTT = time.Since(sent)
```

### Probe (`/pathaware-libp2p/probe/1.0.0`)

File: `internal/protocol/probe.go`

```
  Client                          Server
    |                               |
    |-- 53B payload --------------> |
    |   [8B ts][4B pathID][1B hop]  |
    |   [4B throughput][4B jitter]  |  (zeros from client)
    |   [32B nonce]                 |
    |                               |
    |                          hop++
    |                               |
    |<-- 53B echo (hop incr.) ----- |
    |                               |
  RTT = time.Since(sent)
  hopCount = echo[12]
  verify nonce[21..52] matches
  verify pathID matches
```

### Block Transfer (`/pathaware-libp2p/block/1.0.0`)

File: `internal/protocol/blocktransfer.go`

```
  Client (fetch)                   Server
    |                               |
    |-- [2B cidLen][cidBytes] ----->|
    |-- CloseWrite                  |
    |                               |-- lookup in cache, then disk
    |                               |
    |   Found:                      |
    |<-- [0x01][4B dataLen][data] --|
    |                               |
    |   Not found:                  |
    |<-- [0x00][2B errLen][err] ----|
    |                               |
  verify: ComputeCID(data) == cid
```

Server caches served blocks in the LRU cache (NDN-style relay caching).

### Block Push (`/pathaware-libp2p/block-push/1.0.0`)

File: `internal/protocol/blocktransfer.go`

```
  Client (push)                    Server
    |                               |
    |-- [2B cidLen][cidBytes] ----->|
    |-- [4B dataLen][data] -------> |
    |-- CloseWrite                  |
    |                               |-- verify CID
    |                               |-- store to disk
    |                               |-- cache in memory
    |                               |
    |<-- [0x01] ack ----------------|  (or [0x00] reject)
```

## Node Lifecycle

File: `internal/node/node.go`

```
  Node.Start(ctx):
      1. Initialize Prometheus metrics (New())
      2. Create libp2p host with metrics registry
      3. Register protocol handlers:
         - PingHandler
         - ProbeHandler
         - BlockTransferHandler
         - BlockPushHandler
      4. Create ContentStore, load pins
      5. Create BlockCache, attach pin checker
      6. Create ReplicationTracker (threshold=5)
      7. Parse bootstrap peers
      8. Setup DHT (Kademlia, /pathaware-libp2p prefix)
      9. Create ContentRouter (DHT wrapper)
     10. Enable relay service (if configured)
     11. Start mDNS (if configured)
     12. Start Prometheus HTTP server
     13. Create PathManager, start probe loop
     14. Start replication goroutine (every 60s)

  Node.Stop():
      1. Stop PathManager
      2. Cancel context (stops replication loop)
      3. Close DHT
      4. Close Host
```

## Parallel Block Fetching

File: `internal/node/api.go`

The fetch handler processes chunks in batches of 4 concurrent goroutines:

```
  handleFetch():
      get manifest
      verify signature (if present)
      allocate blocks[len(chunkCIDs)]

      for batch = 0; batch < len(chunks); batch += 4:
          for i in batch..min(batch+4, len):
              go func(i):
                  try cache -> try local store -> try network
                  blocks[i] = result

          wait for batch to complete
          if any error: return 404

      write all blocks to response in order
```

Network fetching (`fetchBlockFromNetwork`) sorts providers by path quality:

```
  fetchBlockFromNetwork(cid):
      providers = ContentRouter.FindProviders(cid, 10)
      filter out self
      sort by path quality: successRate / avgRTT
      try providers in order until one succeeds
      record metrics: fetch duration, blocks transferred, path type
```

## Metrics Integration

File: `internal/metrics/metrics.go`

Each node gets its own `prometheus.Registry` to avoid panics in multi-node test scenarios. The registry is:

1. Used to register 14 application-level metrics
2. Passed to the libp2p host via `PrometheusRegisterer()` to enable ~30 transport-level metrics
3. Served via a dedicated HTTP endpoint (`/metrics`)
