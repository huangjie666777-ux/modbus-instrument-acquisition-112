package values

import (
	"testing"

	"modbus-backend/config"
)

func pt(t config.PointType, wo config.WordOrder, scale, off float64) config.Point {
	return config.Point{Type: t, WordOrder: wo, Scale: scale, Offset: off}
}

func TestDecodeU16(t *testing.T) {
	raw, eng, err := Decode(pt(config.TypeU16, "", 0.1, 0), []byte{0x09, 0x29}) // 2345
	if err != nil || raw != 2345 || eng != 234.5 {
		t.Fatalf("got raw=%v eng=%v err=%v", raw, eng, err)
	}
}

func TestDecodeI16Negative(t *testing.T) {
	raw, eng, err := Decode(pt(config.TypeI16, "", 1, 0), []byte{0xFF, 0x38}) // -200
	if err != nil || raw != -200 || eng != -200 {
		t.Fatalf("got raw=%v eng=%v err=%v", raw, eng, err)
	}
}

func TestDecodeF32HighFirst(t *testing.T) {
	// 3.14159 = 0x40490FD0
	raw, _, err := Decode(pt(config.TypeF32, config.WordHighFirst, 1, 0), []byte{0x40, 0x49, 0x0F, 0xD0})
	if err != nil || raw < 3.1415 || raw > 3.1416 {
		t.Fatalf("got raw=%v err=%v", raw, err)
	}
}

func TestDecodeF32LowFirst(t *testing.T) {
	// 2.5 = 0x40200000, low word first
	raw, _, err := Decode(pt(config.TypeF32, config.WordLowFirst, 1, 0), []byte{0x00, 0x00, 0x40, 0x20})
	if err != nil || raw != 2.5 {
		t.Fatalf("got raw=%v err=%v", raw, err)
	}
}

func TestDecodeF32NaNFails(t *testing.T) {
	_, _, err := Decode(pt(config.TypeF32, config.WordHighFirst, 1, 0), []byte{0x7F, 0xC0, 0x00, 0x00})
	if err == nil {
		t.Fatal("expected error for NaN")
	}
}

func TestDecodeScaleOffsetOverflow(t *testing.T) {
	_, _, err := Decode(pt(config.TypeU16, "", 1e308, 1e308), []byte{0xFF, 0xFF})
	if err == nil {
		t.Fatal("expected error for non-finite engineering value")
	}
}
