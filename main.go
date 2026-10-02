package main

import (
	"flag"
	"log"
	"net/http"

	"modbus-backend/api"
	"modbus-backend/collect"
	"modbus-backend/config"
	"modbus-backend/sim"
)

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen address")
	configPath := flag.String("config", "config.json", "config persistence file")
	maxParallel := flag.Int("max-parallel", 8, "max devices read concurrently")
	withSim := flag.Bool("with-sim", true, "also start a local demo Modbus device on :1502")
	flag.Parse()

	store, err := config.LoadStore(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if *withSim {
		holding := make([]uint16, 256)
		input := make([]uint16, 256)
		// demo values: holding[0]=2345 (u16), holding[1]=0xFF38 i16=-200,
		// holding[10..11]=f32 3.14159 high-first, holding[12..13]=f32 2.5 low-first
		holding[0] = 2345
		holding[1] = 0xFF38
		holding[10] = 0x4049
		holding[11] = 0x0FD0
		holding[12] = 0x0000
		holding[13] = 0x4020
		input[0] = 100
		dev, err := sim.New(":1502", 1, holding, input)
		if err != nil {
			log.Fatalf("start sim device: %v", err)
		}
		defer dev.Close()
		log.Printf("demo modbus device on %s (unit 1)", dev.Addr())
	}

	srv := api.NewServer(store, collect.New(*maxParallel))
	log.Printf("http listening on %s (config file %s)", *listen, *configPath)
	log.Fatal(http.ListenAndServe(*listen, srv.Router()))
}
