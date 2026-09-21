// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/tools"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// scriptedRPC answers JSON-RPC methods from a map; each value is the raw
// result payload. Unknown methods return a -32601 RPC error.
func scriptedRPC(t *testing.T, responses map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     uint64          `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if v, ok := responses[req.Method]; ok {
			resp["result"] = v
		} else {
			resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func toolByName(t *testing.T, list []tools.Tool, name string) tools.Tool {
	t.Helper()
	for _, tool := range list {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %q not registered", name)
	return nil
}

func runTool(t *testing.T, tool tools.Tool, args map[string]any) map[string]any {
	t.Helper()
	res := tool.Execute(context.Background(), args)
	if res.IsError {
		t.Fatalf("%s: %s", tool.Name(), res.ForLLM)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("%s result not JSON: %v (%s)", tool.Name(), err, res.ForLLM)
	}
	return out
}

func expectErr(t *testing.T, tool tools.Tool, args map[string]any, substr string) {
	t.Helper()
	res := tool.Execute(context.Background(), args)
	if !res.IsError {
		t.Fatalf("%s(%v): expected error, got %s", tool.Name(), args, res.ForLLM)
	}
	if substr != "" && !strings.Contains(res.ForLLM, substr) {
		t.Fatalf("%s: error %q missing %q", tool.Name(), res.ForLLM, substr)
	}
}

func testTools(t *testing.T, responses map[string]any) ([]tools.Tool, *config.Web3ToolsConfig) {
	t.Helper()
	srv := scriptedRPC(t, responses)
	t.Cleanup(srv.Close)
	cfg := &config.Web3ToolsConfig{MaxLogRange: 10000}
	return Tools(web3.NewStaticProvider(srv.URL, ""), cfg), cfg
}

const (
	testAddr = "0xde0B295669a9FD93d5F28D9Ec85E40f4cb697BAe"
	testHash = "0x881de7d386f6e90111b157d513b81b496d5297aaa6e70771b2c44d4fbebfa61a"
)

func TestWeb3Chain(t *testing.T) {
	list, _ := testTools(t, map[string]any{
		"eth_chainId":              "0x1",
		"eth_blockNumber":          "0x18d077c",
		"eth_gasPrice":             "0x3b9aca00",
		"eth_maxPriorityFeePerGas": "0x5f5e100",
		"eth_syncing":              false,
	})
	out := runTool(t, toolByName(t, list, "web3_chain"), nil)
	if out["chain_id"] != "0x1" || out["chain_id_dec"].(float64) != 1 {
		t.Fatalf("chain_id = %v", out["chain_id"])
	}
	if out["network"] != "ethereum-mainnet" {
		t.Fatalf("network = %v", out["network"])
	}
	if out["block_number"].(float64) != 26019708 {
		t.Fatalf("block_number = %v", out["block_number"])
	}
	if out["syncing"] != false {
		t.Fatalf("syncing = %v", out["syncing"])
	}
	if out["endpoint_source"] == "" {
		t.Fatal("endpoint_source missing")
	}
}

func TestWeb3Balance(t *testing.T) {
	list, _ := testTools(t, map[string]any{
		"eth_getBalance": "0xde0b6b3a7640000", // 1 ether
	})
	tool := toolByName(t, list, "web3_balance")
	out := runTool(t, tool, map[string]any{"address": testAddr})
	if out["ether"] != "1" || out["wei"] != "0xde0b6b3a7640000" {
		t.Fatalf("balance = %v", out)
	}
	expectErr(t, tool, map[string]any{"address": "not-an-address"}, "address")
	expectErr(t, tool, map[string]any{}, "address")
	expectErr(t, tool, map[string]any{"address": testAddr, "block": "bogus"}, "block")
}

func TestWeb3Call(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params []map[string]any `json:"params"`
			ID     uint64           `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		captured = req.Params[0]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID, "result": "0x0001",
		})
	}))
	defer srv.Close()

	cfg := &config.Web3ToolsConfig{}
	tool := toolByName(t,
		Tools(web3.NewStaticProvider(srv.URL, ""), cfg), "web3_call")
	out := runTool(t, tool, map[string]any{
		"to": testAddr, "data": "0xa9059cbb", "from": testAddr, "value": "1000000000000000000",
	})
	if out["return_data"] != "0x0001" {
		t.Fatalf("return_data = %v", out)
	}
	if captured["to"] != testAddr || captured["value"] != "0xde0b6b3a7640000" {
		t.Fatalf("call params = %v", captured)
	}

	expectErr(t, tool, map[string]any{"to": "bad", "data": "0x"}, "to")
	expectErr(t, tool, map[string]any{"to": testAddr, "data": "xyz"}, "data")
	expectErr(t, tool, map[string]any{"to": testAddr}, "data")
	expectErr(t, tool, map[string]any{"to": testAddr, "data": "0x", "value": "-1"}, "value")
	expectErr(t, tool, map[string]any{"to": testAddr, "data": "0x", "value": "0x-1"}, "value")
}

func TestWeb3Block(t *testing.T) {
	list, _ := testTools(t, map[string]any{
		"eth_getBlockByNumber": map[string]any{
			"number": "0x10", "hash": "0xabc", "parentHash": "0xdef",
			"timestamp": "0x5f5e100", "gasUsed": "0x5208", "gasLimit": "0x1c9c380",
			"transactions": []any{"0x111", "0x222"},
		},
	})
	out := runTool(t, toolByName(t, list, "web3_block"), nil)
	blk := out["block"].(map[string]any)
	if blk["number"].(float64) != 16 || blk["transaction_count"].(float64) != 2 {
		t.Fatalf("block = %v", blk)
	}

	// Not found → error.
	list2, _ := testTools(t, map[string]any{"eth_getBlockByNumber": nil})
	res := toolByName(t, list2, "web3_block").Execute(context.Background(), nil)
	if !res.IsError {
		t.Fatal("expected not-found error")
	}
}

