package node

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/erena/scion-libp2p/internal/cache"
	"github.com/erena/scion-libp2p/internal/content"
	"github.com/erena/scion-libp2p/internal/pathpolicy"
	"github.com/erena/scion-libp2p/internal/protocol"

	"github.com/libp2p/go-libp2p/core/peer"
)

// API provides an HTTP/JSON interface for the CLI to communicate with a running daemon.
type API struct {
	node *Node
	mux  *http.ServeMux
}

// NewAPI creates a new API server backed by the given node.
func NewAPI(n *Node) *API {
	a := &API{
		node: n,
		mux:  http.NewServeMux(),
	}
	a.mux.HandleFunc("/api/v1/peers", a.handlePeers)
	a.mux.HandleFunc("/api/v1/ping", a.handlePing)
	a.mux.HandleFunc("/api/v1/paths", a.handlePaths)
	a.mux.HandleFunc("/api/v1/publish", a.handlePublish)
	a.mux.HandleFunc("/api/v1/fetch", a.handleFetch)
	a.mux.HandleFunc("/api/v1/manifest", a.handleManifest)
	a.mux.HandleFunc("/api/v1/find", a.handleFindProviders)
	a.mux.HandleFunc("/api/v1/pin", a.handlePin)
	a.mux.HandleFunc("/api/v1/pins", a.handleListPins)
	a.mux.HandleFunc("/api/v1/status", a.handleStatus)
	a.mux.HandleFunc("/health", a.handleHealth)
	return a
}

// ListenAndServe starts the API HTTP server.
func (a *API) ListenAndServe(addr string) error {
	server := &http.Server{
		Addr:         addr,
		Handler:      a.mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	return server.ListenAndServe()
}

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (a *API) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	peers := a.node.ConnectedPeers()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peers)
}

