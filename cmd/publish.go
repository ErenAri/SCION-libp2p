package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

var publishCmd = &cobra.Command{
	Use:   "publish <file>",
	Short: "Publish a file to the content network",
	Args:  cobra.ExactArgs(1),
	RunE:  runPublish,
}

var publishAPIAddr string

func init() {
	publishCmd.Flags().StringVar(&publishAPIAddr, "api", "127.0.0.1:9090", "API address of running daemon")
	rootCmd.AddCommand(publishCmd)
}

type publishRequest struct {
	FilePath string `json:"file_path"`
	Name     string `json:"name"`
}

type publishResponse struct {
	RootCID   string `json:"root_cid"`
	Name      string `json:"name"`
	TotalSize int64  `json:"total_size"`
	NumChunks int    `json:"num_chunks"`
}

func runPublish(cmd *cobra.Command, args []string) error {
	filePath := args[0]

	// Verify file exists.
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("file not found: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", filePath)
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	reqBody := publishRequest{
		FilePath: absPath,
		Name:     filepath.Base(filePath),
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("http://%s/api/v1/publish", publishAPIAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
		return fmt.Errorf("publish failed with status %d", resp.StatusCode)
	}

	var pr publishResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	fmt.Printf("Published: %s\n", pr.Name)
	fmt.Printf("Root CID: %s\n", pr.RootCID)
	fmt.Printf("Size:     %d bytes\n", pr.TotalSize)
	fmt.Printf("Chunks:   %d\n", pr.NumChunks)

	return nil
}
