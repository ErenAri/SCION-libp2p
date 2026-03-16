package protocol

import "github.com/libp2p/go-libp2p/core/protocol"

const (
	BlockTransferID  protocol.ID = "/pathaware-libp2p/block/1.0.0"
	PathProbeID      protocol.ID = "/pathaware-libp2p/probe/1.0.0"
	PathAnnounceID   protocol.ID = "/pathaware-libp2p/path-announce/1.0.0"
	PingID           protocol.ID = "/pathaware-libp2p/ping/1.0.0"
	CacheSummaryID   protocol.ID = "/pathaware-libp2p/cache-summary/1.0.0"
)
