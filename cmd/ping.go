package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var pingCmd = &cobra.Command{
	Use:   "ping <peer-id>",
	Short: "Ping a peer and show RTT",
	Args:  cobra.ExactArgs(1),
	RunE:  runPing,
}

var (
	pingCount   int
	pingAPIAddr string
)

func init() {
	pingCmd.Flags().IntVarP(&pingCount, "count", "c", 5, "number of pings")
	pingCmd.Flags().StringVar(&pingAPIAddr, "api", "127.0.0.1:9090", "API address of running daemon")

	rootCmd.AddCommand(pingCmd)
}

type pingResponse struct {
	Results []pingResultJSON `json:"results"`
}

type pingResultJSON struct {
	RTT   string `json:"rtt"`
	Error string `json:"error,omitempty"`
}

func runPing(cmd *cobra.Command, args []string) error {
	peerID := args[0]

	url := fmt.Sprintf("http://%s/api/v1/ping?peer=%s&count=%d", pingAPIAddr, peerID, pingCount)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(pingCount)*5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ping request failed (is the daemon running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping failed with status %d", resp.StatusCode)
	}

	var pr pingResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	fmt.Printf("PING %s (%d pings)\n", peerID, pingCount)
	successCount := 0
	for i, r := range pr.Results {
		if r.Error != "" {
			fmt.Printf("  ping %d: error: %s\n", i+1, r.Error)
		} else {
			fmt.Printf("  ping %d: rtt=%s\n", i+1, r.RTT)
			successCount++
		}
	}
	fmt.Printf("--- %d/%d successful ---\n", successCount, len(pr.Results))

	return nil
}
