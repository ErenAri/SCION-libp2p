package cmd

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	cfgFile  string
	logLevel string
)

var rootCmd = &cobra.Command{
	Use:   "scion-libp2p",
	Short: "SCION-inspired path-aware P2P content overlay",
	Long: `A path-aware, secure P2P content overlay built on libp2p.
Uses SCION-inspired relay path selection to let end-hosts choose
among multiple relay paths based on latency, reliability, and hop count.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		setupLogging(logLevel)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path (JSON format)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// LoadConfigFile reads a JSON config file and returns values as a map.
// Returns nil if no config file is specified or file doesn't exist.
func LoadConfigFile() map[string]interface{} {
	path := cfgFile
	if path == "" {
		// Try default locations.
		home, err := os.UserHomeDir()
		if err == nil {
			defaultPath := filepath.Join(home, ".scion-libp2p", "config.json")
			if _, err := os.Stat(defaultPath); err == nil {
				path = defaultPath
			}
		}
	}

	if path == "" {
		// Also check current directory.
		if _, err := os.Stat("scion-libp2p.json"); err == nil {
			path = "scion-libp2p.json"
		}
	}

	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("could not read config file", "path", path, "err", err)
		return nil
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		slog.Warn("could not parse config file", "path", path, "err", err)
		return nil
	}

	slog.Info("loaded config file", "path", path)
	return cfg
}

func setupLogging(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(handler))
}


