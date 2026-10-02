// Package plan turns requested points into merge-split Modbus read ranges.
package plan

import (
	"sort"

	"example.com/modbus-instrument-acquisition/internal/config"
)

// MaxRegistersPerRead is the Modbus limit for function codes 03/04.
const MaxRegistersPerRead = 125

// Range is one read holding/input register request covering points.
type Range struct {
	FunctionCode int
	Address      int
	Count        int
	Points       []config.Point
}

type interval struct {
	start int // first register
	end   int // one past last register
	p     config.Point
}

// Build groups points by device and function code, merges overlapping or
// adjacent register intervals, then greedily splits them into requests of at
// most 125 registers without cutting any point. Gaps are never bridged.
func Build(points []config.Point) []Range {
	if len(points) == 0 {
		return nil
	}
	intervals := make([]interval, 0, len(points))
	for _, p := range points {
		size, _ := p.Size()
		intervals = append(intervals, interval{start: p.Address, end: p.Address + size, p: p})
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start != intervals[j].start {
			return intervals[i].start < intervals[j].start
		}
		return intervals[i].end > intervals[j].end
	})

	// Merge only overlapping or touching intervals.
	type merged struct{ start, end int; ps []config.Point }
	var groups []merged
	for _, in := range intervals {
		if len(groups) == 0 || in.start > groups[len(groups)-1].end {
			groups = append(groups, merged{start: in.start, end: in.end, ps: []config.Point{in.p}})
			continue
		}
		g := &groups[len(groups)-1]
		if in.end > g.end {
			g.end = in.end
		}
		g.ps = append(g.ps, in.p)
	}

	var out []Range
	fc := points[0].FunctionCode
	for _, g := range groups {
		// Sort points by start; longest first when starting equal, so the
		// largest member is considered when fixing segment boundaries.
		ps := append([]config.Point(nil), g.ps...)
		sort.Slice(ps, func(i, j int) bool {
			if ps[i].Address != ps[j].Address {
				return ps[i].Address < ps[j].Address
			}
			si, _ := ps[i].Size()
			sj, _ := ps[j].Size()
			return si > sj
		})
		segStart := g.start
		segPoints := []config.Point{}
		for _, p := range ps {
			size, _ := p.Size()
			pEnd := p.Address + size
			if len(segPoints) > 0 && pEnd-segStart > MaxRegistersPerRead {
				last := segPoints[len(segPoints)-1]
				lastSize, _ := last.Size()
				out = append(out, Range{FunctionCode: fc, Address: segStart,
					Count: last.Address + lastSize - segStart, Points: segPoints})
				// Start the next segment at the first register past the split.
				segStart = out[len(out)-1].Address + out[len(out)-1].Count
				segPoints = []config.Point{}
			}
			segPoints = append(segPoints, p)
		}
		if len(segPoints) > 0 {
			last := segPoints[len(segPoints)-1]
			lastSize, _ := last.Size()
			out = append(out, Range{FunctionCode: fc, Address: segStart,
				Count: last.Address + lastSize - segStart, Points: segPoints})
		}
	}
	return out
}
