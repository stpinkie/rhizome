// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
)

const watchContract = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"

// watchRPC serves eth_blockNumber + eth_getLogs; logsReqs records each
// filter the poller sent.
type watchRPC struct {
	head     uint64
	logs     []map[string]any
	logsErr  *RPCError
	mu       sync.Mutex
	logsReqs []map[string]any
}

func (w *watchRPC) handler(method string, params json.RawMessage, _ uint64) (any, *RPCError) {
	switch method {
	case "eth_blockNumber":
		return fmt.Sprintf("0x%x", w.head), nil
	case "eth_getLogs":
		var arr []json.RawMessage
		_ = json.Unmarshal(params, &arr)
		if len(arr) > 0 {
			var f map[string]any
			_ = json.Unmarshal(arr[0], &f)
			w.mu.Lock()
			w.logsReqs = append(w.logsReqs, f)
			w.mu.Unlock()
		}
		if w.logsErr != nil {
			return nil, w.logsErr
		}
		return w.logs, nil
	}
	return nil, &RPCError{Code: -32601, Message: "method not found"}
}

func testLog(block uint64) map[string]any {
	return map[string]any{
		"address":         watchContract,
		"topics":          []string{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"},
		"data":            "0x00000000000000000000000000000000000000000000000000000000000f4240",
		"blockNumber":     fmt.Sprintf("0x%x", block),
		"transactionHash": "0x" + fmt.Sprintf("%064x", block),
		"logIndex":        "0x0",
	}
}

func newWatchRunner(t *testing.T, rpc *watchRPC, watches []config.Web3WatchConfig,
	confDepth uint64,
) (*WatchRunner, *[]map[string]any) {
	t.Helper()
	srv := rpcServer(t, rpc.handler)
	t.Cleanup(srv.Close)
	var emitted []map[string]any
	r := NewWatchRunner(
		NewStaticProvider(srv.URL, ""), nil, watches,
		t.TempDir(), 0, confDepth,
		func(kind string, attrs map[string]any) {
			attrs["kind"] = kind
			emitted = append(emitted, attrs)
		})
	return r, &emitted
}

func TestWatchRunner_Validation(t *testing.T) {
	good := config.Web3WatchConfig{Name: "ok", Contract: watchContract}
	cases := []struct {
		name    string
		watches []config.Web3WatchConfig
		want    int
	}{
		{"empty name", []config.Web3WatchConfig{{Contract: watchContract}}, 0},
		{"empty contract", []config.Web3WatchConfig{{Name: "x"}}, 0},
		{"bad topic", []config.Web3WatchConfig{
			{Name: "x", Contract: watchContract, Topics: []string{"0x1234"}},
		}, 0},
		{"good", []config.Web3WatchConfig{good}, 1},
		{"cap", []config.Web3WatchConfig{
			good, good, good, good, good, good, good, good, good, good,
		}, watchMaxCount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewWatchRunner(nil, nil, tc.watches, t.TempDir(), 0, 0, nil)
			if r.Len() != tc.want {
				t.Fatalf("Len() = %d, want %d", r.Len(), tc.want)
			}
		})
	}
}

func TestWatchRunner_PollEmitsAndAdvancesCursor(t *testing.T) {
	rpc := &watchRPC{head: 100, logs: []map[string]any{testLog(99)}}
	r, emitted := newWatchRunner(t, rpc, []config.Web3WatchConfig{
		{Name: "usdc", Contract: watchContract},
	}, 0)
	r.resolved["usdc"] = watchContract

	r.pollWatch(context.Background(), &r.watches[0])

	if len(*emitted) != 1 {
		t.Fatalf("emitted %d events, want 1", len(*emitted))
	}
	ev := (*emitted)[0]
	if ev["kind"] != "web3.event" || ev["watch"] != "usdc" {
		t.Fatalf("event attrs = %v", ev)
	}
	if ev["block"] != uint64(99) || ev["index"] != uint64(0) {
		t.Fatalf("event block/index = %v/%v", ev["block"], ev["index"])
	}
	if r.last["usdc"] != 100 {
		t.Fatalf("cursor = %d, want head 100", r.last["usdc"])
	}
	if len(rpc.logsReqs) != 1 {
		t.Fatalf("getLogs calls = %d", len(rpc.logsReqs))
	}
	f := rpc.logsReqs[0]
	// First run scans only the bounded lookback, not all history
	// (head 100 < 500 → fromBlock 1).
	if f["fromBlock"] != "0x1" {
		t.Fatalf("fromBlock = %v, want 0x1", f["fromBlock"])
	}
	if f["toBlock"] != "0x64" {
		t.Fatalf("toBlock = %v, want 0x64", f["toBlock"])
	}
	if f["address"] != watchContract {
		t.Fatalf("address = %v", f["address"])
	}

	// Second poll (head advanced) continues from last+1.
	rpc.head = 200
	r.pollWatch(context.Background(), &r.watches[0])
	if len(rpc.logsReqs) != 2 {
		t.Fatalf("getLogs calls = %d, want 2", len(rpc.logsReqs))
	}
	if rpc.logsReqs[1]["fromBlock"] != "0x65" {
		t.Fatalf("second fromBlock = %v, want 0x65", rpc.logsReqs[1]["fromBlock"])
	}
}

func TestWatchRunner_ConfirmationDepth(t *testing.T) {
	rpc := &watchRPC{head: 105, logs: []map[string]any{testLog(104)}}
	r, emitted := newWatchRunner(t, rpc, []config.Web3WatchConfig{
		{Name: "w", Contract: watchContract},
	}, 10)
	r.resolved["w"] = watchContract

	r.pollWatch(context.Background(), &r.watches[0])
	if len(*emitted) != 1 {
		t.Fatalf("emitted %d, want 1", len(*emitted))
	}
	// toBlock must be head-10 = 95.
	if rpc.logsReqs[0]["toBlock"] != "0x5f" {
		t.Fatalf("toBlock = %v, want 0x5f", rpc.logsReqs[0]["toBlock"])
	}
	if r.last["w"] != 95 {
		t.Fatalf("cursor = %d, want 95", r.last["w"])
	}

	// Head lower than confDepth → no poll at all.
	rpc2 := &watchRPC{head: 5, logs: []map[string]any{testLog(4)}}
	r2, emitted2 := newWatchRunner(t, rpc2, []config.Web3WatchConfig{
		{Name: "w", Contract: watchContract},
	}, 10)
	r2.resolved["w"] = watchContract
	r2.pollWatch(context.Background(), &r2.watches[0])
	if len(*emitted2) != 0 || len(rpc2.logsReqs) != 0 {
		t.Fatalf("should not poll below confirmation depth")
	}
}

func TestWatchRunner_RunResolvesEmitsAndSavesState(t *testing.T) {
	rpc := &watchRPC{head: 60, logs: []map[string]any{testLog(58)}}
	srv := rpcServer(t, rpc.handler)
	defer srv.Close()

	dir := t.TempDir()
	got := make(chan map[string]any, 4)
	r := NewWatchRunner(
		NewStaticProvider(srv.URL, ""), nil,
		[]config.Web3WatchConfig{{Name: "w", Contract: watchContract}},
		dir, 0, 0,
		func(kind string, attrs map[string]any) {
			attrs["kind"] = kind
			got <- attrs
		})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	select {
	case ev := <-got:
		if ev["kind"] != "web3.event" || ev["watch"] != "w" {
			t.Fatalf("event = %v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no web3.event emitted")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
	if _, err := os.Stat(filepath.Join(dir, watchStateFile)); err != nil {
		t.Fatalf("state file not persisted: %v", err)
	}
}
