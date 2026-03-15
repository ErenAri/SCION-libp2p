package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/erena/scion-libp2p/internal/node"
	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start a long-running scion-libp2p node",
	Long:  `Starts the node daemon which participates in the P2P network, serves content, and handles relay connections.`,
	RunE:  runDaemon,
}

var (
	listenAddrs    []string
	bootstrapPeers []string
	dataDir        string
	enableRelay    bool
	enableMDNS     bool
	apiAddr        string
	metricsAddr    string
	pathPolicy     string
	pathEpsilon    float64
)

func init() {
	daemonCmd.Flags().StringSliceVar(&listenAddrs, "listen", []string{"/ip4/127.0.0.1/tcp/9000"}, "listen multiaddrs")
	daemonCmd.Flags().StringSliceVar(&bootstrapPeers, "bootstrap", nil, "bootstrap peer multiaddrs")
	daemonCmd.Flags().StringVar(&dataDir, "data-dir", "", "data directory (default: ~/.scion-libp2p)")
	daemonCmd.Flags().BoolVar(&enableRelay, "enable-relay", true, "act as relay server")
	daemonCmd.Flags().BoolVar(&enableMDNS, "enable-mdns", true, "enable mDNS discovery")
	daemonCmd.Flags().StringVar(&apiAddr, "api-addr", "127.0.0.1:9090", "HTTP API address")
	daemonCmd.Flags().StringVar(&metricsAddr, "metrics-addr", ":2112", "Prometheus metrics address")
	daemonCmd.Flags().StringVar(&pathPolicy, "policy", "balanced", "path selection policy (latency, hop-count, reliability, balanced, epsilon-greedy, random)")
	daemonCmd.Flags().Float64Var(&pathEpsilon, "epsilon", 0.1, "epsilon-greedy exploration rate (0.0-1.0)")

	rootCmd.AddCommand(daemonCmd)
}

func runDaemon(cmd *cobra.Command, args []string) error {
	cfg := node.DefaultConfig()
	cfg.ListenAddrs = listenAddrs
	cfg.BootstrapPeers = bootstrapPeers
	cfg.EnableRelay = enableRelay
	cfg.EnableMDNS = enableMDNS
	cfg.APIAddr = apiAddr
	cfg.MetricsAddr = metricsAddr
	cfg.PathPolicy = pathPolicy
	cfg.PathEpsilon = pathEpsilon

	if dataDir != "" {
		cfg.DataDir = dataDir
	}

	n, err := node.New(cfg)
	if err != nil {
		return fmt.Errorf("create node: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := n.Start(ctx); err != nil {
		return fmt.Errorf("start node: %w", err)
	}

	// Start HTTP API for CLI commands.
	api := node.NewAPI(n)
	go func() {
		slog.Info("starting API server", "addr", cfg.APIAddr)
		if err := api.ListenAndServe(cfg.APIAddr); err != nil {
			slog.Error("API server error", "err", err)
		}
	}()

	// Print addresses for user.
	fmt.Fprintf(os.Stderr, "Peer ID: %s\n", n.PeerID())
	fmt.Fprintf(os.Stderr, "API:     http://%s\n", cfg.APIAddr)
	for _, addr := range n.Host.Addrs() {
		fmt.Fprintf(os.Stderr, "Listen:  %s/p2p/%s\n", addr, n.PeerID())
	}

	// Wait for interrupt.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	slog.Info("received signal, shutting down", "signal", sig)

	return n.Stop()
}
