package node

import (
	"os"
	"path/filepath"
	"time"
)

// Config holds all configuration for a scion-libp2p node.
type Config struct {
	// Network
	ListenAddrs    []string `mapstructure:"listen_addrs"`
	BootstrapPeers []string `mapstructure:"bootstrap_peers"`

	// Storage
	DataDir string `mapstructure:"data_dir"`

	// Features
	EnableRelay bool `mapstructure:"enable_relay"`
	EnableMDNS  bool `mapstructure:"enable_mdns"`

	// API
	APIAddr     string `mapstructure:"api_addr"`
	MetricsAddr string `mapstructure:"metrics_addr"`

	// Path policy
	PathPolicy string `mapstructure:"path_policy"`

	// Cache
	CacheMaxBytes int64 `mapstructure:"cache_max_bytes"`

	// Content
	ChunkSizeBytes int `mapstructure:"chunk_size_bytes"`

	// Path selection
	PathEpsilon float64 `mapstructure:"path_epsilon"` // epsilon-greedy exploration rate (default 0.1)

	// Probing
	ProbeInterval time.Duration `mapstructure:"probe_interval"`
	ProbeTimeout  time.Duration `mapstructure:"probe_timeout"`

	// Logging
	LogLevel string `mapstructure:"log_level"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".scion-libp2p")

	return Config{
		ListenAddrs:    []string{"/ip4/0.0.0.0/tcp/9000"},
		BootstrapPeers: nil,
		DataDir:        dataDir,
		EnableRelay:    true,
		EnableMDNS:     true,
		APIAddr:        "127.0.0.1:9090",
		MetricsAddr:    ":2112",
		PathPolicy:     "balanced",
		CacheMaxBytes:  128 * 1024 * 1024, // 128 MB
		ChunkSizeBytes: 256 * 1024,        // 256 KB
		PathEpsilon:    0.1,
		ProbeInterval:  10 * time.Second,
		ProbeTimeout:   3 * time.Second,
		LogLevel:       "info",
	}
}
