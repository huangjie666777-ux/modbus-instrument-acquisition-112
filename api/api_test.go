package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"modbus-backend/collect"
	"modbus-backend/config"
	"modbus-backend/sim"
)

func newTestServer(t *testing.T) (*httptest.Server, *sim.Device) {
	t.Helper()
	holding := make([]uint16, 64)
	holding[0] = 1234
	dev, err := sim.New("127.0.0.1:0", 1, holding, make([]uint16, 64))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dev.Close() })
	store, err := config.LoadStore(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewServer(store, collect.New(4)).Router())
	t.Cleanup(srv.Close)
	return srv, dev
}

func TestConfigAndCollectFlow(t *testing.T) {
	srv, dev := newTestServer(t)
	cfg := `{"devices":[{"id":"m1","address":"` + dev.Addr() + `","unit_id":1,"timeout_ms":1000}],
"points":[{"device_id":"m1","name":"v","func_code":3,"address":0,"type":"u16","scale":0.1,"offset":0}]}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/config", strings.NewReader(cfg))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("put config status %d", resp.StatusCode)
	}
	var saved config.Config
	json.NewDecoder(resp.Body).Decode(&saved)
	resp.Body.Close()
	if saved.Version != 1 {
		t.Fatalf("version = %d", saved.Version)
	}

	// invalid config rejected, version unchanged
	bad := `{"devices":[],"points":[]}`
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/api/config", strings.NewReader(bad))
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 400 {
		t.Fatalf("bad config status %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp, _ = http.Get(srv.URL + "/api/config")
	var cur config.Config
	json.NewDecoder(resp.Body).Decode(&cur)
	resp.Body.Close()
	if cur.Version != 1 {
		t.Fatalf("version changed after failed update: %d", cur.Version)
	}

	// collect
	resp, _ = http.Post(srv.URL+"/api/collect", "application/json",
		strings.NewReader(`{"names":["v","unknown"]}`))
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("collect status %d", resp.StatusCode)
	}
	var res collect.Result
	json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()
	if res.Version != 1 {
		t.Fatalf("result version = %d", res.Version)
	}
	if !res.Results[0].OK || res.Results[0].Raw != 1234 || res.Results[0].Engineering != 123.4 {
		t.Fatalf("bad result: %+v", res.Results[0])
	}
	if res.Results[1].OK {
		t.Fatalf("unknown point should fail: %+v", res.Results[1])
	}
}