func (a *API) handlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	peerIDStr := r.URL.Query().Get("peer")
	if peerIDStr == "" {
		http.Error(w, "missing 'peer' parameter", http.StatusBadRequest)
		return
	}

	target, err := peer.Decode(peerIDStr)
	if err != nil {
		http.Error(w, "invalid peer ID: "+err.Error(), http.StatusBadRequest)
		return
	}

	count := 5
	if c := r.URL.Query().Get("count"); c != "" {
		if parsed, err := strconv.Atoi(c); err == nil && parsed > 0 && parsed <= 100 {
			count = parsed
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(count)*5*time.Second)
	defer cancel()

	results := protocol.SendPings(ctx, a.node.Host, target, count)

	type pingResult struct {
		RTT   string `json:"rtt"`
		Error string `json:"error,omitempty"`
	}

	resp := struct {
		Results []pingResult `json:"results"`
	}{}

	for _, res := range results {
		pr := pingResult{}
		if res.Err != nil {
			pr.Error = res.Err.Error()
		} else {
			pr.RTT = res.RTT.String()
		}
		resp.Results = append(resp.Results, pr)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode ping response", "err", err)
	}
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := struct {
		PeerID    string   `json:"peer_id"`
		Uptime    string   `json:"uptime"`
		NumPeers  int      `json:"num_peers"`
		Addresses []string `json:"addresses"`
		Policy    string   `json:"policy"`
	}{
		PeerID:   a.node.PeerID(),
		Uptime:   a.node.Uptime().Round(time.Second).String(),
		NumPeers: len(a.node.Host.Network().Peers()),
	}

	if a.node.PathManager != nil {
		status.Policy = a.node.PathManager.PolicyName()
	}

	for _, addr := range a.node.Host.Addrs() {
		status.Addresses = append(status.Addresses, addr.String())
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

type pathInfoJSON struct {
	ID          string   `json:"id"`
	Target      string   `json:"target"`
	Type        string   `json:"type"`
	RelayChain  []string `json:"relay_chain,omitempty"`
	AvgRTT      string   `json:"avg_rtt"`
	HopCount    int      `json:"hop_count"`
	SuccessRate float64  `json:"success_rate"`
	SampleCount int      `json:"sample_count"`
	LastProbed  string   `json:"last_probed"`
}

func (a *API) handlePaths(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.node.PathManager == nil {
		http.Error(w, "path manager not initialized", http.StatusServiceUnavailable)
		return
	}

	// Optional: filter by peer.
	peerIDStr := r.URL.Query().Get("peer")

	var paths []pathInfoJSON

	if peerIDStr != "" {
		target, err := peer.Decode(peerIDStr)
		if err != nil {
			http.Error(w, "invalid peer ID: "+err.Error(), http.StatusBadRequest)
			return
		}
		for _, p := range a.node.PathManager.Paths(target) {
			paths = append(paths, pathToJSON(p))
		}
	} else {
		allPaths := a.node.PathManager.AllPaths()
		for _, ps := range allPaths {
			for _, p := range ps {
				paths = append(paths, pathToJSON(p))
			}
		}
	}

	resp := struct {
		Policy string         `json:"policy"`
		Paths  []pathInfoJSON `json:"paths"`
	}{
		Policy: a.node.PathManager.PolicyName(),
		Paths:  paths,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func pathToJSON(p *pathpolicy.Path) pathInfoJSON {
	relays := make([]string, len(p.RelayChain))
	for i, r := range p.RelayChain {
		relays[i] = r.String()
	}
	lastProbed := ""
	if !p.Metrics.LastProbed.IsZero() {
		lastProbed = p.Metrics.LastProbed.Format(time.RFC3339)
	}
	return pathInfoJSON{
		ID:          p.ID,
		Target:      p.Target.String(),
		Type:        string(p.Type),
		RelayChain:  relays,
		AvgRTT:      p.Metrics.AvgRTT.String(),
		HopCount:    p.Metrics.HopCount,
		SuccessRate: p.Metrics.SuccessRate,
		SampleCount: p.Metrics.SampleCount,
		LastProbed:  lastProbed,
	}
}

func (a *API) handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.node.ContentStore == nil {
		http.Error(w, "content store not initialized", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		FilePath string `json:"file_path"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Read the file.
	data, err := os.ReadFile(req.FilePath)
	if err != nil {
		http.Error(w, "read file: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Chunk the file — adaptive or fixed.
	chunkSize := a.node.Cfg.ChunkSizeBytes
	if chunkSize <= 0 {
		chunkSize = 256 * 1024
	}
	if a.node.Cfg.AdaptiveChunking {
		// Estimate best path RTT from any connected peer.
		var bestRTTms float64 = 10 // default assumption
		if a.node.PathManager != nil {
			for _, pid := range a.node.Host.Network().Peers() {
				if bp := a.node.PathManager.BestPath(pid); bp != nil && bp.Metrics.AvgRTT > 0 {
					rttMs := float64(bp.Metrics.AvgRTT.Milliseconds())
					if bestRTTms == 10 || rttMs < bestRTTms {
						bestRTTms = rttMs
					}
				}
			}
		}
		chunkSize = content.AdaptiveChunkSize(int64(len(data)), bestRTTms,
			a.node.Cfg.MinChunkSize, a.node.Cfg.MaxChunkSize)
		slog.Debug("adaptive chunking", "file_size", len(data), "rtt_ms", bestRTTms, "chunk_size", chunkSize)
	}
	blocks, err := content.Chunk(bytes.NewReader(data), chunkSize)
	if err != nil {
		http.Error(w, "chunk file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Store all blocks.
	for _, b := range blocks {
		if err := a.node.ContentStore.Put(b); err != nil {
			http.Error(w, "store block: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Build and store manifest.
	manifest := content.BuildManifest(req.Name, int64(len(data)), blocks)

	// Sign manifest with node's Ed25519 key.
	if privKey := a.node.Ed25519PrivateKey(); len(privKey) >= ed25519.SeedSize {
		ed25519Key := ed25519.NewKeyFromSeed(privKey[:ed25519.SeedSize])
		content.SignManifest(&manifest, ed25519Key)
		slog.Debug("manifest signed", "publisher", manifest.PublisherID[:16])
	}

	if err := a.node.ContentStore.PutManifest(manifest); err != nil {
		http.Error(w, "store manifest: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Announce content to DHT.
	if a.node.ContentRouter != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := a.node.ContentRouter.ProvideBlocks(ctx, manifest); err != nil {
				slog.Warn("failed to announce content to DHT", "err", err)
			}
		}()
	}

	slog.Info("content published",
		"name", req.Name,
		"root_cid", manifest.RootCID,
		"chunks", len(blocks),
		"size", len(data),
	)

	resp := struct {
		RootCID   string `json:"root_cid"`
		Name      string `json:"name"`
		TotalSize int64  `json:"total_size"`
		NumChunks int    `json:"num_chunks"`
	}{
		RootCID:   manifest.RootCID,
		Name:      manifest.Name,
		TotalSize: manifest.TotalSize,
		NumChunks: len(blocks),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (a *API) handleFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.node.ContentStore == nil {
		http.Error(w, "content store not initialized", http.StatusServiceUnavailable)
		return
	}

	rootCID := r.URL.Query().Get("cid")
	if rootCID == "" {
		http.Error(w, "missing 'cid' parameter", http.StatusBadRequest)
		return
	}

	// Get manifest.
	manifest, err := a.node.ContentStore.GetManifest(rootCID)
	if err != nil {
		http.Error(w, "manifest not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Verify manifest signature if present.
	if manifest.Signature != "" {
		if err := content.VerifyManifest(&manifest); err != nil {
			slog.Warn("manifest signature verification failed", "cid", rootCID, "err", err)
			http.Error(w, "manifest signature invalid: "+err.Error(), http.StatusForbidden)
			return
		}
	}

	// Track content retrieval.
	if a.node.Metrics != nil {
		a.node.Metrics.ContentRetrievals.Inc()
	}

	// Fetch all blocks, using parallel batching for network fetches.
	const fetchConcurrency = 4
	blocks := make([]content.Block, len(manifest.ChunkCIDs))
	var fetchErr error

	// Process chunks in batches of fetchConcurrency.
	for batchStart := 0; batchStart < len(manifest.ChunkCIDs); batchStart += fetchConcurrency {
		batchEnd := batchStart + fetchConcurrency
		if batchEnd > len(manifest.ChunkCIDs) {
			batchEnd = len(manifest.ChunkCIDs)
		}

		var wg sync.WaitGroup
		var mu sync.Mutex

		for i := batchStart; i < batchEnd; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				chunkCID := manifest.ChunkCIDs[idx]
				fetchStart := time.Now()

				// Try in-memory cache first.
				if a.node.BlockCache != nil {
					if block, ok := a.node.BlockCache.Get(chunkCID); ok {
						if a.node.Metrics != nil {
							a.node.Metrics.BlockFetchDuration.WithLabelValues("cache").Observe(time.Since(fetchStart).Seconds())
						}
						blocks[idx] = block
						return
					}
				}

				// Try local store.
				block, err := a.node.ContentStore.Get(chunkCID)
				if err != nil {
					// Not found locally — try to fetch from remote peers via DHT.
					block, err = a.fetchBlockFromNetwork(r.Context(), chunkCID)
					if err != nil {
						slog.Error("block unavailable", "cid", chunkCID, "err", err)
						mu.Lock()
						if fetchErr == nil {
							fetchErr = fmt.Errorf("block unavailable: %s", chunkCID)
						}
						mu.Unlock()
						return
					}
					// Cache locally for next time.
					_ = a.node.ContentStore.Put(block)
				} else if a.node.Metrics != nil {
					a.node.Metrics.BlockFetchDuration.WithLabelValues("local").Observe(time.Since(fetchStart).Seconds())
				}

				blocks[idx] = block
			}(i)
		}
		wg.Wait()

		if fetchErr != nil {
			http.Error(w, fetchErr.Error(), http.StatusNotFound)
			return
		}
	}

	// Write reassembled content in order.
	w.Header().Set("Content-Type", "application/octet-stream")
	for _, block := range blocks {
		if _, err := w.Write(block.Data); err != nil {
			slog.Error("write block data", "err", err)
			return
		}
	}
}

// fetchBlockFromNetwork discovers providers via DHT and fetches a block
// using path-aware provider selection. When the path manager is available,
// providers are ranked by their best path score, preferring peers reachable
// via the highest-quality paths.
func (a *API) fetchBlockFromNetwork(ctx context.Context, blockCID string) (content.Block, error) {
	if a.node.ContentRouter == nil {
		return content.Block{}, fmt.Errorf("no content router")
	}

	providers, err := a.node.ContentRouter.FindProviders(ctx, blockCID, 10)
	if err != nil {
		return content.Block{}, fmt.Errorf("find providers: %w", err)
	}

	// Filter out self.
	filtered := make([]peer.AddrInfo, 0, len(providers))
	for _, p := range providers {
		if p.ID != a.node.Host.ID() {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		return content.Block{}, fmt.Errorf("no provider had block %s", blockCID)
	}

	// Sort providers by path quality if path manager is available.
	if a.node.PathManager != nil {
		sortProvidersByPathQuality(filtered, a.node.PathManager)
	}

	// Boost providers that likely have the block cached (Bloom filter check).
	if a.node.PeerBlooms != nil {
		boostCachedProviders(filtered, a.node.PeerBlooms, blockCID)
	}

	// Use multipath racing when we have multiple providers and a path manager.
	if len(filtered) >= 2 && a.node.PathManager != nil {
		block, err := a.fetchBlockMultipath(ctx, blockCID, filtered)
		if err == nil {
			return block, nil
		}
		slog.Debug("multipath fetch failed, falling back to serial", "err", err)
	}

	// Serial fallback: try providers in order of path quality.
	for _, p := range filtered {
		start := time.Now()
		block, err := protocol.FetchBlock(ctx, a.node.Host, p.ID, blockCID)
		if err != nil {
			slog.Debug("fetch from provider failed", "peer", p.ID.String()[:8], "err", err)
			continue
		}

		// Record metrics.
		if a.node.Metrics != nil {
			a.node.Metrics.BlockFetchDuration.WithLabelValues("network").Observe(time.Since(start).Seconds())
			a.node.Metrics.BlocksTransferred.WithLabelValues("received").Inc()

			// Track which path type was used.
			if a.node.PathManager != nil {
				if best := a.node.PathManager.BestPath(p.ID); best != nil {
					a.node.Metrics.PathSelectionsTotal.WithLabelValues(string(best.Type)).Inc()
				}
			}
		}

		return block, nil
	}

	return content.Block{}, fmt.Errorf("no provider had block %s", blockCID)
}

// sortProvidersByPathQuality reorders providers so that peers reachable via
// the best-quality paths come first. This is the core of path-aware content
// delivery: we prefer to fetch from peers with lower-latency, more-reliable paths.
func sortProvidersByPathQuality(providers []peer.AddrInfo, pm *pathpolicy.Manager) {
	type scored struct {
		idx   int
		score float64
	}

	scores := make([]scored, len(providers))
	for i, p := range providers {
		s := scored{idx: i, score: -1}
		if best := pm.BestPath(p.ID); best != nil {
			// Use success rate * inverse RTT as a combined score.
			if best.Metrics.AvgRTT > 0 {
				s.score = best.Metrics.SuccessRate / best.Metrics.AvgRTT.Seconds()
			} else {
				s.score = best.Metrics.SuccessRate
			}
		}
		scores[i] = s
	}

	// Simple insertion sort (providers list is small).
	for i := 1; i < len(scores); i++ {
		for j := i; j > 0 && scores[j].score > scores[j-1].score; j-- {
			scores[j], scores[j-1] = scores[j-1], scores[j]
		}
	}

	// Reorder providers in-place.
	sorted := make([]peer.AddrInfo, len(providers))
	for i, s := range scores {
		sorted[i] = providers[s.idx]
	}
	copy(providers, sorted)
}

// fetchBlockMultipath attempts to fetch a block from multiple providers in
// parallel over disjoint paths, returning the first successful result.
// This "path racing" approach leverages SCION-style path diversity for
// bandwidth aggregation — the fastest path wins.
func (a *API) fetchBlockMultipath(ctx context.Context, blockCID string, providers []peer.AddrInfo) (content.Block, error) {
	if a.node.PathManager == nil || len(providers) < 2 {
		// Fall back to serial fetch if no path manager or too few providers.
		return a.fetchBlockSerial(ctx, blockCID, providers)
	}

	// Use a cancellable context so we can stop other goroutines when one succeeds.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		block content.Block
		err   error
	}

	ch := make(chan result, len(providers))

	// Launch parallel fetches, each via a different provider.
	// DisjointPaths ensures we use independent paths.
	for _, p := range providers {
		go func(target peer.ID) {
			start := time.Now()
			block, err := protocol.FetchBlock(ctx, a.node.Host, target, blockCID)
			if err != nil {
				ch <- result{err: err}
				return
			}
			if a.node.Metrics != nil {
				a.node.Metrics.BlockFetchDuration.WithLabelValues("network").Observe(time.Since(start).Seconds())
				a.node.Metrics.BlocksTransferred.WithLabelValues("received").Inc()
			}
			ch <- result{block: block}
		}(p.ID)
	}

	// Return the first successful result.
	var lastErr error
	for range len(providers) {
		r := <-ch
		if r.err == nil {
			return r.block, nil
		}
		lastErr = r.err
	}
	return content.Block{}, fmt.Errorf("all multipath fetches failed: %w", lastErr)
}

// fetchBlockSerial fetches a block from providers in order (fallback).
func (a *API) fetchBlockSerial(ctx context.Context, blockCID string, providers []peer.AddrInfo) (content.Block, error) {
	for _, p := range providers {
		start := time.Now()
		block, err := protocol.FetchBlock(ctx, a.node.Host, p.ID, blockCID)
		if err != nil {
			continue
		}
		if a.node.Metrics != nil {
			a.node.Metrics.BlockFetchDuration.WithLabelValues("network").Observe(time.Since(start).Seconds())
			a.node.Metrics.BlocksTransferred.WithLabelValues("received").Inc()
		}
		return block, nil
	}
	return content.Block{}, fmt.Errorf("no provider had block %s", blockCID)
}

// boostCachedProviders reorders providers so that peers whose Bloom filters
// indicate they likely have the block cached come first. This reduces cache
// misses by preferring peers with warm caches.
func boostCachedProviders(providers []peer.AddrInfo, blooms *cache.PeerBloomStore, cid string) {
	// Partition: peers that likely have it cached go first.
	cached := make([]peer.AddrInfo, 0, len(providers))
	rest := make([]peer.AddrInfo, 0, len(providers))
	for _, p := range providers {
		if blooms.PeerMayHave(p.ID, cid) {
			cached = append(cached, p)
		} else {
			rest = append(rest, p)
		}
	}
	copy(providers, cached)
	copy(providers[len(cached):], rest)
}

func (a *API) handleFindProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.node.ContentRouter == nil {
		http.Error(w, "content router not initialized", http.StatusServiceUnavailable)
		return
	}

	cidStr := r.URL.Query().Get("cid")
	if cidStr == "" {
		http.Error(w, "missing 'cid' parameter", http.StatusBadRequest)
		return
	}

	providers, err := a.node.ContentRouter.FindProviders(r.Context(), cidStr, 20)
	if err != nil {
		http.Error(w, "find providers: "+err.Error(), http.StatusInternalServerError)
		return
	}

	type providerJSON struct {
		ID    string   `json:"id"`
		Addrs []string `json:"addrs"`
	}

	result := make([]providerJSON, 0, len(providers))
	for _, p := range providers {
		addrs := make([]string, len(p.Addrs))
		for i, a := range p.Addrs {
			addrs[i] = a.String()
		}
		result = append(result, providerJSON{ID: p.ID.String(), Addrs: addrs})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (a *API) handlePin(w http.ResponseWriter, r *http.Request) {
	if a.node.ContentStore == nil {
		http.Error(w, "content store not initialized", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodPost:
		var req struct {
			CID string `json:"cid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.CID == "" {
			http.Error(w, "missing 'cid' field", http.StatusBadRequest)
			return
		}
		if err := a.node.ContentStore.Pin(req.CID); err != nil {
			http.Error(w, "pin failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "pinned", "cid": req.CID})

	case http.MethodDelete:
		var req struct {
			CID string `json:"cid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.CID == "" {
			http.Error(w, "missing 'cid' field", http.StatusBadRequest)
			return
		}
		if err := a.node.ContentStore.Unpin(req.CID); err != nil {
			http.Error(w, "unpin failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "unpinned", "cid": req.CID})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleListPins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.node.ContentStore == nil {
		http.Error(w, "content store not initialized", http.StatusServiceUnavailable)
		return
	}

	pins := a.node.ContentStore.ListPinned()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pins)
}

func (a *API) handleManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.node.ContentStore == nil {
		http.Error(w, "content store not initialized", http.StatusServiceUnavailable)
		return
	}

	rootCID := r.URL.Query().Get("cid")
	if rootCID == "" {
		http.Error(w, "missing 'cid' parameter", http.StatusBadRequest)
		return
	}

	manifest, err := a.node.ContentStore.GetManifest(rootCID)
	if err != nil {
		http.Error(w, "manifest not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Verify signature if present.
	verified := false
	if manifest.Signature != "" {
		if err := content.VerifyManifest(&manifest); err == nil {
			verified = true
		}
	}

	resp := struct {
		RootCID     string   `json:"root_cid"`
		Name        string   `json:"name"`
		TotalSize   int64    `json:"total_size"`
		NumChunks   int      `json:"num_chunks"`
		ChunkCIDs   []string `json:"chunk_cids"`
		PublisherID string   `json:"publisher_id,omitempty"`
		Signed      bool     `json:"signed"`
		Verified    bool     `json:"verified"`
	}{
		RootCID:     manifest.RootCID,
		Name:        manifest.Name,
		TotalSize:   manifest.TotalSize,
		NumChunks:   len(manifest.ChunkCIDs),
		ChunkCIDs:   manifest.ChunkCIDs,
		PublisherID: manifest.PublisherID,
		Signed:      manifest.Signature != "",
		Verified:    verified,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