func TestWeb3Transaction(t *testing.T) {
	list, _ := testTools(t, map[string]any{
		"eth_getTransactionByHash": map[string]any{
			"hash": testHash, "from": testAddr, "to": testAddr,
			"value": "0xde0b6b3a7640000", "nonce": "0x7", "gas": "0x5208",
			"gasPrice": "0x3b9aca00", "blockNumber": "0x10", "input": "0x",
		},
		"eth_getTransactionReceipt": map[string]any{
			"status": "0x1", "gasUsed": "0x5208", "blockNumber": "0x10",
			"effectiveGasPrice": "0x3b9aca00", "logs": []any{},
		},
	})
	tool := toolByName(t, list, "web3_transaction")
	out := runTool(t, tool, map[string]any{"hash": testHash})
	if out["status"] != "success" {
		t.Fatalf("status = %v", out["status"])
	}
	tx := out["transaction"].(map[string]any)
	if tx["value_ether"] != "1" {
		t.Fatalf("value_ether = %v", tx["value_ether"])
	}
	expectErr(t, tool, map[string]any{"hash": "0xshort"}, "hash")

	// Pending: null receipt → status pending.
	list2, _ := testTools(t, map[string]any{
		"eth_getTransactionByHash": map[string]any{
			"hash": testHash, "from": testAddr, "to": testAddr,
			"value": "0x0", "nonce": "0x0", "gas": "0x5208",
		},
		"eth_getTransactionReceipt": nil,
	})
	out2 := runTool(t, toolByName(t, list2, "web3_transaction"), map[string]any{"hash": testHash})
	if out2["status"] != "pending" {
		t.Fatalf("pending status = %v", out2["status"])
	}
}

func TestWeb3Logs(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string           `json:"method"`
			Params []map[string]any `json:"params"`
			ID     uint64           `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "eth_blockNumber":
			result = "0x1000" // tip 4096 — used to resolve tag bounds
		default: // eth_getLogs
			captured = req.Params[0]
			result = []any{map[string]any{
				"address": testAddr, "topics": []any{"0xaa"}, "data": "0x",
				"blockNumber": "0x10", "transactionHash": testHash, "logIndex": "0x0",
			}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID, "result": result,
		})
	}))
	defer srv.Close()

	cfg := &config.Web3ToolsConfig{MaxLogRange: 100}
	tool := toolByName(t, Tools(web3.NewStaticProvider(srv.URL, ""), cfg), "web3_logs")
	out := runTool(t, tool, map[string]any{
		"address": testAddr, "from_block": "10", "to_block": "20",
	})
	if out["count"].(float64) != 1 || out["truncated"] != false {
		t.Fatalf("logs = %v", out)
	}
	if captured["fromBlock"] != "0xa" || captured["toBlock"] != "0x14" {
		t.Fatalf("filter = %v", captured)
	}

	// Range cap enforced.
	expectErr(t, tool, map[string]any{
		"from_block": "0", "to_block": "500",
	}, "max_log_range")
	// Inverted range.
	expectErr(t, tool, map[string]any{
		"from_block": "20", "to_block": "10",
	}, "exceeds")
	// Tag bounds resolve to concrete numbers and are still capped:
	// latest..latest is a single block (tip 0x1000), earliest..latest is 4097.
	out = runTool(t, tool, map[string]any{"from_block": "latest", "to_block": "latest"})
	if out["count"].(float64) != 1 {
		t.Fatalf("logs = %v", out)
	}
	expectErr(t, tool, map[string]any{
		"from_block": "earliest", "to_block": "latest",
	}, "max_log_range")
	// Bad topic.
	expectErr(t, tool, map[string]any{
		"topics": []any{"0xshort"},
	}, "topics")
}

func TestWeb3RPC(t *testing.T) {
	list, _ := testTools(t, map[string]any{
		"web3_clientVersion": "nimbus-eth1-verified-proxy/v0.4.1",
	})
	tool := toolByName(t, list, "web3_rpc")
	out := runTool(t, tool, map[string]any{"method": "web3_clientVersion"})
	if out["result"] != "nimbus-eth1-verified-proxy/v0.4.1" {
		t.Fatalf("result = %v", out)
	}
	// Allowlist enforcement — no request reaches the server for denied methods.
	for _, denied := range []string{
		"eth_sendTransaction", "eth_sendRawTransaction", "eth_sign",
		"eth_accounts", "personal_sign", "debug_traceTransaction",
	} {
		expectErr(t, tool, map[string]any{"method": denied}, "allowlist")
	}
	expectErr(t, tool, map[string]any{}, "method")
}

func TestRegisterGating(t *testing.T) {
	srv := scriptedRPC(t, map[string]any{})
	defer srv.Close()
	p := web3.NewStaticProvider(srv.URL, "")
	cfg := &config.Web3ToolsConfig{}

	reg := tools.NewToolRegistry()
	Register(reg, p, cfg)
	want := []string{
		"web3_chain", "web3_balance", "web3_call", "web3_block",
		"web3_transaction", "web3_logs", "web3_rpc",
	}
	for _, name := range want {
		if !reg.HasRegistered(name) {
			t.Fatalf("tool %q not registered", name)
		}
	}
	if reg.Count() != len(want) {
		t.Fatalf("registered %d tools, want %d", reg.Count(), len(want))
	}

	// Nil safety.
	Register(nil, p, cfg)
	Register(reg, nil, cfg)
	Register(reg, p, nil)
}
