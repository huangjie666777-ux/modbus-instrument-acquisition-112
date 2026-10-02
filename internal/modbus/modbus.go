// Package modbus implements a minimal Modbus TCP client (function 03/04).
package modbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"
)

const (
	megaHeaderLen = 7
	maxPDU         = 253
)

// ExceptionError describes a Modbus exception response.
type ExceptionError struct{ Code byte }

var exceptionText = map[byte]string{
	1: "illegal function", 2: "illegal data address", 3: "illegal data value",
	4: "slave device failure", 5: "acknowledge", 6: "slave device busy",
	8: "memory parity error", 10: "gateway path unavailable", 11: "gateway target no response",
}

func (e *ExceptionError) Error() string {
	if t, ok := exceptionText[e.Code]; ok {
		return fmt.Sprintf("modbus exception %d (%s)", e.Code, t)
	}
	return fmt.Sprintf("modbus exception %d", e.Code)
}

// Client holds one TCP connection to a device for a single acquisition batch.
type Client struct {
	conn   net.Conn
	unitID byte
	nextTx atomic.Uint32
}

// Dial opens a connection honoring ctx for both dial and deadline.
func Dial(address string, unitID int, timeout time.Duration) (*Client, error) {
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, unitID: byte(unitID)}
	c.nextTx.Store(1)
	return c, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// ReadRegisters performs one read request (function 3 or 4) and returns
// count register words. It validates every MBAP and PDU field and tolerates
// TCP segmentation/coalescing by framing on the MBAP length.
func (c *Client) ReadRegisters(functionCode, address, count int, timeout time.Duration) ([]uint16, error) {
	if functionCode != 3 && functionCode != 4 {
		return nil, fmt.Errorf("unsupported function code %d", functionCode)
	}
	if count < 1 || count > 125 {
		return nil, fmt.Errorf("register count %d out of range", count)
	}
	txID := uint16(c.nextTx.Add(1) - 1)
	req := make([]byte, 12)
	binary.BigEndian.PutUint16(req[0:2], txID)
	binary.BigEndian.PutUint16(req[2:4], 0) // protocol id
	binary.BigEndian.PutUint16(req[4:6], 6) // length: unit + pdu
	req[6] = c.unitID
	req[7] = byte(functionCode)
	binary.BigEndian.PutUint16(req[8:10], uint16(address))
	binary.BigEndian.PutUint16(req[10:12], uint16(count))

	if err := c.conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(req); err != nil {
		return nil, err
	}

	hdr := make([]byte, megaHeaderLen)
	if _, err := io.ReadFull(c.conn, hdr); err != nil {
		return nil, err
	}
	gotTx := binary.BigEndian.Uint16(hdr[0:2])
	proto := binary.BigEndian.Uint16(hdr[2:4])
	length := binary.BigEndian.Uint16(hdr[4:6])
	unit := hdr[6]
	if gotTx != txID {
		return nil, fmt.Errorf("transaction id mismatch: got %d want %d", gotTx, txID)
	}
	if proto != 0 {
		return nil, fmt.Errorf("protocol id mismatch: %d", proto)
	}
	if length < 2 {
		return nil, fmt.Errorf("mbap length too small: %d", length)
	}
	if int(length-1) > maxPDU {
		return nil, fmt.Errorf("mbap length too large: %d", length)
	}
	if unit != c.unitID {
		return nil, fmt.Errorf("unit id mismatch: got %d want %d", unit, c.unitID)
	}
	body := make([]byte, length-1)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return nil, err
	}
	fc := body[0]
	if int(fc)&0x80 != 0 {
		if len(body) < 2 {
			return nil, errors.New("malformed exception response")
		}
		if int(fc&0x7f) != functionCode {
			return nil, fmt.Errorf("exception for wrong function code %d", fc&0x7f)
		}
		return nil, &ExceptionError{Code: body[1]}
	}
	if int(fc) != functionCode {
		return nil, fmt.Errorf("function code mismatch: got %d want %d", fc, functionCode)
	}
	if len(body) < 2 {
		return nil, errors.New("response truncated")
	}
	byteCount := int(body[1])
	if byteCount != count*2 {
		return nil, fmt.Errorf("byte count mismatch: got %d want %d", byteCount, count*2)
	}
	if len(body) != 2+byteCount {
		return nil, fmt.Errorf("pdu length mismatch: got %d bytes for declared %d", len(body)-2, byteCount)
	}
	words := make([]uint16, count)
	for i := 0; i < count; i++ {
		words[i] = binary.BigEndian.Uint16(body[2+i*2 : 4+i*2])
	}
	return words, nil
}
