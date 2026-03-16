#!/bin/bash
set -e

# Apply network emulation if configured.
# LATENCY_MS: added delay in milliseconds
# LOSS_PCT: packet loss percentage
# JITTER_MS: random jitter in milliseconds
if [ -n "$LATENCY_MS" ] || [ -n "$LOSS_PCT" ]; then
    DELAY="${LATENCY_MS:-0}ms"
    LOSS="${LOSS_PCT:-0}%"
    JITTER="${JITTER_MS:-0}ms"

    echo "Applying tc netem: delay=${DELAY} jitter=${JITTER} loss=${LOSS}"
    tc qdisc add dev eth0 root netem delay ${DELAY} ${JITTER} loss ${LOSS} 2>/dev/null || \
    tc qdisc change dev eth0 root netem delay ${DELAY} ${JITTER} loss ${LOSS}
fi

# If BOOTSTRAP_HOST is set, wait for it and discover its peer ID via API.
if [ -n "$BOOTSTRAP_HOST" ]; then
    echo "Waiting for bootstrap node at ${BOOTSTRAP_HOST}..."
    for i in $(seq 1 30); do
        PEER_ID=$(curl -sf "http://${BOOTSTRAP_HOST}:9090/api/v1/status" 2>/dev/null | jq -r '.peer_id // empty' 2>/dev/null) || true
        if [ -n "$PEER_ID" ]; then
            BOOTSTRAP_ADDR="/dns4/${BOOTSTRAP_HOST}/tcp/9000/p2p/${PEER_ID}"
            echo "Discovered bootstrap peer: ${BOOTSTRAP_ADDR}"
            break
        fi
        sleep 1
    done
    if [ -z "$PEER_ID" ]; then
        echo "WARNING: Could not discover bootstrap peer, starting without bootstrap"
    fi
fi

# Start the daemon with provided arguments.
if [ -n "$BOOTSTRAP_ADDR" ]; then
    exec scion-libp2p daemon "$@" --bootstrap "$BOOTSTRAP_ADDR"
else
    exec scion-libp2p daemon "$@"
fi
