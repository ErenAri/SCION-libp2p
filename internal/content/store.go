package content

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Store provides on-disk content-addressed block storage.
type Store struct {
	dir     string
	mu      sync.RWMutex
	pins    map[string]bool
	pinFile string
}

// NewStore creates a block store at the given directory.
func NewStore(dataDir string) (*Store, error) {
	dir := filepath.Join(dataDir, "blocks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create blocks dir: %w", err)
	}
	s := &Store{
		dir:     dir,
		pins:    make(map[string]bool),
		pinFile: filepath.Join(dataDir, "pins.json"),
	}
	s.loadPins()
	return s, nil
}

// Put writes a block to disk.
func (s *Store) Put(b Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.blockPath(b.CID)
	if err := os.WriteFile(path, b.Data, 0o600); err != nil {
		return fmt.Errorf("write block %s: %w", b.CID, err)
	}
	slog.Debug("stored block", "cid", b.CID, "size", len(b.Data))
	return nil
}

// Get retrieves a block by CID.
func (s *Store) Get(cid string) (Block, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := s.blockPath(cid)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Block{}, fmt.Errorf("block %s not found", cid)
		}
		return Block{}, fmt.Errorf("read block %s: %w", cid, err)
	}
	return Block{CID: cid, Data: data}, nil
}

// Has returns whether a block exists.
func (s *Store) Has(cid string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err := os.Stat(s.blockPath(cid))
	return err == nil
}

// Delete removes a block.
func (s *Store) Delete(cid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return os.Remove(s.blockPath(cid))
}

// PutManifest stores a manifest on disk.
func (s *Store) PutManifest(m Manifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	path := s.manifestPath(m.RootCID)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// GetManifest retrieves a manifest by root CID.
func (s *Store) GetManifest(rootCID string) (Manifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.manifestPath(rootCID))
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", rootCID, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("unmarshal manifest: %w", err)
	}
	return m, nil
}

// PutFragment stores an erasure-coded fragment on disk.
func (s *Store) PutFragment(parentCID string, index int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fragDir := filepath.Join(s.dir, "fragments", parentCID)
	if err := os.MkdirAll(fragDir, 0o700); err != nil {
		return fmt.Errorf("create fragment dir: %w", err)
	}
	path := filepath.Join(fragDir, fmt.Sprintf("%d.frag", index))
	return os.WriteFile(path, data, 0o600)
}

// GetFragment retrieves an erasure-coded fragment by parent CID and index.
func (s *Store) GetFragment(parentCID string, index int) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.dir, "fragments", parentCID, fmt.Sprintf("%d.frag", index))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fragment %s/%d: %w", parentCID, index, err)
	}
	return data, nil
}

// ListFragments returns available fragment indices for a parent CID.
func (s *Store) ListFragments(parentCID string) []int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	fragDir := filepath.Join(s.dir, "fragments", parentCID)
	entries, err := os.ReadDir(fragDir)
	if err != nil {
		return nil
	}
	var indices []int
	for _, e := range entries {
		var idx int
		if _, err := fmt.Sscanf(e.Name(), "%d.frag", &idx); err == nil {
			indices = append(indices, idx)
		}
	}
	return indices
}

func (s *Store) blockPath(cid string) string {
	return filepath.Join(s.dir, cid+".block")
}

func (s *Store) manifestPath(rootCID string) string {
	return filepath.Join(s.dir, rootCID+".manifest.json")
}

// Pin marks a CID as pinned so it won't be evicted or garbage-collected.
func (s *Store) Pin(cid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pins[cid] = true
	return s.savePins()
}

// Unpin removes the pin from a CID.
func (s *Store) Unpin(cid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pins, cid)
	return s.savePins()
}

// IsPinned returns whether a CID is pinned.
func (s *Store) IsPinned(cid string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pins[cid]
}

// ListPinned returns all pinned CIDs.
func (s *Store) ListPinned() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]string, 0, len(s.pins))
	for cid := range s.pins {
		result = append(result, cid)
	}
	return result
}

func (s *Store) loadPins() {
	data, err := os.ReadFile(s.pinFile)
	if err != nil {
		return // no pins file yet
	}
	var pins []string
	if err := json.Unmarshal(data, &pins); err != nil {
		slog.Warn("failed to parse pins file", "err", err)
		return
	}
	for _, cid := range pins {
		s.pins[cid] = true
	}
}

func (s *Store) savePins() error {
	pins := make([]string, 0, len(s.pins))
	for cid := range s.pins {
		pins = append(pins, cid)
	}
	data, err := json.Marshal(pins)
	if err != nil {
		return fmt.Errorf("marshal pins: %w", err)
	}
	return os.WriteFile(s.pinFile, data, 0o600)
}
