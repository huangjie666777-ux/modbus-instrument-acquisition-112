// Package collect executes read plans against devices with bounded concurrency.
package collect

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"modbus-backend/config"
	"modbus-backend/modbus"
	"modbus-backend/plan"
	"modbus-backend/values"
)

type PointResult struct {
	Name        string  `json:"name"`
	OK          bool    `json:"ok"`
	Raw         float64 `json:"raw,omitempty"`
	Engineering float64 `json:"engineering,omitempty"`
	Error       string  `json:"error,omitempty"`
}

type Result struct {
	Version uint64        `json:"version"`
	Results []PointResult `json:"results"`
}

// Collector runs acquisitions. Devices are read in parallel up to
// MaxParallel; requests to the same device are serialized.
type Collector struct {
	MaxParallel int
}

func New(maxParallel int) *Collector {
	if maxParallel <= 0 {
		maxParallel = 8
	}
	return &Collector{MaxParallel: maxParallel}
}

// Collect reads the named points using exactly the given config snapshot.
func (c *Collector) Collect(ctx context.Context, cfg config.Config, names []string) Result {
	res := Result{Version: cfg.Version, Results: make([]PointResult, len(names))}
	byName := map[string]*PointResult{}
	var selected []config.Point
	for i, n := range names {
		res.Results[i].Name = n
		byName[n] = &res.Results[i]
		p, ok := cfg.PointByName(n)
		if !ok {
			byName[n].Error = "unknown point name"
			continue
		}
		selected = append(selected, p)
	}

	reqs := plan.Build(selected)
	// group requests per device
	perDevice := map[string][]plan.Request{}
	var devOrder []string
	for _, r := range reqs {
		if _, ok := perDevice[r.DeviceID]; !ok {
			devOrder = append(devOrder, r.DeviceID)
		}
		perDevice[r.DeviceID] = append(perDevice[r.DeviceID], r)
	}
	sort.Strings(devOrder)

	sem := make(chan struct{}, c.MaxParallel)
	var wg sync.WaitGroup
	for _, devID := range devOrder {
		dev, ok := cfg.DeviceByID(devID)
		if !ok {
			for _, r := range perDevice[devID] {
				for _, ref := range r.PointRefs {
					byName[ref.Point.Name].Error = "unknown device " + devID
				}
			}
			continue
		}
		wg.Add(1)
		go func(dev config.Device, reqs []plan.Request) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				for _, r := range reqs {
					for _, ref := range r.PointRefs {
						byName[ref.Point.Name].Error = "cancelled: " + ctx.Err().Error()
					}
				}
				return
			}
			c.readDevice(ctx, dev, reqs, byName)
		}(dev, perDevice[devID])
	}
	wg.Wait()
	return res
}

// readDevice serially executes all requests of one device on one connection.
func (c *Collector) readDevice(ctx context.Context, dev config.Device, reqs []plan.Request, byName map[string]*PointResult) {
	timeout := time.Duration(dev.TimeoutMs) * time.Millisecond
	cli, err := modbus.Dial(ctx, dev.Address, dev.UnitID, timeout)
	if err != nil {
		for _, r := range reqs {
			for _, ref := range r.PointRefs {
				byName[ref.Point.Name].Error = "connect: " + err.Error()
			}
		}
		return
	}
	defer cli.Close()
	for _, r := range reqs {
		data, err := cli.ReadRegisters(ctx, r.FuncCode, r.Start, r.Count, timeout)
		if err != nil {
			for _, ref := range r.PointRefs {
				byName[ref.Point.Name].Error = "read: " + err.Error()
			}
			var exc *modbus.ExceptionError
			if errors.As(err, &exc) {
				// exception frames keep the stream in sync; continue
				continue
			}
			// transport/protocol errors may desync the stream; drop the connection
			return
		}
		for _, ref := range r.PointRefs {
			pr := byName[ref.Point.Name]
			end := ref.ByteOffset + int(ref.Point.RegCount())*2
			if end > len(data) {
				pr.Error = fmt.Sprintf("response too short for point (need bytes %d..%d of %d)", ref.ByteOffset, end, len(data))
				continue
			}
			raw, eng, err := values.Decode(ref.Point, data[ref.ByteOffset:end])
			if err != nil {
				pr.Error = err.Error()
				continue
			}
			pr.Raw = raw
			pr.Engineering = eng
			pr.OK = true
		}
	}
}
