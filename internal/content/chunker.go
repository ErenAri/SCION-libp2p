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
