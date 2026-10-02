// Command simulator is a local Modbus TCP register device for demos/tests.
package main

import (
	"encoding/binary"
	"flag"
	"io"
	"log"
	"math"
	"net"
)

type registerBank struct {
	holding [65536]uint16
	input   [65536]uint16
}

func newBank() *registerBank {
	b := &registerBank{}
	// holding[0] u16=230, holding[1] i16=-123, holding[2..3] f32 high-first=36.6
	b.holding[0] = 230
	var negTemp int16 = -123
	b.holding[1] = uint16(negTemp)
	bits := math.Float32bits(36.6)
	b.holding[2] = uint16(bits >> 16)
	b.holding[3] = uint16(bits)
	// input[0] u16=500, input[1..2] f32 low-word-first=1013.25
	b.input[0] = 500
	ibits := math.Float32bits(1013.25)
	b.input[1] = uint16(ibits)
	b.input[2] = uint16(ibits >> 16)
	return b
}

func main() {
	listen := flag.String("listen", "127.0.0.1:1502", "Modbus TCP listen address")
	flag.Parse()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	bank := newBank()
	log.Printf("modbus simulator listening on %s", *listen)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go serve(conn, bank)
	}
}

func serve(conn net.Conn, bank *registerBank) {
	defer conn.Close()
	hdr := make([]byte, 7)
	for {
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		txID := binary.BigEndian.Uint16(hdr[0:2])
		proto := binary.BigEndian.Uint16(hdr[2:4])
		length := binary.BigEndian.Uint16(hdr[4:6])
		unit := hdr[6]
		if proto != 0 || length < 2 || length > 253 {
			return
		}
		pdu := make([]byte, length-1)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}
		resp := handlePDU(bank, pdu)
		if resp == nil {
			return
		}
		out := make([]byte, 7+len(resp))
		binary.BigEndian.PutUint16(out[0:2], txID)
		binary.BigEndian.PutUint16(out[2:4], 0)
		binary.BigEndian.PutUint16(out[4:6], uint16(len(resp)+1))
		out[6] = unit
		copy(out[7:], resp)
		if _, err := conn.Write(out); err != nil {
			return
		}
	}
}

func exception(fc, code byte) []byte { return []byte{fc | 0x80, code} }

func handlePDU(bank *registerBank, pdu []byte) []byte {
	if len(pdu) < 5 {
		return exception(pdu[0], 3)
	}
	fc := pdu[0]
	address := int(binary.BigEndian.Uint16(pdu[1:3]))
	count := int(binary.BigEndian.Uint16(pdu[3:5]))
	if fc != 3 && fc != 4 {
		return exception(fc, 1)
	}
	if count < 1 || count > 125 {
		return exception(fc, 3)
	}
	if address+count > 65536 {
		return exception(fc, 2)
	}
	regs := bank.holding
	if fc == 4 {
		regs = bank.input
	}
	resp := make([]byte, 2+count*2)
	resp[0] = fc
	resp[1] = byte(count * 2)
	for i := 0; i < count; i++ {
		binary.BigEndian.PutUint16(resp[2+i*2:], regs[address+i])
	}
	return resp
}
