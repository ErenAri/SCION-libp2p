package transport

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	ma "github.com/multiformats/go-multiaddr"
)

const mdnsServiceTag = "scion-libp2p.local"

// SetupDHT initializes the Kademlia DHT for peer and content discovery.
func SetupDHT(ctx context.Context, h host.Host, bootstrapPeers []peer.AddrInfo) (*dht.IpfsDHT, error) {
	opts := []dht.Option{
		dht.Mode(dht.ModeAutoServer),
		dht.ProtocolPrefix("/scion-libp2p"),
	}

	d, err := dht.New(ctx, h, opts...)
	if err != nil {
		return nil, fmt.Errorf("create DHT: %w", err)
	}

	if err := d.Bootstrap(ctx); err != nil {
		return nil, fmt.Errorf("bootstrap DHT: %w", err)
	}

	// Connect to bootstrap peers.
	var wg sync.WaitGroup
	for _, pi := range bootstrapPeers {
		wg.Add(1)
		go func(pi peer.AddrInfo) {
			defer wg.Done()
			if err := h.Connect(ctx, pi); err != nil {
				slog.Warn("failed to connect to bootstrap peer",
					"peer", pi.ID.String(),
					"err", err,
				)
			} else {
				slog.Info("connected to bootstrap peer", "peer", pi.ID.String())
			}
		}(pi)
	}
	wg.Wait()

	slog.Info("DHT initialized", "mode", "auto-server")
	return d, nil
}

// ParseBootstrapPeers parses multiaddr strings into peer.AddrInfo.
func ParseBootstrapPeers(addrs []string) ([]peer.AddrInfo, error) {
	var peers []peer.AddrInfo
	for _, s := range addrs {
		maddr, err := ma.NewMultiaddr(s)
		if err != nil {
			return nil, fmt.Errorf("parse multiaddr %q: %w", s, err)
		}
		pi, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			return nil, fmt.Errorf("parse peer info from %q: %w", s, err)
		}
		peers = append(peers, *pi)
	}
	return peers, nil
}

// SetupMDNS starts mDNS-based local peer discovery.
func SetupMDNS(h host.Host) error {
	n := &discoveryNotifee{h: h}
	svc := mdns.NewMdnsService(h, mdnsServiceTag, n)
	if err := svc.Start(); err != nil {
		return fmt.Errorf("start mDNS: %w", err)
	}
	slog.Info("mDNS discovery started", "tag", mdnsServiceTag)
	return nil
}

type discoveryNotifee struct {
	h host.Host
}

func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	if pi.ID == n.h.ID() {
		return // skip self
	}
	slog.Debug("mDNS peer found", "peer", pi.ID.String())
	if err := n.h.Connect(context.Background(), pi); err != nil {
		slog.Debug("mDNS connect failed", "peer", pi.ID.String(), "err", err)
	} else {
		slog.Info("connected via mDNS", "peer", pi.ID.String())
	}
}
