package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var findCmd = &cobra.Command{
	Use:   "find <cid>",
	Short: "Find peers that hold a content CID via DHT",
	Args:  cobra.ExactArgs(1),
	RunE:  runFind,
}

var findAPIAddr string

func init() {
	findCmd.Flags().StringVar(&findAPIAddr, "api", "127.0.0.1:9090", "API address of running daemon")
	rootCmd.AddCommand(findCmd)
}

type findProviderResp struct {
	ID    string   `json:"id"`
	Addrs []string `json:"addrs"`
}

func runFind(cmd *cobra.Command, args []string) error {
	cid := args[0]

	url := fmt.Sprintf("http://%s/api/v1/find?cid=%s", findAPIAddr, cid)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	var providers []findProviderResp
	if err := json.NewDecoder(resp.Body).Decode(&providers); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(providers) == 0 {
		fmt.Println("No providers found for this CID.")
		return nil
	}

	fmt.Printf("Found %d provider(s) for CID %s:\n\n", len(providers), truncateCID(cid))

	for _, p := range providers {
		fmt.Printf("  Peer: %s\n", truncatePeerID(p.ID))
		for _, addr := range p.Addrs {
			fmt.Printf("    %s\n", addr)
		}
		fmt.Println()
	}

	return nil
}

func truncateCID(cid string) string {
	if len(cid) > 16 {
		return cid[:8] + "..." + cid[len(cid)-8:]
	}
	return cid
}
