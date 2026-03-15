package erasure

import (
	"crypto/sha256"
	"fmt"

	"github.com/klauspost/reedsolomon"
)

// Fragment is a single erasure-coded shard of a content block.
// Any DataCount fragments out of Total suffice to reconstruct the original data.
type Fragment struct {
	CID       string `json:"cid"`        // SHA-256 hash of fragment data
	ParentCID string `json:"parent_cid"` // CID of the original block
	Index     int    `json:"index"`      // shard index (0..Total-1)
	Total     int    `json:"total"`      // total shards (data + parity)
	DataCount int    `json:"data_count"` // number of data shards needed for reconstruction
	IsParity  bool   `json:"is_parity"`
	Data      []byte `json:"data"`
}

// Encode splits data into dataShards data fragments + parityShards parity fragments
// using Reed-Solomon erasure coding. Any dataShards fragments suffice to reconstruct.
func Encode(data []byte, parentCID string, dataShards, parityShards int) ([]Fragment, error) {
	enc, err := reedsolomon.New(dataShards, parityShards)
	if err != nil {
		return nil, fmt.Errorf("create encoder: %w", err)
	}

	// Split data into shards. reedsolomon.Split pads the last shard if needed.
	shards, err := enc.Split(data)
	if err != nil {
		return nil, fmt.Errorf("split data: %w", err)
	}

	// Compute parity shards.
	if err := enc.Encode(shards); err != nil {
		return nil, fmt.Errorf("encode parity: %w", err)
	}

	total := dataShards + parityShards
	fragments := make([]Fragment, total)
	for i, shard := range shards {
		fragments[i] = Fragment{
			CID:       computeCID(shard),
			ParentCID: parentCID,
			Index:     i,
			Total:     total,
			DataCount: dataShards,
			IsParity:  i >= dataShards,
			Data:      shard,
		}
	}

	return fragments, nil
}

// Decode reconstructs the original data from a set of fragments.
// At least dataShards fragments are required; missing shards are set to nil.
func Decode(fragments []Fragment, originalSize int, dataShards, parityShards int) ([]byte, error) {
	if len(fragments) < dataShards {
		return nil, fmt.Errorf("need at least %d fragments, got %d", dataShards, len(fragments))
	}

	enc, err := reedsolomon.New(dataShards, parityShards)
	if err != nil {
		return nil, fmt.Errorf("create decoder: %w", err)
	}

	total := dataShards + parityShards
	shards := make([][]byte, total)
	for _, f := range fragments {
		if f.Index >= 0 && f.Index < total {
			shards[f.Index] = f.Data
		}
	}

	// Reconstruct missing shards.
	if err := enc.Reconstruct(shards); err != nil {
		return nil, fmt.Errorf("reconstruct: %w", err)
	}

	// Verify reconstruction integrity.
	ok, err := enc.Verify(shards)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("verification failed after reconstruction")
	}

	// Join data shards and trim to original size.
	var result []byte
	for i := 0; i < dataShards; i++ {
		result = append(result, shards[i]...)
	}

	if originalSize > 0 && originalSize < len(result) {
		result = result[:originalSize]
	}

	return result, nil
}

// CanReconstruct returns true if there are enough fragments to reconstruct.
func CanReconstruct(fragments []Fragment, dataShards int) bool {
	return len(fragments) >= dataShards
}

// StorageOverhead returns the storage overhead factor for erasure coding.
// E.g., (4,2) → 1.5x overhead vs 1.0x for no coding.
func StorageOverhead(dataShards, parityShards int) float64 {
	return float64(dataShards+parityShards) / float64(dataShards)
}

func computeCID(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
