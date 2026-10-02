// Package sim is a local Modbus TCP register device for demos and tests.
package sim

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
)

// Device serves holding (0x03) and input (0x04) registers over Modbus TCP.
type Device struct {
	ln      net.Listener
	mu      sync.Mutex
	holding []uint16
	input   []uint16
	unitID  uint8
	chunkAt int // if >0, split responses into chunks of this size to exercise reassembly
	done    chan struct{}
}

func New(address string, unitID uint8, holding, input []uint16) (*Device, error) {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	d := &Device{ln: ln, holding: holding, input: input, unitID: unitID, done: make(chan struct{})}
	go d.accept()
	return d, nil
}

func (d *Device) Addr() string { return d.ln.Addr().String() }

// SetChunking makes responses go out in small TCP segments (tests 粘包/分段).
func (d *Device) SetChunking(n int) { d.chunkAt = n }

func (d *Device) SetHolding(addr uint16, vals ...uint16) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, v := range vals {
		if int(addr)+i < len(d.holding) {
			d.holding[int(addr)+i] = v
		}
	}
}

func (d *Device) Close() error {
	close(d.done)
	return d.ln.Close()
}

func (d *Device) accept() {
	for {
		conn, err := d.ln.Accept()
		if err != nil {
			return
		}
		go d.serve(conn)
	}
}

func (d *Device) serve(conn net.Conn) {
	defer conn.Close()
	for {
		header := make([]byte, 7)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint16(header[4:6]))
		if length < 2 || length > 254 {
			return
		}
		body := make([]byte, length-1)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		resp := d.handle(header, body)
		if resp == nil {
			return
		}
		if d.chunkAt > 0 {
			for len(resp) > 0 {
				n := d.chunkAt
				if n > len(resp) {
					n = len(resp)
				}
				if _, err := conn.Write(resp[:n]); err != nil {
					return
				}
				resp = resp[n:]
			}
		} else if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

func (d *Device) handle(header, body []byte) []byte {
	txn := binary.BigEndian.Uint16(header[0:2])
	unit := header[6]
	if unit != d.unitID {
		return nil
	}
	funcCode := body[0]
	var respPDU []byte
	switch funcCode {
	case 3, 4:
		if len(body) < 5 {
			return nil
		}
		start := binary.BigEndian.Uint16(body[1:3])
		count := binary.BigEndian.Uint16(body[3:5])
		d.mu.Lock()
		var table []uint16
		if funcCode == 3 {
			table = d.holding
		} else {
			table = d.input
		}
		end := int(start) + int(count)
		if count == 0 || count > 125 || end > len(table) {
			respPDU = []byte{funcCode | 0x80, 2} // illegal data address
		} else {
			data := make([]byte, 0, count*2)
			for _, v := range table[start:end] {
				data = binary.BigEndian.AppendUint16(data, v)
			}
			respPDU = append([]byte{funcCode, byte(len(data))}, data...)
		}
		d.mu.Unlock()
	default:
		respPDU = []byte{funcCode | 0x80, 1} // illegal function
	}
	frame := make([]byte, 0, 7+len(respPDU))
	frame = binary.BigEndian.AppendUint16(frame, txn)
	frame = binary.BigEndian.AppendUint16(frame, 0)
	frame = binary.BigEndian.AppendUint16(frame, uint16(len(respPDU)+1))
	frame = append(frame, unit)
	return append(frame, respPDU...)
}
