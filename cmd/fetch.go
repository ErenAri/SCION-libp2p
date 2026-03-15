package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var fetchCmd = &cobra.Command{
	Use:   "fetch <root-cid>",
	Short: "Fetch content by root CID",
	Args:  cobra.ExactArgs(1),
	RunE:  runFetch,
}

var (
	fetchOutput  string
	fetchAPIAddr string
)

func init() {
	fetchCmd.Flags().StringVarP(&fetchOutput, "output", "o", "", "output file path (default: stdout)")
	fetchCmd.Flags().StringVar(&fetchAPIAddr, "api", "127.0.0.1:9090", "API address of running daemon")
	rootCmd.AddCommand(fetchCmd)
}

func runFetch(cmd *cobra.Command, args []string) error {
	rootCID := args[0]

	url := fmt.Sprintf("http://%s/api/v1/fetch?cid=%s", fetchAPIAddr, rootCID)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fetch failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Determine output writer.
	var out *os.File
	if fetchOutput == "" {
		out = os.Stdout
	} else {
		f, err := os.Create(fetchOutput)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer f.Close()
		out = f
	}

	// Stream response body to output.
	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	if fetchOutput != "" {
		fmt.Fprintf(os.Stderr, "Fetched %d bytes to %s\n", written, fetchOutput)
	}

	return nil
}

// FetchManifest retrieves a manifest by root CID from the daemon.
func fetchManifest(apiAddr, rootCID string) (map[string]interface{}, error) {
	url := fmt.Sprintf("http://%s/api/v1/manifest?cid=%s", apiAddr, rootCID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return result, nil
}
