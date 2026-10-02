package plan

import (
	"testing"

	"modbus-backend/config"
)

func mk(name, dev string, code uint8, addr uint16, typ config.PointType) config.Point {
	return config.Point{Name: name, DeviceID: dev, FuncCode: code, Address: addr, Type: typ,
		WordOrder: config.WordHighFirst, Scale: 1}
}

func TestMergeAdjacentAndOverlap(t *testing.T) {
	reqs := Build([]config.Point{
		mk("a", "d1", 3, 0, config.TypeU16),
		mk("b", "d1", 3, 1, config.TypeF32), // regs 1-2, adjacent
		mk("c", "d1", 3, 2, config.TypeU16), // overlaps b
		mk("d", "d1", 3, 10, config.TypeU16),
		mk("e", "d1", 4, 0, config.TypeU16), // different func code
		mk("f", "d2", 3, 0, config.TypeU16), // different device
	})
	if len(reqs) != 4 {
		t.Fatalf("expected 4 requests, got %d: %+v", len(reqs), reqs)
	}
	var merged *Request
	for i := range reqs {
		if reqs[i].DeviceID == "d1" && reqs[i].FuncCode == 3 && reqs[i].Start == 0 {
			merged = &reqs[i]
		}
	}
	if merged == nil || merged.Count != 3 || len(merged.PointRefs) != 3 {
		t.Fatalf("bad merged request: %+v", merged)
	}
	for _, ref := range merged.PointRefs {
		want := int(ref.Point.Address) * 2
		if ref.ByteOffset != want {
			t.Fatalf("point %s offset %d want %d", ref.Point.Name, ref.ByteOffset, want)
		}
	}
}

func TestSplitNeverCutsPoint(t *testing.T) {
	var pts []config.Point
	for i := 0; i < 63; i++ {
		pts = append(pts, mk(string(rune('a'+i%26))+string(rune('A'+i/26)), "d1", 3, uint16(i*2), config.TypeF32))
	}
	reqs := Build(pts)
	for _, r := range reqs {
		if r.Count > MaxRegisters {
			t.Fatalf("request exceeds %d registers: %+v", MaxRegisters, r)
		}
		for _, ref := range r.PointRefs {
			end := ref.ByteOffset + int(ref.Point.RegCount())*2
			if end > int(r.Count)*2 {
				t.Fatalf("point %s cut by request %+v", ref.Point.Name, r)
			}
		}
	}
	total := 0
	for _, r := range reqs {
		total += len(r.PointRefs)
	}
	if total != 63 {
		t.Fatalf("lost points: %d of 63", total)
	}
}
