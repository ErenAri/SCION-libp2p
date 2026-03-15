package transport

import (
	"fmt"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
)

// EnableRelayService starts the circuit relay v2 service on the host,
// allowing other peers to relay connections through this node.
func EnableRelayService(h host.Host) error {
	_, err := relay.New(h)
	if err != nil {
		return fmt.Errorf("enable relay service: %w", err)
	}
	slog.Info("circuit relay v2 service enabled")
	return nil
}

// BuildRelayAddr constructs a circuit-relay multiaddr to reach the target via a relay.
// Format: <relay-addr>/p2p/<relayID>/p2p-circuit/p2p/<targetID>
func BuildRelayAddr(relayID, targetID peer.ID, relayAddrs []ma.Multiaddr) (ma.Multiaddr, error) {
	if len(relayAddrs) == 0 {
		return nil, fmt.Errorf("no relay addresses available")
	}

	// Use the first address of the relay.
	base := relayAddrs[0]

	relayComponent, err := ma.NewComponent("p2p", relayID.String())
	if err != nil {
		return nil, fmt.Errorf("create relay p2p component: %w", err)
	}

	circuitComponent, err := ma.NewComponent("p2p-circuit", "")
	if err != nil {
		return nil, fmt.Errorf("create circuit component: %w", err)
	}

	targetComponent, err := ma.NewComponent("p2p", targetID.String())
	if err != nil {
		return nil, fmt.Errorf("create target p2p component: %w", err)
	}

	relayAddr := base.Encapsulate(relayComponent).Encapsulate(circuitComponent).Encapsulate(targetComponent)
	return relayAddr, nil
}

// EnumerateRelayPeers returns connected peers that support the relay protocol.
func EnumerateRelayPeers(h host.Host) []peer.AddrInfo {
	var relays []peer.AddrInfo
	for _, p := range h.Network().Peers() {
		protos, err := h.Peerstore().GetProtocols(p)
		if err != nil {
			continue
		}
		for _, proto := range protos {
			if proto == "/libp2p/circuit/relay/0.2.0/hop" {
				relays = append(relays, peer.AddrInfo{
					ID:    p,
					Addrs: h.Peerstore().Addrs(p),
				})
				break
			}
		}
	}
	return relays
}
