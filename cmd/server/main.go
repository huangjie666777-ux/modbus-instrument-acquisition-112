package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"example.com/modbus-instrument-acquisition/internal/config"
	"example.com/modbus-instrument-acquisition/internal/httpapi"
	"example.com/modbus-instrument-acquisition/internal/service"
)

func main() {
	addr := flag.String("listen", ":8080", "HTTP listen address")
	dataFile := flag.String("data", "data/config.json", "persistence file path")
	concurrency := flag.Int("concurrency", 8, "max devices read concurrently")
	flag.Parse()

	store, err := config.LoadStore(*dataFile)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	svc := service.New(store, *concurrency)
	srv := &http.Server{
		Addr:              *addr,
		Handler:           httpapi.NewRouter(store, svc),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("modbus acquisition backend listening on %s (version %d)", *addr, store.Version())
	log.Fatal(srv.ListenAndServe())
}
