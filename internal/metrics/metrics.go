package metrics

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus metrics for a pathaware-libp2p node.
// Each node gets its own registry to avoid panics in multi-node scenarios.
type Metrics struct {
	Registry            *prometheus.Registry
	PeersConnected      prometheus.Gauge
	RelayPeersAvailable prometheus.Gauge
	ProbeRTT            *prometheus.HistogramVec
	ProbeFailures       prometheus.Counter
	PingRTT             *prometheus.HistogramVec
	CacheHits           prometheus.Counter
	CacheMisses         prometheus.Counter
	CacheBytes          prometheus.Gauge

	// Content delivery metrics.
	BlockFetchDuration  *prometheus.HistogramVec // by source: "local", "cache", "network"
	BlocksTransferred   *prometheus.CounterVec   // by direction: "sent", "received"
	ContentRetrievals   prometheus.Counter
	PathSelectionsTotal *prometheus.CounterVec // by path_type: "direct", "relay"
	StalePaths          prometheus.Counter
	BlocksReplicated    prometheus.Counter

	// Erasure coding metrics.
	ErasureEncodeSeconds prometheus.Histogram
	ErasureDecodeSeconds prometheus.Histogram
	FragmentsStored      prometheus.Counter
}

// New creates and registers all Prometheus metrics in a fresh per-node registry.
func New() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		Registry: reg,
		PeersConnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "scion_libp2p",
			Name:      "peers_connected",
			Help:      "Number of currently connected peers.",
		}),
		RelayPeersAvailable: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "scion_libp2p",
			Name:      "relay_peers_available",
			Help:      "Number of relay-capable peers.",
		}),
		ProbeRTT: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "scion_libp2p",
			Name:      "probe_rtt_seconds",
			Help:      "Round-trip time for path probes.",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2, 15),
		}, []string{"path_type"}),
		ProbeFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "scion_libp2p",
			Name:      "probe_failures_total",
			Help:      "Total number of failed probes.",
		}),
		PingRTT: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "scion_libp2p",
			Name:      "ping_rtt_seconds",
			Help:      "Round-trip time for ping requests.",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2, 15),
		}, []string{"target_peer"}),
		CacheHits: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "scion_libp2p",
			Name:      "cache_hits_total",
			Help:      "Total number of block cache hits.",
		}),
		CacheMisses: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "scion_libp2p",
			Name:      "cache_misses_total",
			Help:      "Total number of block cache misses.",
		}),
		CacheBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "scion_libp2p",
			Name:      "cache_bytes",
			Help:      "Current size of the block cache in bytes.",
		}),
		BlockFetchDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "scion_libp2p",
			Name:      "block_fetch_duration_seconds",
			Help:      "Time to fetch a content block by source.",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2, 12),
		}, []string{"source"}),
		BlocksTransferred: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "scion_libp2p",
			Name:      "blocks_transferred_total",
			Help:      "Total blocks transferred by direction.",
		}, []string{"direction"}),
		ContentRetrievals: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "scion_libp2p",
			Name:      "content_retrievals_total",
			Help:      "Total content retrieval operations.",
		}),
		PathSelectionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "scion_libp2p",
			Name:      "path_selections_total",
			Help:      "Path selections by type.",
		}, []string{"path_type"}),
		StalePaths: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "scion_libp2p",
			Name:      "stale_paths_pruned_total",
			Help:      "Total paths pruned due to staleness.",
		}),
		BlocksReplicated: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "scion_libp2p",
			Name:      "blocks_replicated_total",
			Help:      "Total blocks replicated to peers.",
		}),
		ErasureEncodeSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "scion_libp2p",
			Name:      "erasure_encode_seconds",
			Help:      "Time to erasure-encode a block into fragments.",
			Buckets:   prometheus.ExponentialBuckets(0.0001, 2, 10),
		}),
		ErasureDecodeSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "scion_libp2p",
			Name:      "erasure_decode_seconds",
			Help:      "Time to reconstruct a block from erasure-coded fragments.",
			Buckets:   prometheus.ExponentialBuckets(0.0001, 2, 10),
		}),
		FragmentsStored: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "scion_libp2p",
			Name:      "fragments_stored_total",
			Help:      "Total erasure-coded fragments stored.",
		}),
	}

	reg.MustRegister(
		m.PeersConnected,
		m.RelayPeersAvailable,
		m.ProbeRTT,
		m.ProbeFailures,
		m.PingRTT,
		m.CacheHits,
		m.CacheMisses,
		m.CacheBytes,
		m.BlockFetchDuration,
		m.BlocksTransferred,
		m.ContentRetrievals,
		m.PathSelectionsTotal,
		m.StalePaths,
		m.BlocksReplicated,
		m.ErasureEncodeSeconds,
		m.ErasureDecodeSeconds,
		m.FragmentsStored,
	)

	return m
}

// StartMetricsServer starts an HTTP server serving this node's Prometheus metrics.
// If addr is empty, the server is not started.
func StartMetricsServer(addr string, registry *prometheus.Registry) {
	if addr == "" {
		return
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	slog.Info("starting metrics server", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("metrics server error", "err", err)
	}
}

