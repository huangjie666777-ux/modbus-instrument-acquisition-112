package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
)

type Device struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	UnitID    uint8  `json:"unit_id"`
	TimeoutMs int    `json:"timeout_ms"`
}

type PointType string

const (
	TypeU16 PointType = "u16"
	TypeI16 PointType = "i16"
	TypeF32 PointType = "f32"
)

type WordOrder string

const (
	WordHighFirst WordOrder = "high_first"
	WordLowFirst  WordOrder = "low_first"
)

type Point struct {
	DeviceID  string    `json:"device_id"`
	Name      string    `json:"name"`
	FuncCode  uint8     `json:"func_code"`
	Address   uint16    `json:"address"`
	Type      PointType `json:"type"`
	WordOrder WordOrder `json:"word_order,omitempty"`
	Scale     float64   `json:"scale"`
	Offset    float64   `json:"offset"`
}

// RegCount returns how many 16-bit registers the point occupies.
func (p Point) RegCount() uint16 {
	if p.Type == TypeF32 {
		return 2
	}
	return 1
}

type Config struct {
	Version uint64   `json:"version"`
	Devices []Device `json:"devices"`
	Points  []Point  `json:"points"`
}

func (c *Config) Validate() error {
	if len(c.Devices) == 0 {
		return errors.New("at least one device is required")
	}
	devIDs := map[string]bool{}
	for _, d := range c.Devices {
		if d.ID == "" {
			return errors.New("device id must not be empty")
		}
		if devIDs[d.ID] {
			return fmt.Errorf("duplicate device id %q", d.ID)
		}
		devIDs[d.ID] = true
		if d.Address == "" {
			return fmt.Errorf("device %q: address must not be empty", d.ID)
		}
		if d.TimeoutMs <= 0 || d.TimeoutMs > 600000 {
			return fmt.Errorf("device %q: timeout_ms must be in (0, 600000]", d.ID)
		}
	}
	names := map[string]bool{}
	for _, p := range c.Points {
		if !devIDs[p.DeviceID] {
			return fmt.Errorf("point %q: unknown device_id %q", p.Name, p.DeviceID)
		}
		if p.Name == "" {
			return errors.New("point name must not be empty")
		}
		if names[p.Name] {
			return fmt.Errorf("duplicate point name %q", p.Name)
		}
		names[p.Name] = true
		if p.FuncCode != 3 && p.FuncCode != 4 {
			return fmt.Errorf("point %q: func_code must be 3 or 4", p.Name)
		}
		switch p.Type {
		case TypeU16, TypeI16, TypeF32:
		default:
			return fmt.Errorf("point %q: type must be u16, i16 or f32", p.Name)
		}
		if p.Type == TypeF32 {
			if p.WordOrder != WordHighFirst && p.WordOrder != WordLowFirst {
				return fmt.Errorf("point %q: word_order must be high_first or low_first", p.Name)
			}
		}
		end := uint32(p.Address) + uint32(p.RegCount()) - 1
		if end > 65535 {
			return fmt.Errorf("point %q: register range %d..%d exceeds 65535", p.Name, p.Address, end)
		}
		if math.IsNaN(p.Scale) || math.IsInf(p.Scale, 0) || p.Scale == 0 {
			return fmt.Errorf("point %q: scale must be finite and non-zero", p.Name)
		}
		if math.IsNaN(p.Offset) || math.IsInf(p.Offset, 0) {
			return fmt.Errorf("point %q: offset must be finite", p.Name)
		}
	}
	return nil
}

func (c *Config) DeviceByID(id string) (Device, bool) {
	for _, d := range c.Devices {
		if d.ID == id {
			return d, true
		}
	}
	return Device{}, false
}

func (c *Config) PointByName(name string) (Point, bool) {
	for _, p := range c.Points {
		if p.Name == name {
			return p, true
		}
	}
	return Point{}, false
}

// Store keeps the current config and persists updates atomically.
type Store struct {
	mu   sync.Mutex
	path string
	cfg  Config
}

func LoadStore(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.cfg = Config{Version: 0}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("corrupt config file: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid persisted config: %w", err)
	}
	s.cfg = cfg
	return s, nil
}

// Snapshot returns the current config; safe for concurrent use.
func (s *Store) Snapshot() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// Replace validates, persists and swaps the config atomically.
// Concurrent updates are serialized; on failure the old version stays.
func (s *Store) Replace(next Config) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next.Version = s.cfg.Version + 1
	if err := next.Validate(); err != nil {
		return Config{}, err
	}
	if err := writeFileAtomic(s.path, next); err != nil {
		return Config{}, fmt.Errorf("persist config: %w", err)
	}
	s.cfg = next
	return next, nil
}

func writeFileAtomic(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
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
	return os.Rename(tmpName, path)
}
