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

# Start the daemon with provided arguments.
exec scion-libp2p daemon "$@"
