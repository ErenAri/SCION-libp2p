package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var pinCmd = &cobra.Command{
	Use:   "pin <cid>",
	Short: "Pin a CID to prevent eviction",
	Args:  cobra.ExactArgs(1),
	RunE:  runPin,
}

var unpinCmd = &cobra.Command{
	Use:   "unpin <cid>",
	Short: "Unpin a CID to allow eviction",
	Args:  cobra.ExactArgs(1),
	RunE:  runUnpin,
}

var pinsCmd = &cobra.Command{
	Use:   "pins",
	Short: "List all pinned CIDs",
	RunE:  runListPins,
}

var (
	pinAPIAddr   string
	unpinAPIAddr string
	pinsAPIAddr  string
)

func init() {
	pinCmd.Flags().StringVar(&pinAPIAddr, "api", "127.0.0.1:9090", "API address of running daemon")
	unpinCmd.Flags().StringVar(&unpinAPIAddr, "api", "127.0.0.1:9090", "API address of running daemon")
	pinsCmd.Flags().StringVar(&pinsAPIAddr, "api", "127.0.0.1:9090", "API address of running daemon")

	rootCmd.AddCommand(pinCmd)
	rootCmd.AddCommand(unpinCmd)
	rootCmd.AddCommand(pinsCmd)
}

func runPin(cmd *cobra.Command, args []string) error {
	cid := args[0]
	body, _ := json.Marshal(map[string]string{"cid": cid})

	url := fmt.Sprintf("http://%s/api/v1/pin", pinAPIAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed (is the daemon running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pin failed with status %d", resp.StatusCode)
	}

	fmt.Printf("Pinned: %s\n", cid)
	return nil
}

func runUnpin(cmd *cobra.Command, args []string) error {
	cid := args[0]
	body, _ := json.Marshal(map[string]string{"cid": cid})

	url := fmt.Sprintf("http://%s/api/v1/pin", unpinAPIAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed (is the daemon running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unpin failed with status %d", resp.StatusCode)
	}

	fmt.Printf("Unpinned: %s\n", cid)
	return nil
}

func runListPins(cmd *cobra.Command, args []string) error {
	url := fmt.Sprintf("http://%s/api/v1/pins", pinsAPIAddr)

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

	var pins []string
	if err := json.NewDecoder(resp.Body).Decode(&pins); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(pins) == 0 {
		fmt.Println("No pinned CIDs.")
		return nil
	}

	fmt.Printf("Pinned CIDs: %d\n", len(pins))
	for _, cid := range pins {
		fmt.Printf("  %s\n", cid)
	}
	return nil
}
