// Package values decodes raw register bytes into engineering values.
package values

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"modbus-backend/config"
)

// Decode interprets raw register bytes (big-endian within each register)
// according to the point definition, then applies scale and offset.
// Returns the raw numeric value and the engineering value.
func Decode(p config.Point, regs []byte) (raw float64, engineering float64, err error) {
	switch p.Type {
	case config.TypeU16:
		if len(regs) < 2 {
			return 0, 0, errors.New("need 2 bytes for u16")
		}
		raw = float64(binary.BigEndian.Uint16(regs))
	case config.TypeI16:
		if len(regs) < 2 {
			return 0, 0, errors.New("need 2 bytes for i16")
		}
		raw = float64(int16(binary.BigEndian.Uint16(regs)))
	case config.TypeF32:
		if len(regs) < 4 {
			return 0, 0, errors.New("need 4 bytes for f32")
		}
		var bits uint32
		if p.WordOrder == config.WordLowFirst {
			bits = uint32(binary.BigEndian.Uint16(regs)) |
				uint32(binary.BigEndian.Uint16(regs[2:]))<<16
		} else {
			bits = uint32(binary.BigEndian.Uint16(regs))<<16 |
				uint32(binary.BigEndian.Uint16(regs[2:]))
		}
		raw = float64(math.Float32frombits(bits))
	default:
		return 0, 0, fmt.Errorf("unsupported type %q", p.Type)
	}
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return raw, 0, errors.New("raw value is not finite (NaN/Inf in f32)")
	}
	engineering = raw*p.Scale + p.Offset
	if math.IsNaN(engineering) || math.IsInf(engineering, 0) {
		return raw, 0, errors.New("engineering value is not finite after scale/offset")
	}
	return raw, engineering, nil
}
