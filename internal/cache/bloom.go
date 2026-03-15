package cache

import (
	"encoding/binary"
	"hash"
	"hash/fnv"
	"math"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
)

// BloomFilter is a space-efficient probabilistic data structure for testing
// set membership. Used for exchanging cache summaries between peers.
type BloomFilter struct {
	bits    []byte
	size    uint // number of bits
	numHash uint // number of hash functions
}

// NewBloomFilter creates a Bloom filter sized for expectedItems with the
// given false positive rate. Typical usage: NewBloomFilter(1000, 0.01).
func NewBloomFilter(expectedItems int, fpRate float64) *BloomFilter {
	if expectedItems <= 0 {
		expectedItems = 100
	}
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = 0.01
	}

	// Optimal size: m = -n*ln(p) / (ln(2)^2)
	n := float64(expectedItems)
	m := -n * math.Log(fpRate) / (math.Ln2 * math.Ln2)
	size := uint(math.Ceil(m))
	if size < 8 {
		size = 8
	}

	// Optimal hash count: k = (m/n) * ln(2)
	k := uint(math.Ceil(float64(size) / n * math.Ln2))
	if k < 1 {
		k = 1
	}

	return &BloomFilter{
		bits:    make([]byte, (size+7)/8),
		size:    size,
		numHash: k,
	}
}

// Add inserts a key into the Bloom filter.
func (bf *BloomFilter) Add(key string) {
	for _, idx := range bf.hashes(key) {
		bf.bits[idx/8] |= 1 << (idx % 8)
	}
}

// Contains tests whether a key might be in the set.
// False positives are possible; false negatives are not.
func (bf *BloomFilter) Contains(key string) bool {
	for _, idx := range bf.hashes(key) {
		if bf.bits[idx/8]&(1<<(idx%8)) == 0 {
			return false
		}
	}
	return true
}

// Bytes serializes the Bloom filter for network transmission.
// Format: [4 bytes size][4 bytes numHash][bits...]
func (bf *BloomFilter) Bytes() []byte {
	data := make([]byte, 8+len(bf.bits))
	binary.LittleEndian.PutUint32(data[0:4], uint32(bf.size))
	binary.LittleEndian.PutUint32(data[4:8], uint32(bf.numHash))
	copy(data[8:], bf.bits)
	return data
}

// BloomFromBytes deserializes a Bloom filter from wire format.
func BloomFromBytes(data []byte) *BloomFilter {
	if len(data) < 8 {
		return nil
	}
	size := uint(binary.LittleEndian.Uint32(data[0:4]))
	numHash := uint(binary.LittleEndian.Uint32(data[4:8]))
	bits := make([]byte, len(data)-8)
	copy(bits, data[8:])
	return &BloomFilter{bits: bits, size: size, numHash: numHash}
}

func (bf *BloomFilter) hashes(key string) []uint {
	h := fnv.New64a()
	indices := make([]uint, bf.numHash)
	for i := uint(0); i < bf.numHash; i++ {
		h.Reset()
		writeUint(h, uint64(i))
		h.Write([]byte(key))
		indices[i] = uint(h.Sum64()) % bf.size
	}
	return indices
}

func writeUint(h hash.Hash64, v uint64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	h.Write(buf[:])
}

// PeerBloomStore tracks per-peer cache Bloom filters received from the network.
type PeerBloomStore struct {
	mu      sync.RWMutex
	filters map[peer.ID]*BloomFilter
}

// NewPeerBloomStore creates a new store for peer cache summaries.
func NewPeerBloomStore() *PeerBloomStore {
	return &PeerBloomStore{
		filters: make(map[peer.ID]*BloomFilter),
	}
}

// Set stores a Bloom filter for a peer.
func (s *PeerBloomStore) Set(p peer.ID, bf *BloomFilter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filters[p] = bf
}

// PeerMayHave returns true if the peer's Bloom filter suggests it has the CID cached.
// Returns false if the peer has no Bloom filter (conservative: don't assume cache hit).
func (s *PeerBloomStore) PeerMayHave(p peer.ID, cid string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bf, ok := s.filters[p]
	if !ok {
		return false
	}
	return bf.Contains(cid)
}

// Remove deletes a peer's Bloom filter (e.g., on disconnect).
func (s *PeerBloomStore) Remove(p peer.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.filters, p)
}
