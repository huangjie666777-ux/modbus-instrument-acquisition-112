package modbus

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// fakeServer runs handler with raw frames for protocol-level tests.
func fakeServer(t *testing.T, handler func(conn net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handler(c)
		}
	}()
	return ln.Addr().String()
}

func readReq(t *testing.T, conn net.Conn) (txn uint16, unit uint8, body []byte) {
	t.Helper()
	header := make([]byte, 7)
	if _, err := readFull(conn, header); err != nil {
		t.Fatal(err)
	}
	length := int(binary.BigEndian.Uint16(header[4:6]))
	body = make([]byte, length-1)
	if _, err := readFull(conn, body); err != nil {
		t.Fatal(err)
	}
	return binary.BigEndian.Uint16(header[0:2]), header[6], body
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := conn.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func writeFrame(txn uint16, unit uint8, pdu []byte) []byte {
	frame := make([]byte, 0, 7+len(pdu))
	frame = binary.BigEndian.AppendUint16(frame, txn)
	frame = binary.BigEndian.AppendUint16(frame, 0)
	frame = binary.BigEndian.AppendUint16(frame, uint16(len(pdu)+1))
	frame = append(frame, unit)
	return append(frame, pdu...)
}

func TestReadRegistersChunked(t *testing.T) {
	addr := fakeServer(t, func(conn net.Conn) {
		defer conn.Close()
		txn, unit, body := readReq(t, conn)
		if body[0] != 3 {
			t.Errorf("func code = %d", body[0])
		}
		data := []byte{0x00, 0x2A, 0x00, 0x2B}
		pdu := append([]byte{3, byte(len(data))}, data...)
		frame := writeFrame(txn, unit, pdu)
		// send byte by byte to force segmentation
		for _, b := range frame {
			conn.Write([]byte{b})
			time.Sleep(time.Millisecond)
		}
	})
	cli, err := Dial(context.Background(), addr, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	data, err := cli.ReadRegisters(context.Background(), 3, 0, 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 4 || data[1] != 0x2A || data[3] != 0x2B {
		t.Fatalf("unexpected data %x", data)
	}
}

func TestExceptionResponse(t *testing.T) {
	addr := fakeServer(t, func(conn net.Conn) {
		defer conn.Close()
		txn, unit, _ := readReq(t, conn)
		conn.Write(writeFrame(txn, unit, []byte{0x83, 0x02}))
	})
	cli, _ := Dial(context.Background(), addr, 1, time.Second)
	defer cli.Close()
	_, err := cli.ReadRegisters(context.Background(), 3, 0, 1, time.Second)
	exc, ok := err.(*ExceptionError)
	if !ok || exc.Code != 2 {
		t.Fatalf("expected exception code 2, got %v", err)
	}
}

func TestTxnIDMismatch(t *testing.T) {
	addr := fakeServer(t, func(conn net.Conn) {
		defer conn.Close()
		_, unit, _ := readReq(t, conn)
		conn.Write(writeFrame(9999, unit, []byte{3, 2, 0, 1}))
	})
	cli, _ := Dial(context.Background(), addr, 1, time.Second)
	defer cli.Close()
	if _, err := cli.ReadRegisters(context.Background(), 3, 0, 1, time.Second); err == nil {
		t.Fatal("expected transaction id mismatch error")
	}
}

func TestUnitIDMismatch(t *testing.T) {
	addr := fakeServer(t, func(conn net.Conn) {
		defer conn.Close()
		txn, _, _ := readReq(t, conn)
		conn.Write(writeFrame(txn, 7, []byte{3, 2, 0, 1}))
	})
	cli, _ := Dial(context.Background(), addr, 1, time.Second)
	defer cli.Close()
	if _, err := cli.ReadRegisters(context.Background(), 3, 0, 1, time.Second); err == nil {
		t.Fatal("expected unit id mismatch error")
	}
}

func TestByteCountMismatch(t *testing.T) {
	addr := fakeServer(t, func(conn net.Conn) {
		defer conn.Close()
		txn, unit, _ := readReq(t, conn)
		conn.Write(writeFrame(txn, unit, []byte{3, 4, 0, 1, 0, 2})) // asked 1 reg
	})
	cli, _ := Dial(context.Background(), addr, 1, time.Second)
	defer cli.Close()
	if _, err := cli.ReadRegisters(context.Background(), 3, 0, 1, time.Second); err == nil {
		t.Fatal("expected byte count mismatch error")
	}
}

func TestContextCancelReleases(t *testing.T) {
	addr := fakeServer(t, func(conn net.Conn) {
		defer conn.Close()
		readReq(t, conn)
		time.Sleep(5 * time.Second) // never respond in time
	})
	cli, _ := Dial(context.Background(), addr, 1, time.Second)
	defer cli.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	if _, err := cli.ReadRegisters(ctx, 3, 0, 1, 10*time.Second); err == nil {
		t.Fatal("expected cancellation error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancellation did not release promptly")
	}
}
