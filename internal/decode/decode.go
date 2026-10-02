// Package decode converts Modbus big-endian register words to typed values.
package decode

import (
	"encoding/binary"
	"fmt"
	"math"

	"example.com/modbus-instrument-acquisition/internal/config"
)

// Decode reads the point's words out of a register map (address -> word).
// It returns the raw numeric value and the scaled engineering value.
func Decode(p config.Point, regs map[int]uint16) (raw float64, eng float64, err error) {
	switch p.Type {
	case "u16":
		w, ok := regs[p.Address]
		if !ok {
			return 0, 0, fmt.Errorf("register %d missing from response", p.Address)
		}
		raw = float64(w)
	case "i16":
		w, ok := regs[p.Address]
		if !ok {
			return 0, 0, fmt.Errorf("register %d missing from response", p.Address)
		}
		raw = float64(int16(w))
	case "f32":
		hiAddr, loAddr := p.Address, p.Address+1 // high word first
		if p.WordOrder == "low" {
			hiAddr, loAddr = p.Address+1, p.Address
		}
		hi, ok1 := regs[hiAddr]
		lo, ok2 := regs[loAddr]
		if !ok1 || !ok2 {
			return 0, 0, fmt.Errorf("registers %d,%d missing from response", p.Address, p.Address+1)
		}
		var buf [4]byte
		binary.BigEndian.PutUint16(buf[0:2], hi)
		binary.BigEndian.PutUint16(buf[2:4], lo)
		bits := binary.BigEndian.Uint32(buf[:])
		raw = float64(math.Float32frombits(bits))
	default:
		return 0, 0, fmt.Errorf("unsupported type %q", p.Type)
	}
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return 0, 0, fmt.Errorf("non-finite raw value")
	}
	eng = raw*p.ScaleOrDefault() + p.OffsetOrDefault()
	if math.IsNaN(eng) || math.IsInf(eng, 0) {
		return raw, 0, fmt.Errorf("non-finite engineering value")
	}
	return raw, eng, nil
}
