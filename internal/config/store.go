package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Snapshot is an immutable view of the table used by one acquisition.
type Snapshot struct {
	Version int64
	Config  Config
	devices map[string]Device
	points  map[string]Point
}

func (s *Snapshot) Device(id string) (Device, bool) {
	d, ok := s.devices[id]
	return d, ok
}

func (s *Snapshot) Point(name string) (Point, bool) {
	p, ok := s.points[name]
	return p, ok
}

// persistedRecord is the on-disk JSON layout.
type persistedRecord struct {
	Version int64  `json:"version"`
	Config  Config `json:"config"`
}

// Store persists whole-table configurations atomically. Concurrent updates
// are serialized; a failed update leaves the previous version intact.
type Store struct {
	mu      sync.RWMutex
	path    string
	version int64
	cfg     Config
}

// LoadStore reads the persisted table. A missing file yields an empty store.
func LoadStore(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	var rec persistedRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := rec.Config.Validate(); err != nil {
		return nil, fmt.Errorf("persisted config invalid: %w", err)
	}
	s.version = rec.Version
	s.cfg = rec.Config
	return s, nil
}

// Replace validates and atomically persists a new table, bumping the version.
// On validation or persistence failure the store keeps its old version.
func (s *Store) Replace(cfg Config) (int64, error) {
	if err := cfg.Validate(); err != nil {
		return s.Version(), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.version + 1
	rec := persistedRecord{Version: next, Config: cloneConfig(cfg)}
	if err := persist(s.path, rec); err != nil {
		return s.version, err
	}
	s.cfg = rec.Config
	s.version = next
	return next, nil
}

// Snapshot returns a deep copy stable for the duration of an acquisition.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := cloneConfig(s.cfg)
	snap := Snapshot{Version: s.version, Config: cfg,
		devices: make(map[string]Device, len(cfg.Devices)),
		points:  make(map[string]Point, len(cfg.Points))}
	for _, d := range cfg.Devices {
		snap.devices[d.ID] = d
	}
	for _, p := range cfg.Points {
		snap.points[p.Name] = p
	}
	return snap
}

func (s *Store) Version() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

func cloneConfig(c Config) Config {
	out := Config{Devices: append([]Device(nil), c.Devices...), Points: append([]Point(nil), c.Points...)}
	for i := range out.Points {
		if c.Points[i].Scale != nil {
			v := *c.Points[i].Scale
			out.Points[i].Scale = &v
		}
		if c.Points[i].Offset != nil {
			v := *c.Points[i].Offset
			out.Points[i].Offset = &v
		}
	}
	return out
}

// persist writes via a temp file, fsync and rename so readers always see a
// complete record and crashes leave the previous file in place.
func persist(path string, rec persistedRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		dir.Sync()
		dir.Close()
	}
	return nil
}
