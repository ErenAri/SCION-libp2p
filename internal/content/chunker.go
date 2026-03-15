package content

import (
	"crypto/sha256"
	"fmt"
	"io"
)

// Block is a content-addressed chunk of data.
type Block struct {
	CID  string // hex-encoded SHA-256 hash
	Data []byte
}

// ComputeCID computes the content ID (SHA-256 hex) for data.
func ComputeCID(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// Chunk splits data from a reader into fixed-size blocks and returns them.
func Chunk(r io.Reader, chunkSize int) ([]Block, error) {
	if chunkSize <= 0 {
		return nil, fmt.Errorf("chunk size must be positive, got %d", chunkSize)
	}

	var blocks []Block
	buf := make([]byte, chunkSize)

	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			blocks = append(blocks, Block{
				CID:  ComputeCID(data),
				Data: data,
			})
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read chunk: %w", err)
		}
	}

	return blocks, nil
}

// Manifest describes a published content item.
type Manifest struct {
	RootCID     string   `json:"root_cid"`
	Name        string   `json:"name"`
	TotalSize   int64    `json:"total_size"`
	ChunkCIDs   []string `json:"chunk_cids"`
	PublisherID string   `json:"publisher_id,omitempty"` // hex Ed25519 public key
	Signature   string   `json:"signature,omitempty"`    // hex Ed25519 signature
}

// AdaptiveChunkSize computes an optimal chunk size based on file size and
// estimated path RTT. Small files use a single chunk; large files on
// high-latency paths use larger chunks (fewer round trips), while large
// files on low-latency paths use smaller chunks (more parallelism).
func AdaptiveChunkSize(fileSize int64, pathRTTms float64, minChunk, maxChunk int) int {
	if minChunk <= 0 {
		minChunk = 64 * 1024 // 64 KB
	}
	if maxChunk <= 0 {
		maxChunk = 1024 * 1024 // 1 MB
	}

	// Small files: single chunk.
	if fileSize <= int64(minChunk) {
		return int(fileSize)
	}

	// Target 4-16 chunks depending on RTT.
	// High RTT → larger chunks (fewer round trips).
	// Low RTT → smaller chunks (more parallelism).
	targetChunks := 8 // default
	if pathRTTms > 50 {
		targetChunks = 4 // high latency: fewer, larger chunks
	} else if pathRTTms < 5 {
		targetChunks = 16 // low latency: more, smaller chunks
	}

	chunkSize := int(fileSize) / targetChunks
	if chunkSize < minChunk {
		chunkSize = minChunk
	}
	if chunkSize > maxChunk {
		chunkSize = maxChunk
	}

	return chunkSize
}

// BuildManifest creates a manifest from a list of blocks and metadata.
func BuildManifest(name string, totalSize int64, blocks []Block) Manifest {
	cids := make([]string, len(blocks))
	for i, b := range blocks {
		cids[i] = b.CID
	}

	// Root CID = hash of all chunk CIDs concatenated.
	h := sha256.New()
	for _, c := range cids {
		h.Write([]byte(c))
	}
	rootCID := fmt.Sprintf("%x", h.Sum(nil))

	return Manifest{
		RootCID:   rootCID,
		Name:      name,
		TotalSize: totalSize,
		ChunkCIDs: cids,
	}
}
