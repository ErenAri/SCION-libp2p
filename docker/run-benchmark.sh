#!/bin/bash
# Run WAN simulation benchmarks across Docker containers.
# Prerequisites: docker compose -f docker/docker-compose.wan.yml up
set -e

PUBLISHER="http://localhost:9090"

echo "=== WAN Simulation Benchmark ==="
echo ""

# Wait for nodes to be ready.
echo "Waiting for nodes to become healthy..."
for i in {1..30}; do
    if curl -sf "$PUBLISHER/health" > /dev/null 2>&1; then
        echo "Publisher is ready."
        break
    fi
    sleep 1
done

# Check peer count.
echo ""
echo "--- Peer Status ---"
PEERS=$(curl -sf "$PUBLISHER/api/v1/peers" | jq length)
echo "Publisher sees $PEERS peers"

# Check path diversity.
echo ""
echo "--- Path Diversity ---"
curl -sf "$PUBLISHER/api/v1/paths" | jq '.paths[] | {id, type, avg_rtt, success_rate}' 2>/dev/null || echo "No paths yet (probing may still be running)"

# Create test content.
echo ""
echo "--- Publishing Test Content ---"
TMPFILE=$(mktemp)
dd if=/dev/urandom of="$TMPFILE" bs=1024 count=1024 2>/dev/null
echo "Created 1MB test file: $TMPFILE"

# Note: In Docker, the publish endpoint expects a file path inside the container.
# For a real benchmark, you'd docker cp the file in or mount a volume.
echo "To run a full benchmark, use:"
echo "  docker exec scion-node1 scion-libp2p bench --experiment compare --nodes 5"
echo ""

# Show latency distribution from paths.
echo "--- Latency Distribution ---"
curl -sf "$PUBLISHER/api/v1/paths" | jq '[.paths[] | .avg_rtt] | sort' 2>/dev/null || echo "No path data available"

rm -f "$TMPFILE"
echo ""
echo "Benchmark complete. Check Grafana at http://localhost:3000 for metrics."
