// Package modbus implements a minimal Modbus TCP read-only client.
package modbus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"
)

// ExceptionError is a Modbus exception response (function code | 0x80).
type ExceptionError struct {
	Function uint8
	Code     uint8
}

func (e *ExceptionError) Error() string {
	return fmt.Sprintf("modbus exception: function=0x%02x code=0x%02x (%s)",
		e.Function, e.Code, ExceptionText(e.Code))
}

func ExceptionText(code uint8) string {
	switch code {
	case 1:
		return "illegal function"
	case 2:
		return "illegal data address"
	case 3:
		return "illegal data value"
	case 4:
		return "slave device failure"
	case 5:
		return "acknowledge"
	case 6:
		return "slave device busy"
	case 10:
		return "gateway path unavailable"
	case 11:
		return "gateway target failed to respond"
	default:
		return "unknown"
	}
}

// Client is a single-use Modbus TCP connection.
type Client struct {
	conn   net.Conn
	unitID uint8
	txn    uint32
}

// Dial connects to a Modbus TCP server honoring ctx cancellation and timeout.
func Dial(ctx context.Context, address string, unitID uint8, timeout time.Duration) (*Client, error) {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", address, err)
	}
	return &Client{conn: conn, unitID: unitID}, nil
}

func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// ReadRegisters issues a 0x03/0x04 request and returns the raw register data.
// ctx cancellation and the deadline both release the connection promptly.
func (c *Client) ReadRegisters(ctx context.Context, funcCode uint8, start, count uint16, timeout time.Duration) ([]byte, error) {
	if funcCode != 3 && funcCode != 4 {
		return nil, fmt.Errorf("unsupported function code %d", funcCode)
	}
	if count == 0 || count > 125 {
		return nil, fmt.Errorf("register count %d out of range (1..125)", count)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	txnID := uint16(atomic.AddUint32(&c.txn, 1))
	pdu := make([]byte, 5)
	pdu[0] = funcCode
	binary.BigEndian.PutUint16(pdu[1:], start)
	binary.BigEndian.PutUint16(pdu[3:], count)
	frame := make([]byte, 0, 12)
	frame = binary.BigEndian.AppendUint16(frame, txnID)
	frame = binary.BigEndian.AppendUint16(frame, 0) // protocol id
	frame = binary.BigEndian.AppendUint16(frame, uint16(len(pdu)+1))
	frame = append(frame, c.unitID)
	frame = append(frame, pdu...)

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	// Close the connection if the caller cancels, unblocking pending I/O.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			c.conn.Close()
		case <-done:
		}
	}()

	if _, err := c.conn.Write(frame); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}
	// Read MBAP header; io.ReadFull handles TCP segmentation.
	header := make([]byte, 7)
	if _, err := io.ReadFull(c.conn, header); err != nil {
		return nil, fmt.Errorf("read MBAP header: %w", err)
	}
	if got := binary.BigEndian.Uint16(header[0:2]); got != txnID {
		return nil, fmt.Errorf("transaction id mismatch: got %d want %d", got, txnID)
	}
	if got := binary.BigEndian.Uint16(header[2:4]); got != 0 {
		return nil, fmt.Errorf("protocol id mismatch: got %d want 0", got)
	}
	length := int(binary.BigEndian.Uint16(header[4:6]))
	if length < 2 || length > 254 {
		return nil, fmt.Errorf("invalid MBAP length %d", length)
	}
	if header[6] != c.unitID {
		return nil, fmt.Errorf("unit id mismatch: got %d want %d", header[6], c.unitID)
	}
	// length covers unit id + PDU; unit id already consumed.
	body := make([]byte, length-1)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return nil, fmt.Errorf("read PDU: %w", err)
	}
	respFunc := body[0]
	if respFunc == funcCode|0x80 {
		if len(body) < 2 {
			return nil, errors.New("malformed exception response")
		}
		return nil, &ExceptionError{Function: respFunc, Code: body[1]}
	}
	if respFunc != funcCode {
		return nil, fmt.Errorf("function code mismatch: got 0x%02x want 0x%02x", respFunc, funcCode)
	}
	if len(body) < 2 {
		return nil, errors.New("response too short for byte count")
	}
	byteCount := int(body[1])
	if byteCount != int(count)*2 {
		return nil, fmt.Errorf("byte count mismatch: got %d want %d", byteCount, count*2)
	}
	if len(body)-2 < byteCount {
		return nil, fmt.Errorf("response truncated: %d data bytes for declared %d", len(body)-2, byteCount)
	}
	return body[2 : 2+byteCount], nil
}
