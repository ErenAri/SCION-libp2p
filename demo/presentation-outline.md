# scion-libp2p -- Presentation Outline (2 Minutes)

## Slide 1: Problem (20 seconds)

Title: "The Herd Effect in P2P Content Delivery"

- P2P networks like IPFS/libp2p treat all paths equally
- When multiple paths exist, greedy selection causes everyone to pile onto
  the "best" path
- Result: congestion collapse on the popular path, wasted capacity on others
- Diagram: all arrows converging on one path, then that path turning red

Talking point: "In path-aware networks, rational individual optimization
produces collectively suboptimal outcomes. Everyone picks the fastest path,
it gets congested, and everyone suffers."

## Slide 2: Solution (20 seconds)

Title: "Epsilon-Greedy Path Selection"

- Probe all paths continuously (RTT, jitter, hops, success rate)
- 90% of the time: pick the best-scored path (exploit)
- 10% of the time: pick a random viable path (explore)
- Result: load distributes naturally, tail latency drops significantly

Talking point: "We borrow from multi-armed bandit theory. By exploring
alternative paths 10% of the time, we discover improving routes and prevent
congestion on any single path."

## Slide 3: Live Demo (40 seconds)

Show terminal running the demo script:

1. Three nodes start, discover each other via mDNS
2. Publish a file on Node A -- show the CID
3. Fetch from Node B -- show path selection happening in real time
4. Kill Node A -- fetch again -- content still available from relay cache
5. Point to Grafana dashboard showing path RTT and cache hit ratio

Talking point: "Notice the content survived the publisher going offline.
Relay node R cached the blocks during the first fetch -- this is NDN-style
in-network caching. No configuration needed."

## Slide 4: Results (20 seconds)

Title: "Epsilon-Greedy Beats Greedy Under Load"

Bar chart comparing P95 latency:
- Epsilon-greedy: 18.5 ms
- Greedy min-RTT: 24.2 ms (30% worse)
- Random: 28.9 ms (56% worse)

Line graph showing scalability:
- Greedy degrades 183% at 25 nodes
- Epsilon-greedy degrades only 75%

Talking point: "The slightly higher average latency is the cost of
exploration. The dramatically lower tail latency is the payoff."

## Slide 5: What Makes This Different (20 seconds)

Title: "Novel Combination"

- First system combining SCION path awareness + libp2p content delivery
- Path-aware: probes, scores, selects (not path-oblivious)
- Content-aware: NDN caching, popularity-aware eviction, replication
- Evaluated: three-way comparison, scalability, resilience
- Open source, fully tested, paper draft ready

Talking point: "No prior system combines path awareness with content
delivery in a P2P overlay. We bring SCION's philosophy to existing
networks without infrastructure changes."

## Closing

"scion-libp2p: path-aware content delivery that avoids congestion
through controlled exploration."

Repository: github.com/ErenAri/SCION-libp2p

## Backup Slides

### B1: Architecture Diagram

Five-layer stack with dependency arrows. Reference the ASCII art from
the README or use a cleaned-up version.

### B2: Probe Wire Format

53-byte layout diagram showing timestamp, pathID, hop counter, jitter,
throughput, nonce fields.

### B3: Cache Eviction Algorithm

Step-by-step walkthrough of popularity-aware eviction with the
second-chance mechanism.

### B4: Disjoint Path Selection

Example showing how 4 candidate paths get filtered to 3 disjoint paths
by excluding paths that share relay peers.
