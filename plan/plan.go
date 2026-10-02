// Package plan merges point register ranges into read requests.
package plan

import (
	"sort"

	"modbus-backend/config"
)

const MaxRegisters = 125

// Request is one Modbus read: registers [Start, Start+Count) via FuncCode.
type Request struct {
	DeviceID  string
	FuncCode  uint8
	Start     uint16
	Count     uint16
	PointRefs []PointRef
}

// PointRef links a point to its byte offset inside a request response.
type PointRef struct {
	Point      config.Point
	ByteOffset int // offset of the point's first register within the response data
}

// Build groups points by device+func code, merges overlapping or adjacent
// ranges, caps each request at MaxRegisters and never splits a point.
func Build(points []config.Point) []Request {
	type key struct {
		dev  string
		code uint8
	}
	groups := map[key][]config.Point{}
	var order []key
	for _, p := range points {
		k := key{p.DeviceID, p.FuncCode}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], p)
	}
	var reqs []Request
	for _, k := range order {
		pts := groups[k]
		sort.Slice(pts, func(i, j int) bool { return pts[i].Address < pts[j].Address })
		var cur *Request
		curEnd := uint32(0)
		flush := func() {
			if cur != nil {
				reqs = append(reqs, *cur)
				cur = nil
			}
		}
		for _, p := range pts {
			start := uint32(p.Address)
			end := start + uint32(p.RegCount())
			switch {
			case cur == nil:
				cur = &Request{DeviceID: k.dev, FuncCode: k.code, Start: p.Address, Count: p.RegCount()}
				curEnd = end
			case start <= curEnd && end <= curEnd:
				// fully contained, nothing to extend
			case start <= curEnd:
				// overlapping/adjacent: extend
				cur.Count = uint16(end - uint32(cur.Start))
				curEnd = end
			default:
				// gap: new request
				flush()
				cur = &Request{DeviceID: k.dev, FuncCode: k.code, Start: p.Address, Count: p.RegCount()}
				curEnd = end
			}
			// split if the merged range would exceed MaxRegisters,
			// without cutting through this point
			if cur.Count > MaxRegisters {
				// close the request right before this point
				prevEnd := uint32(cur.Start) + uint32(cur.Count)
				_ = prevEnd
				splitStart := p.Address
				saved := *cur
				saved.Count = splitStart - saved.Start
				if saved.Count > 0 {
					reqs = append(reqs, saved)
				}
				cur = &Request{DeviceID: k.dev, FuncCode: k.code, Start: p.Address, Count: p.RegCount()}
				curEnd = end
			}
			cur.PointRefs = append(cur.PointRefs, PointRef{
				Point:      p,
				ByteOffset: int(p.Address-cur.Start) * 2,
			})
		}
		flush()
	}
	return reqs
}
