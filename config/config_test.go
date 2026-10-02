package config

import (
	"math"
	"path/filepath"
	"sync"
	"testing"
)

func validCfg() Config {
	return Config{
		Devices: []Device{{ID: "d1", Address: "127.0.0.1:1502", UnitID: 1, TimeoutMs: 1000}},
		Points: []Point{
			{DeviceID: "d1", Name: "p1", FuncCode: 3, Address: 0, Type: TypeU16, Scale: 1},
		},
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(c *Config){
		"unknown device": func(c *Config) { c.Points[0].DeviceID = "nope" },
		"bad func code":  func(c *Config) { c.Points[0].FuncCode = 6 },
		"range overflow": func(c *Config) {
			c.Points[0].Address = 65535
			c.Points[0].Type = TypeF32
			c.Points[0].WordOrder = WordHighFirst
		},
		"edge ok addr":   func(c *Config) { c.Points[0].Address = 65535 },
		"nan scale":      func(c *Config) { c.Points[0].Scale = nan() },
		"zero scale":     func(c *Config) { c.Points[0].Scale = 0 },
		"inf offset":     func(c *Config) { c.Points[0].Offset = inf() },
		"dup point name": func(c *Config) { c.Points = append(c.Points, c.Points[0]) },
		"dup device id":  func(c *Config) { c.Devices = append(c.Devices, c.Devices[0]) },
		"bad word order": func(c *Config) { c.Points[0].Type = TypeF32; c.Points[0].WordOrder = "weird" },
	}
	for name, mutate := range cases {
		cfg := validCfg()
		mutate(&cfg)
		err := cfg.Validate()
		if name == "edge ok addr" {
			if err != nil {
				t.Fatalf("%s: unexpected error %v", name, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

func nan() float64 { return math.NaN() }
func inf() float64 { return math.Inf(1) }

func TestStorePersistAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Version != 0 {
		t.Fatal("fresh store should be version 0")
	}
	saved, err := s.Replace(validCfg())
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 {
		t.Fatalf("version = %d, want 1", saved.Version)
	}
	// invalid replace keeps old version
	bad := validCfg()
	bad.Points[0].FuncCode = 9
	if _, err := s.Replace(bad); err == nil {
		t.Fatal("expected error")
	}
	if s.Snapshot().Version != 1 {
		t.Fatal("failed replace must keep old version")
	}
	// reload from disk
	s2, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Snapshot().Version != 1 || len(s2.Snapshot().Points) != 1 {
		t.Fatalf("reload mismatch: %+v", s2.Snapshot())
	}
}

func TestStoreConcurrentSerialized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, _ := LoadStore(path)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Replace(validCfg()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if v := s.Snapshot().Version; v != 20 {
		t.Fatalf("version = %d, want 20", v)
	}
	s2, err := LoadStore(path)
	if err != nil || s2.Snapshot().Version != 20 {
		t.Fatalf("persisted version mismatch: %v %v", s2.Snapshot().Version, err)
	}
}
