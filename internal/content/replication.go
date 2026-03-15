package content

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// ReplicationTracker monitors block fetch popularity and identifies
// candidates for auto-replication to improve fault tolerance.
type ReplicationTracker struct {
	mu         sync.Mutex
	fetchCount map[string]*atomic.Int64 // CID -> fetch count
	threshold  int64                    // replicate after this many fetches
}

// NewReplicationTracker creates a tracker with the given popularity threshold.
func NewReplicationTracker(threshold int64) *ReplicationTracker {
	return &ReplicationTracker{
		fetchCount: make(map[string]*atomic.Int64),
		threshold:  threshold,
	}
}

// RecordFetch increments the fetch counter for a block and returns whether
// the block has crossed the replication threshold.
func (rt *ReplicationTracker) RecordFetch(cid string) bool {
	rt.mu.Lock()
	counter, ok := rt.fetchCount[cid]
	if !ok {
		counter = &atomic.Int64{}
		rt.fetchCount[cid] = counter
	}
	rt.mu.Unlock()

	count := counter.Add(1)
	return count == rt.threshold // trigger replication exactly once
}

// PopularBlocks returns all CIDs that have exceeded the replication threshold.
func (rt *ReplicationTracker) PopularBlocks() []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	var popular []string
	for cid, counter := range rt.fetchCount {
		if counter.Load() >= rt.threshold {
			popular = append(popular, cid)
		}
	}
	return popular
}

// Stats returns the current state of the tracker.
type ReplicationStats struct {
	TrackedBlocks  int            `json:"tracked_blocks"`
	PopularBlocks  int            `json:"popular_blocks"`
	FetchCounts    map[string]int `json:"fetch_counts"`
}

func (rt *ReplicationTracker) Stats() ReplicationStats {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	stats := ReplicationStats{
		TrackedBlocks: len(rt.fetchCount),
		FetchCounts:   make(map[string]int, len(rt.fetchCount)),
	}

	for cid, counter := range rt.fetchCount {
		count := int(counter.Load())
		stats.FetchCounts[cid] = count
		if int64(count) >= rt.threshold {
			stats.PopularBlocks++
		}
	}

	return stats
}

// ReplicatePopular sends popular blocks to the given store (simulating
// push-based replication to nearby peers). In production, this would
// use ContentRouter.Provide() + block push to K closest peers.
func ReplicatePopular(ctx context.Context, rt *ReplicationTracker, src *Store, dst *Store) int {
	popular := rt.PopularBlocks()
	replicated := 0

	for _, cid := range popular {
		block, err := src.Get(cid)
		if err != nil {
			continue
		}

		if err := dst.Put(block); err != nil {
			slog.Debug("replication failed", "cid", cid[:min(8, len(cid))], "err", err)
			continue
		}
		replicated++
	}

	return replicated
}
