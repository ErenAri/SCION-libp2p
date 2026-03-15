package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var pathsCmd = &cobra.Command{
	Use:   "paths [--peer <peer-id>]",
	Short: "Show known paths to peers with quality metrics",
	RunE:  runPaths,
}

var (
	pathsPeer    string
	pathsAPIAddr string
)

func init() {
	pathsCmd.Flags().StringVar(&pathsPeer, "peer", "", "filter paths by target peer ID")
	pathsCmd.Flags().StringVar(&pathsAPIAddr, "api", "127.0.0.1:9090", "API address of running daemon")

	rootCmd.AddCommand(pathsCmd)
}

type pathsResponse struct {
	Policy string         `json:"policy"`
	Paths  []pathInfoResp `json:"paths"`
}

type pathInfoResp struct {
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

func runPaths(cmd *cobra.Command, args []string) error {
	url := fmt.Sprintf("http://%s/api/v1/paths", pathsAPIAddr)
	if pathsPeer != "" {
		url += "?peer=" + pathsPeer
	}

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

	var pr pathsResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	fmt.Printf("Policy: %s\n", pr.Policy)

	if len(pr.Paths) == 0 {
		fmt.Println("No paths discovered yet.")
		return nil
	}

	fmt.Printf("Paths: %d\n\n", len(pr.Paths))

	for _, p := range pr.Paths {
		fmt.Printf("  %-12s %s\n", "Path ID:", p.ID)
		fmt.Printf("  %-12s %s\n", "Target:", truncatePeerID(p.Target))
		fmt.Printf("  %-12s %s\n", "Type:", p.Type)
		if len(p.RelayChain) > 0 {
			fmt.Printf("  %-12s", "Relays:")
			for _, r := range p.RelayChain {
				fmt.Printf(" %s", truncatePeerID(r))
			}
			fmt.Println()
		}
		fmt.Printf("  %-12s %s\n", "Avg RTT:", p.AvgRTT)
		fmt.Printf("  %-12s %d\n", "Hops:", p.HopCount)
		fmt.Printf("  %-12s %.1f%%\n", "Success:", p.SuccessRate*100)
		fmt.Printf("  %-12s %d\n", "Samples:", p.SampleCount)
		fmt.Println()
	}

	return nil
}

func truncatePeerID(id string) string {
	if len(id) > 16 {
		return id[:8] + "..." + id[len(id)-8:]
	}
	return id
}
