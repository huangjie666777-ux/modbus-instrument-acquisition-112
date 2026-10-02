package config

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"strings"
	"time"
)

// Duration wraps time.Duration and accepts either a duration string
// ("500ms", "2s") or a plain number interpreted as milliseconds.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("timeout must be a duration string (e.g. \"1s\") or milliseconds number: %w", err)
		}
		d.Duration = parsed
		return nil
	}
	var ms int64
	if err := json.Unmarshal(b, &ms); err != nil {
		return fmt.Errorf("timeout must be a duration string or milliseconds number")
	}
	if ms < 0 {
		return fmt.Errorf("timeout must be positive")
	}
	d.Duration = time.Duration(ms) * time.Millisecond
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

// Device describes one Modbus TCP slave endpoint.
type Device struct {
	ID      string   `json:"id"`
	Address string   `json:"address"`
	UnitID  int      `json:"unit_id"`
	Timeout Duration `json:"timeout"`
}

// Point describes one register-backed instrument value.
type Point struct {
	DeviceID     string   `json:"device_id"`
	Name         string   `json:"name"`
	FunctionCode int      `json:"function_code"`
	Address      int      `json:"address"`
	Type         string   `json:"type"`
	WordOrder    string   `json:"word_order"`
	Scale        *float64 `json:"scale"`
	Offset       *float64 `json:"offset"`
}

// Config is the whole device and point table.
type Config struct {
	Devices []Device `json:"devices"`
	Points  []Point  `json:"points"`
}

// Size returns the register count occupied by a point type.
func (p Point) Size() (int, error) {
	switch p.Type {
	case "u16", "i16":
		return 1, nil
	case "f32":
		return 2, nil
	default:
		return 0, fmt.Errorf("unknown type %q", p.Type)
	}
}

const maxRegisterAddress = 65535 // registers are addressed 0..65534; end address must not pass 65535

// Validate checks the full table for uniqueness, references, ranges and
// finite numbers. It does not mutate the configuration.
func (c *Config) Validate() error {
	devices := make(map[string]struct{}, len(c.Devices))
	for i := range c.Devices {
		d := &c.Devices[i]
		id := strings.TrimSpace(d.ID)
		if id == "" {
			return fmt.Errorf("devices[%d]: id is required", i)
		}
		if _, dup := devices[id]; dup {
			return fmt.Errorf("devices[%d]: duplicate device id %q", i, id)
		}
		host, port, err := net.SplitHostPort(strings.TrimSpace(d.Address))
		if err != nil || host == "" {
			return fmt.Errorf("devices[%d] (%s): address must be host:port", i, id)
		}
		if p := mustPort(port); p < 1 || p > 65535 {
			return fmt.Errorf("devices[%d] (%s): port out of range", i, id)
		}
		if d.UnitID < 0 || d.UnitID > 255 {
			return fmt.Errorf("devices[%d] (%s): unit_id must be 0..255", i, id)
		}
		if d.Timeout.Duration <= 0 {
			return fmt.Errorf("devices[%d] (%s): timeout must be positive", i, id)
		}
		if d.Timeout.Duration > 60*time.Second {
			return fmt.Errorf("devices[%d] (%s): timeout must not exceed 60s", i, id)
		}
		devices[id] = struct{}{}
	}

	points := make(map[string]struct{}, len(c.Points))
	for i := range c.Points {
		p := &c.Points[i]
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return fmt.Errorf("points[%d]: name is required", i)
		}
		if _, dup := points[name]; dup {
			return fmt.Errorf("points[%d]: duplicate point name %q", i, name)
		}
		if _, ok := devices[p.DeviceID]; !ok {
			return fmt.Errorf("points[%d] (%s): unknown device_id %q", i, name, p.DeviceID)
		}
		if p.FunctionCode != 3 && p.FunctionCode != 4 {
			return fmt.Errorf("points[%d] (%s): function_code must be 3 or 4", i, name)
		}
		if p.Address < 0 || p.Address > maxRegisterAddress-1 {
			return fmt.Errorf("points[%d] (%s): address must be 0..65534", i, name)
		}
		size, err := p.Size()
		if err != nil {
			return fmt.Errorf("points[%d] (%s): %w", i, name, err)
		}
		if p.Address+size > maxRegisterAddress {
			return fmt.Errorf("points[%d] (%s): register range %d..%d crosses 65535", i, name, p.Address, p.Address+size-1)
		}
		switch p.Type {
		case "u16", "i16":
			if p.WordOrder != "" && p.WordOrder != "high" {
				return fmt.Errorf("points[%d] (%s): word_order is only valid for f32", i, name)
			}
		case "f32":
			if p.WordOrder != "high" && p.WordOrder != "low" {
				return fmt.Errorf("points[%d] (%s): word_order must be \"high\" or \"low\" for f32", i, name)
			}
		}
		if p.Scale != nil && !finite(*p.Scale) {
			return fmt.Errorf("points[%d] (%s): scale must be finite", i, name)
		}
		if p.Offset != nil && !finite(*p.Offset) {
			return fmt.Errorf("points[%d] (%s): offset must be finite", i, name)
		}
		points[name] = struct{}{}
	}
	return nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func mustPort(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
		if n > 65535 {
			return -1
		}
	}
	return n
}

// ScaleOrDefault returns the configured multiplier or 1.
func (p Point) ScaleOrDefault() float64 {
	if p.Scale != nil {
		return *p.Scale
	}
	return 1
}

// OffsetOrDefault returns the configured offset or 0.
func (p Point) OffsetOrDefault() float64 {
	if p.Offset != nil {
		return *p.Offset
	}
	return 0
}
