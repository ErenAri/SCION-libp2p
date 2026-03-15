package erasure

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestEncodeDecodeRoundtrip(t *testing.T) {
	data := make([]byte, 1024)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}

	fragments, err := Encode(data, "test-parent", 4, 2)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if len(fragments) != 6 {
		t.Fatalf("expected 6 fragments, got %d", len(fragments))
	}

	// Verify fragment metadata.
	for i, f := range fragments {
		if f.ParentCID != "test-parent" {
			t.Errorf("fragment %d: expected parent CID 'test-parent', got %q", i, f.ParentCID)
		}
		if f.Index != i {
			t.Errorf("fragment %d: expected index %d, got %d", i, i, f.Index)
		}
		if f.Total != 6 {
			t.Errorf("fragment %d: expected total 6, got %d", i, f.Total)
		}
		if f.DataCount != 4 {
			t.Errorf("fragment %d: expected data count 4, got %d", i, f.DataCount)
		}
		if f.IsParity != (i >= 4) {
			t.Errorf("fragment %d: unexpected IsParity=%v", i, f.IsParity)
		}
		if f.CID == "" {
			t.Errorf("fragment %d: empty CID", i)
		}
	}

	// Reconstruct with all fragments.
	result, err := Decode(fragments, len(data), 4, 2)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !bytes.Equal(result, data) {
		t.Error("reconstructed data does not match original")
	}
}

func TestDecodeWithMissingShards(t *testing.T) {
	data := make([]byte, 2048)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}

	fragments, err := Encode(data, "test", 4, 2)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Drop 2 fragments (the maximum we can lose with 2 parity shards).
	partial := []Fragment{fragments[0], fragments[1], fragments[3], fragments[5]}

	result, err := Decode(partial, len(data), 4, 2)
	if err != nil {
		t.Fatalf("decode with missing shards: %v", err)
	}

	if !bytes.Equal(result, data) {
		t.Error("reconstructed data does not match original after losing 2 shards")
	}
}

func TestDecodeInsufficientFragments(t *testing.T) {
	data := make([]byte, 512)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}

	fragments, err := Encode(data, "test", 4, 2)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Only 3 fragments — not enough (need 4).
	partial := fragments[:3]

	_, err = Decode(partial, len(data), 4, 2)
	if err == nil {
		t.Error("expected error when decoding with insufficient fragments")
	}
}

func TestCanReconstruct(t *testing.T) {
	frags := make([]Fragment, 4)
	if !CanReconstruct(frags, 4) {
		t.Error("expected CanReconstruct=true with 4 of 4")
	}
	if CanReconstruct(frags[:3], 4) {
		t.Error("expected CanReconstruct=false with 3 of 4")
	}
}

func TestStorageOverhead(t *testing.T) {
	overhead := StorageOverhead(4, 2)
	if overhead != 1.5 {
		t.Errorf("expected 1.5x overhead for (4,2), got %f", overhead)
	}
}

func TestSmallData(t *testing.T) {
	// Test with data smaller than number of shards.
	data := []byte("hello")

	fragments, err := Encode(data, "small", 4, 2)
	if err != nil {
		t.Fatalf("encode small data: %v", err)
	}

	result, err := Decode(fragments, len(data), 4, 2)
	if err != nil {
		t.Fatalf("decode small data: %v", err)
	}

	if !bytes.Equal(result, data) {
		t.Errorf("expected %q, got %q", data, result)
	}
}
