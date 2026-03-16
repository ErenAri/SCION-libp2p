# Monitoring Guide

This document explains how to set up and use the monitoring stack (Prometheus + Grafana) for pathaware-libp2p.

## Quick Setup

```bash
# Start the monitoring stack
docker compose up -d

# Verify services are running
curl http://localhost:9091/-/ready    # Prometheus
curl http://localhost:3000/api/health # Grafana
```

Access Grafana at `http://localhost:3000` with credentials `admin` / `scion`.

The dashboard "PathAware-libp2p" is auto-provisioned and available immediately.

## Architecture

```
  pathaware-libp2p nodes                 Monitoring Stack
  --------------------               ----------------

  Node 0 (:2112/metrics) ------+
                                |    +------------+     +---------+
  Node 1 (:2113/metrics) ------+---->| Prometheus |---->| Grafana |
                                |    |  :9091     |     | :3000   |
  Node 2 (:2114/metrics) ------+     +-----------+      +---------+
                                         scrape           query
                                         every 5s
```

## Prometheus Configuration

File: `monitoring/prometheus.yml`

The default configuration scrapes three node instances on `host.docker.internal:2112-2114`. To monitor different nodes, edit the `static_configs` section:

```yaml
scrape_configs:
  - job_name: 'pathaware-libp2p'
    static_configs:
      - targets: ['host.docker.internal:2112']
        labels:
          instance: 'node-0'
      - targets: ['host.docker.internal:2113']
        labels:
          instance: 'node-1'
```

After editing, restart Prometheus:

```bash
docker compose restart prometheus
```

## Starting Nodes with Metrics

Each node must use a unique metrics port:

```bash
# Node 0
./pathaware-libp2p daemon --listen /ip4/127.0.0.1/tcp/9000 \
  --api 127.0.0.1:9090 --metrics-addr :2112

# Node 1
./pathaware-libp2p daemon --listen /ip4/127.0.0.1/tcp/9001 \
  --api 127.0.0.1:9091 --metrics-addr :2113

# Node 2
./pathaware-libp2p daemon --listen /ip4/127.0.0.1/tcp/9002 \
  --api 127.0.0.1:9092 --metrics-addr :2114
```

## Dashboard Panels

The Grafana dashboard (`dashboards/pathaware-libp2p.json`) contains 10 panels:

```
  Row 1: Overview gauges and rates
  +------------+----------------+------------+------------+----------+
  | Connected  | Content        | Cache Hit  | Blocks     | Cache    |
  | Peers      | Retrievals/s   | Ratio      | Replicated | Size     |
  | (gauge)    | (timeseries)   | (gauge)    | (stat)     | (stat)   |
  +------------+----------------+------------+------------+----------+

  Row 2: Latency analysis
  +----------------------------+-----------------------------+
  | Path RTT Distribution      | Block Fetch Latency         |
  | (direct vs relay,          | by Source                   |
  |  p50 and p95)              | (local, cache, network)     |
  +----------------------------+-----------------------------+

  Row 3: Operations breakdown
  +------------------+------------------+---------------------+
  | Path Selections  | Blocks           | Probe Failures +    |
  | by Type          | Transferred/s    | Stale Paths         |
  | (pie chart)      | (sent/received)  | Pruned              |
  +------------------+------------------+---------------------+
```

### Key Queries

**Cache hit ratio:**
```promql
pathaware_libp2p_cache_hits_total / (pathaware_libp2p_cache_hits_total + pathaware_libp2p_cache_misses_total)
```

**Path RTT p50 (direct):**
```promql
histogram_quantile(0.50, rate(pathaware_libp2p_probe_rtt_seconds_bucket{path_type="direct"}[5m]))
```

**Block fetch latency p50 (network):**
```promql
histogram_quantile(0.50, rate(pathaware_libp2p_block_fetch_duration_seconds_bucket{source="network"}[5m]))
```

**Content retrieval rate:**
```promql
rate(pathaware_libp2p_content_retrievals_total[1m])
```

## Metrics Reference

### Application Metrics (pathaware_libp2p_*)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `peers_connected` | gauge | -- | Currently connected peers |
| `relay_peers_available` | gauge | -- | Relay-capable connected peers |
| `probe_rtt_seconds` | histogram | `path_type` | Probe round-trip time |
| `probe_failures_total` | counter | -- | Failed probe attempts |
| `ping_rtt_seconds` | histogram | `target_peer` | Ping round-trip time |
| `cache_hits_total` | counter | -- | Block cache hits |
| `cache_misses_total` | counter | -- | Block cache misses |
| `cache_bytes` | gauge | -- | Current cache size |
| `block_fetch_duration_seconds` | histogram | `source` | Block fetch time |
| `blocks_transferred_total` | counter | `direction` | Blocks sent/received |
| `content_retrievals_total` | counter | -- | Content retrieval ops |
| `path_selections_total` | counter | `path_type` | Path selections |
| `stale_paths_pruned_total` | counter | -- | Pruned stale paths |
| `blocks_replicated_total` | counter | -- | Proactively replicated |

Label values:
- `path_type`: `"direct"` or `"relay"`
- `source`: `"local"`, `"cache"`, or `"network"`
- `direction`: `"sent"` or `"received"`

### libp2p Built-in Metrics

When `PrometheusRegisterer` is configured (enabled by default), libp2p exports approximately 30 additional metrics covering:

- Connection counts by direction and transport
- Stream counts by protocol and direction
- Bytes sent/received
- DHT query counts and latencies
- Resource manager limits and usage

These use the `libp2p_` prefix and are automatically scraped alongside application metrics.

## Alerting (Optional)

Example Prometheus alerting rules for production use:

```yaml
groups:
  - name: pathaware-libp2p
    rules:
      - alert: NoPeersConnected
        expr: pathaware_libp2p_peers_connected == 0
        for: 5m
        annotations:
          summary: "Node has no connected peers for 5 minutes"

      - alert: HighProbeFailureRate
        expr: rate(pathaware_libp2p_probe_failures_total[5m]) > 0.5
        for: 2m
        annotations:
          summary: "Probe failure rate exceeds 50%"

      - alert: CacheFullNoEviction
        expr: pathaware_libp2p_cache_bytes / 134217728 > 0.95
        for: 10m
        annotations:
          summary: "Cache is above 95% capacity"
```

## Troubleshooting

**Prometheus cannot reach nodes:**

If running Docker on Windows, ensure `host.docker.internal` resolves correctly. The `docker-compose.yml` includes `extra_hosts: host.docker.internal:host-gateway` for this purpose.

Verify the node's metrics endpoint is accessible:
```bash
curl http://localhost:2112/metrics
```

**Grafana shows "No data":**

1. Check Prometheus targets at `http://localhost:9091/targets` -- all should be "UP"
2. Wait for at least one scrape interval (5 seconds)
3. Verify the node is running and producing metrics

**Dashboard not appearing:**

The dashboard is auto-provisioned via `monitoring/grafana-dashboards.yml`. If it does not appear, check Grafana logs:
```bash
docker compose logs grafana
```

## Stopping

```bash
docker compose down
```

Add `-v` to also remove stored data:
```bash
docker compose down -v
```
