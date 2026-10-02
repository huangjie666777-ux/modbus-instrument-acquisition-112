package collect

import (
	"context"
	"testing"
	"time"

	"modbus-backend/config"
	"modbus-backend/sim"
)

func setup(t *testing.T) (config.Config, *sim.Device) {
	t.Helper()
	holding := make([]uint16, 256)
	input := make([]uint16, 256)
	holding[0] = 2345    // u16
	holding[1] = 0xFF38  // i16 = -200
	holding[10] = 0x4049 // f32 high-first 3.14159
	holding[11] = 0x0FD0
	holding[12] = 0x0000 // f32 low-first 2.5
	holding[13] = 0x4020
	holding[20] = 0x7FC0 // f32 NaN
	holding[21] = 0x0000
	input[0] = 100
	dev, err := sim.New("127.0.0.1:0", 1, holding, input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dev.Close() })
	cfg := config.Config{
		Version: 7,
		Devices: []config.Device{{ID: "meter", Address: dev.Addr(), UnitID: 1, TimeoutMs: 2000}},
		Points: []config.Point{
			{DeviceID: "meter", Name: "voltage", FuncCode: 3, Address: 0, Type: config.TypeU16, Scale: 0.1},
			{DeviceID: "meter", Name: "temp", FuncCode: 3, Address: 1, Type: config.TypeI16, Scale: 0.1, Offset: -5},
			{DeviceID: "meter", Name: "pi", FuncCode: 3, Address: 10, Type: config.TypeF32, WordOrder: config.WordHighFirst, Scale: 1},
			{DeviceID: "meter", Name: "twofive", FuncCode: 3, Address: 12, Type: config.TypeF32, WordOrder: config.WordLowFirst, Scale: 2, Offset: 1},
			{DeviceID: "meter", Name: "nan", FuncCode: 3, Address: 20, Type: config.TypeF32, WordOrder: config.WordHighFirst, Scale: 1},
			{DeviceID: "meter", Name: "count", FuncCode: 4, Address: 0, Type: config.TypeU16, Scale: 1},
			{DeviceID: "meter", Name: "missing", FuncCode: 3, Address: 300, Type: config.TypeU16, Scale: 1},
		},
	}
	return cfg, dev
}

func TestCollectEndToEnd(t *testing.T) {
	cfg, dev := setup(t)
	dev.SetChunking(3) // force TCP segmentation
	res := New(4).Collect(context.Background(), cfg,
		[]string{"voltage", "temp", "pi", "twofive", "nan", "count", "missing", "nope"})
	if res.Version != 7 {
		t.Fatalf("version = %d, want 7", res.Version)
	}
	got := map[string]PointResult{}
	for _, r := range res.Results {
		got[r.Name] = r
	}
	if r := got["voltage"]; !r.OK || r.Raw != 2345 || r.Engineering != 234.5 {
		t.Fatalf("voltage: %+v", r)
	}
	if r := got["temp"]; !r.OK || r.Raw != -200 || r.Engineering != -25 {
		t.Fatalf("temp: %+v", r)
	}
	if r := got["pi"]; !r.OK || r.Raw < 3.1415 || r.Raw > 3.1416 {
		t.Fatalf("pi: %+v", r)
	}
	if r := got["twofive"]; !r.OK || r.Engineering != 6 {
		t.Fatalf("twofive: %+v", r)
	}
	if r := got["nan"]; r.OK || r.Error == "" {
		t.Fatalf("nan should fail: %+v", r)
	}
	if r := got["count"]; !r.OK || r.Raw != 100 {
		t.Fatalf("count: %+v", r)
	}
	if r := got["missing"]; r.OK || r.Error == "" {
		t.Fatalf("missing should fail with exception: %+v", r)
	}
	if r := got["nope"]; r.OK || r.Error != "unknown point name" {
		t.Fatalf("nope: %+v", r)
	}
	// successes kept despite partial failures
	if !got["voltage"].OK {
		t.Fatal("partial failure must not wipe successes")
	}
}

func TestCollectUnreachableDevice(t *testing.T) {
	cfg := config.Config{
		Version: 1,
		Devices: []config.Device{{ID: "down", Address: "127.0.0.1:1", UnitID: 1, TimeoutMs: 300}},
		Points:  []config.Point{{DeviceID: "down", Name: "x", FuncCode: 3, Address: 0, Type: config.TypeU16, Scale: 1}},
	}
	start := time.Now()
	res := New(2).Collect(context.Background(), cfg, []string{"x"})
	if res.Results[0].OK || res.Results[0].Error == "" {
		t.Fatalf("expected connect failure: %+v", res.Results[0])
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout not honored")
	}
}

func TestCollectCancel(t *testing.T) {
	cfg, _ := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cfg.Devices[0].TimeoutMs = 10000
	// shrink sim table response by pointing at unreachable? instead just cancel quickly
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	res := New(1).Collect(ctx, cfg, []string{"voltage"})
	_ = res // either success (fast local) or cancelled; must not hang
}
