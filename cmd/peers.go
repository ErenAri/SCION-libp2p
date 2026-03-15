package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/erena/scion-libp2p/internal/node"
	"github.com/spf13/cobra"
)

var peersCmd = &cobra.Command{
	Use:   "peers",
	Short: "List connected peers",
	RunE:  runPeers,
}

var (
	peersVerbose bool
	peersAPIAddr string
)

func init() {
	peersCmd.Flags().BoolVarP(&peersVerbose, "verbose", "v", false, "show multiaddrs and protocols")
	peersCmd.Flags().StringVar(&peersAPIAddr, "api", "127.0.0.1:9090", "API address of running daemon")

	rootCmd.AddCommand(peersCmd)
}

func runPeers(cmd *cobra.Command, args []string) error {
	url := fmt.Sprintf("http://%s/api/v1/peers", peersAPIAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed (is the daemon running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request failed with status %d", resp.StatusCode)
	}

	var peers []node.PeerInfo
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(peers) == 0 {
		fmt.Println("No connected peers.")
		return nil
	}

	fmt.Printf("Connected peers: %d\n", len(peers))
	for _, p := range peers {
		fmt.Printf("  %s\n", p.ID)
		if peersVerbose {
			for _, addr := range p.Addrs {
				fmt.Printf("    addr: %s\n", addr)
			}
			for _, proto := range p.Protocols {
				fmt.Printf("    proto: %s\n", proto)
			}
		}
	}

	return nil
}
