// Package service orchestrates concurrent per-device Modbus acquisition.
package service

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"example.com/modbus-instrument-acquisition/internal/config"
	"example.com/modbus-instrument-acquisition/internal/decode"
	"example.com/modbus-instrument-acquisition/internal/modbus"
	"example.com/modbus-instrument-acquisition/internal/plan"
)

// PointResult is the outcome for one requested point name.
type PointResult struct {
	Name        string   `json:"name"`
	DeviceID    string   `json:"device_id"`
	RawValue    *float64 `json:"raw_value,omitempty"`
	Value       *float64 `json:"value,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// Response carries the fixed configuration version and per-point results.
type Response struct {
	Version int64         `json:"version"`
	Results []PointResult `json:"results"`
}

// Service schedules acquisitions against a config store.
type Service struct {
	store *config.Store
	sem   chan struct{}

	mu      sync.Mutex
	devices map[string]*sync.Mutex
}

// New creates a Service with maxConcurrent device workers (>=1).
func New(store *config.Store, maxConcurrent int) *Service {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Service{store: store, sem: make(chan struct{}, maxConcurrent), devices: map[string]*sync.Mutex{}}
}

func (s *Service) deviceLock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.devices[id]
	if !ok {
		m = &sync.Mutex{}
		s.devices[id] = m
	}
	return m
}

type deviceJob struct {
	device config.Device
	points []config.Point
}

// Collect reads the named points using one immutable config snapshot.
func (s *Service) Collect(ctx context.Context, names []string) Response {
	snap := s.store.Snapshot()
	results := make([]PointResult, len(names))
	byName := make(map[string]int, len(names))
	jobs := map[string]*deviceJob{}

	for i, raw := range names {
		name := raw
		results[i] = PointResult{Name: name}
		if _, dup := byName[name]; dup {
			results[i].Error = "duplicate point name in request"
			continue
		}
		byName[name] = i
		p, ok := snap.Point(name)
		if !ok {
			results[i].Error = "point not found in config"
			continue
		}
		d, ok := snap.Device(p.DeviceID)
		if !ok {
			results[i].Error = "device not found in config"
			continue
		}
		results[i].DeviceID = d.ID
		j := jobs[d.ID]
		if j == nil {
			j = &deviceJob{device: d}
			jobs[d.ID] = j
		}
		j.points = append(j.points, p)
	}

	var wg sync.WaitGroup
	for _, j := range jobs {
		j := j
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runDevice(ctx, snap, j, results, byName)
		}()
	}
	wg.Wait()
	return Response{Version: snap.Version, Results: results}
}

func (s *Service) runDevice(ctx context.Context, snap config.Snapshot, j *deviceJob, results []PointResult, byName map[string]int) {
	// Same device reads are serialized within and across concurrent requests.
	lock := s.deviceLock(j.device.ID)
	lock.Lock()
	defer lock.Unlock()

	// Acquire a global worker slot; cancellation/timeout must release it.
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		failAll(j.points, results, byName, ctx.Err())
		return
	}
	if err := ctx.Err(); err != nil {
		failAll(j.points, results, byName, err)
		return
	}

	// Split by function code, each planned independently.
	var fc3, fc4 []config.Point
	for _, p := range j.points {
		if p.FunctionCode == 3 {
			fc3 = append(fc3, p)
		}
		fc4 = append(fc4, p)
	}
	readGroups := []struct {
	fc int
	ps []config.Point
	}{{3, fc3}, {4, fc4}}

	client, err := modbus.Dial(j.device.Address, j.device.UnitID, j.device.Timeout.Duration)
	if err != nil {
		failAll(j.points, results, byName, fmt.Errorf("dial: %w", err))
		return
	}
	defer client.Close()
	go func() {
		<-ctx.Done()
		client.Close()
	}()

	for _, g := range readGroups {
		if len(g.ps) == 0 {
			continue
		}
		ranges := plan.Build(g.ps)
		for _, r := range ranges {
			if err := ctx.Err(); err != nil {
				failAll(r.Points, results, byName, err)
				continue
			}
			words, err := client.ReadRegisters(r.FunctionCode, r.Address, r.Count, j.device.Timeout.Duration)
			if err != nil {
				failAll(r.Points, results, byName, err)
				continue
			}
			regs := make(map[int]uint16, r.Count)
			for i, w := range words {
				regs[r.Address+i] = w
			}
			for _, p := range r.Points {
				idx := byName[p.Name]
				raw, eng, derr := decode.Decode(p, regs)
				if derr != nil {
					results[idx].Error = derr.Error()
					continue
				}
				results[idx].RawValue = &raw
				results[idx].Value = &eng
			}
		}
	}
}

func failAll(points []config.Point, results []PointResult, byName map[string]int, err error) {
	msg := err.Error()
	for _, p := range points {
		if i, ok := byName[p.Name]; ok && results[i].Value == nil && results[i].Error == "" {
			results[i].Error = msg
		}
	}
}

// SortedNames returns configuration point names in deterministic order.
func SortedNames(snap config.Snapshot) []string {
	names := make([]string, 0)
	for _, p := range snap.Config.Points {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names
}
