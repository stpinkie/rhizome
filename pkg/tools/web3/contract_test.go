// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3tools

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/tools"
	"github.com/stpinkie/rhizome/pkg/web3"
)

const (
	testTokenAddr = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	testNFTAddr   = "0xBC4CA0EdA7647A8aB7C2061c2E118A18a936f13D"
	testTokenABI  = `[
		{"type":"function","name":"decimals","stateMutability":"view","inputs":[],"outputs":[{"type":"uint8"}]},
		{"type":"function","name":"balanceOf","stateMutability":"view",
		 "inputs":[{"name":"account","type":"address"}],"outputs":[{"type":"uint256"}]},
		{"type":"function","name":"transfer","stateMutability":"nonpayable",
		 "inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],
		 "outputs":[{"type":"bool"}]}
	]`
)

// word encodes an integer as one 0x-prefixed 32-byte word.
func word(n int64) string {
	return fmt.Sprintf("0x%064x", n)
}

// selectorOf returns the 4-byte selector for "name" in abi.
func selectorOf(t *testing.T, abi *web3.ABI, name string, arity int) string {
	t.Helper()
	m, err := abi.Method(name, arity)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(m.Selector())
}

// selectorRPC answers eth_call by the calldata selector; results maps a
// selector hex (no 0x) to the hex return payload.
func selectorRPC(t *testing.T, chainID string, results map[string]string) *httptest.Server {
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
		switch req.Method {
		case "eth_chainId":
			resp["result"] = chainID
		case "eth_call":
			var raw []json.RawMessage
			var call struct {
				Data string `json:"data"`
			}
			if json.Unmarshal(req.Params, &raw) != nil || len(raw) == 0 ||
				json.Unmarshal(raw[0], &call) != nil || len(call.Data) < 10 {
				resp["error"] = map[string]any{"code": -32602, "message": "bad call"}
				break
			}
			sel := call.Data[2:10]
			if v, ok := results[sel]; ok {
				resp["result"] = v
			} else {
				resp["result"] = "0x"
			}
		default:
			resp["error"] = map[string]any{"code": -32601, "message": "unexpected " + req.Method}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func contractToolByName(t *testing.T, deps *SigningDeps, name string) tools.Tool {
	t.Helper()
	return toolByName(t, ContractTools(deps), name)
}

// testContract builds a SigningDeps with a selector-aware endpoint and a
// registered "usdc" label bound to testTokenAddr.
func testContract(t *testing.T, chainID string,
	results map[string]string,
) (*SigningDeps, *web3.SigningStack) {
	t.Helper()
	deps, stack := testSigning(t, nil)
	srv := selectorRPC(t, chainID, results)
	t.Cleanup(srv.Close)
	deps.Provider = web3.NewStaticProvider(srv.URL, "")
	if _, err := stack.Registry.Add("usdc", testTokenABI, testTokenAddr, nil); err != nil {
		t.Fatal(err)
	}
	return deps, stack
}

func TestContractCallDecodesOutput(t *testing.T) {
	deps, _ := testContract(t, "0x1", map[string]string{
		selectorOf(t, erc20ABI, "balanceOf", 1): word(5_000_000),
	})
	tool := contractToolByName(t, deps, "web3_contract_call")
	out := runTool(t, tool, map[string]any{
		"contract": "usdc", "method": "balanceOf", "args": []any{testAddr},
	})
	if out["method"] != "balanceOf(address)" {
		t.Fatalf("method = %v", out["method"])
	}
	outs, _ := out["outputs"].([]any)
	if len(outs) != 1 || outs[0] != "5000000" {
		t.Fatalf("outputs = %v", out["outputs"])
	}
}

func TestContractCallNoABI(t *testing.T) {
	deps, _ := testSigning(t, nil)
	srv := selectorRPC(t, "0x1", nil)
	t.Cleanup(srv.Close)
	deps.Provider = web3.NewStaticProvider(srv.URL, "")
	tool := contractToolByName(t, deps, "web3_contract_call")
	expectErr(t, tool, map[string]any{
		"contract": testAddr, "method": "balanceOf", "args": []any{testAddr},
	}, "no ABI")
}

func TestContractCallRejectsWriteMethod(t *testing.T) {
	deps, _ := testContract(t, "0x1", nil)
	tool := contractToolByName(t, deps, "web3_contract_call")
	expectErr(t, tool, map[string]any{
		"contract": "usdc", "method": "transfer",
		"args": []any{testAddr, "5"},
	}, "not read-only")
}

func TestContractSendQueuesPending(t *testing.T) {
	deps, stack := testContract(t, "0xaa36a7", nil)
	tool := contractToolByName(t, deps, "web3_contract_send")
	out := runTool(t, tool, map[string]any{
		"contract": "usdc", "method": "transfer",
		"args": []any{testAddr, "5000000"}, "reason": "pay bob",
	})
	id, _ := out["pending_id"].(string)
	e, err := stack.Pending.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != web3.KindContract || e.To != testTokenAddr ||
		e.Selector != "0xa9059cbb" || e.ChainID != 11155111 {
		t.Fatalf("entry = %+v", e)
	}
	if !strings.Contains(e.Summary, "usdc") || !strings.Contains(e.Summary, "transfer") ||
		!strings.Contains(e.Summary, "pay bob") {
		t.Fatalf("summary = %q", e.Summary)
	}
	if !strings.HasPrefix(e.Data, "0xa9059cbb") {
		t.Fatalf("data = %s", e.Data)
	}
}

func TestContractSendPolicyDenies(t *testing.T) {
	deps, stack := testContract(t, "0x1", nil)
	stack.Policy.AllowMethods = []string{"usdc:balanceOf"} // read method only
	tool := contractToolByName(t, deps, "web3_contract_send")
	expectErr(t, tool, map[string]any{
		"contract": "usdc", "method": "transfer", "args": []any{testAddr, "5"},
	}, "allow_methods")
	entries, _ := stack.Pending.List()
	if len(entries) != 0 {
		t.Fatal("denied contract send must not queue")
	}
}

func TestContractSendAllowsLabelMethod(t *testing.T) {
	deps, stack := testContract(t, "0x1", nil)
	stack.Policy.AllowContracts = []string{"usdc"}
	stack.Policy.AllowMethods = []string{"usdc:transfer"}
	tool := contractToolByName(t, deps, "web3_contract_send")
	out := runTool(t, tool, map[string]any{
		"contract": "usdc", "method": "transfer", "args": []any{testAddr, "5"},
	})
	if out["status"] != "pending" {
		t.Fatalf("result = %v", out)
	}
}

func TestERC20Balance(t *testing.T) {
	deps, _ := testContract(t, "0x1", map[string]string{
		selectorOf(t, erc20ABI, "balanceOf", 1): word(1_234_567),
	})
	tool := contractToolByName(t, deps, "web3_erc20")
	out := runTool(t, tool, map[string]any{
		"action": "balance", "token": "usdc", "owner": testAddr,
	})
	if out["balance_units"] != "1234567" {
		t.Fatalf("out = %v", out)
	}
}

func TestERC20TransferDecimalsAware(t *testing.T) {
	deps, stack := testContract(t, "0x1", map[string]string{
		selectorOf(t, erc20ABI, "decimals", 0): word(6),
	})
	tool := contractToolByName(t, deps, "web3_erc20")
	out := runTool(t, tool, map[string]any{
		"action": "transfer", "token": "usdc", "to": testAddr, "amount": "5.25",
	})
	e, err := stack.Pending.Get(out["pending_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	// 5.25 USDC × 10^6 = 5250000 = 0x501BD0 → last word must end with it.
	if !strings.HasPrefix(e.Data, "0xa9059cbb") ||
		!strings.HasSuffix(e.Data, "501bd0") {
		t.Fatalf("data = %s", e.Data)
	}
}

func TestERC20ApproveUnlimited(t *testing.T) {
	deps, stack := testContract(t, "0x1", nil)
	tool := contractToolByName(t, deps, "web3_erc20")
	out := runTool(t, tool, map[string]any{
		"action": "approve", "token": "usdc",
		"spender": testAddr, "amount": "unlimited",
	})
	e, err := stack.Pending.Get(out["pending_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.Data, "0x095ea7b3") ||
		!strings.HasSuffix(e.Data, strings.Repeat("f", 64)) {
		t.Fatalf("data = %s", e.Data)
	}
}

func TestERC20WriteNeedsSigning(t *testing.T) {
	deps, stack := testContract(t, "0x1", nil)
	stack.Policy.Enabled = false
	tool := contractToolByName(t, deps, "web3_erc20")
	expectErr(t, tool, map[string]any{
		"action": "transfer", "token": "usdc", "to": testAddr, "amount": "1",
	}, "signing")
}

func TestERC721OwnerOf(t *testing.T) {
	deps, _ := testContract(t, "0x1", map[string]string{
		selectorOf(t, erc721ABI, "ownerOf", 1): word(0), // overwritten below
	})
	// ownerOf returns an address — rebuild the word with a real address.
	owner := "0x00000000000000000000000000000000000000ab"
	srv := selectorRPC(t, "0x1", map[string]string{
		selectorOf(t, erc721ABI, "ownerOf", 1): "0x" + strings.Repeat("0", 24) + owner[2:],
	})
	t.Cleanup(srv.Close)
	deps.Provider = web3.NewStaticProvider(srv.URL, "")
	tool := contractToolByName(t, deps, "web3_erc721")
	out := runTool(t, tool, map[string]any{
		"action": "ownerOf", "token": testNFTAddr, "token_id": "1234",
	})
	if out["owner"] != "0x00000000000000000000000000000000000000aB" &&
		!strings.EqualFold(out["owner"].(string), owner) {
		t.Fatalf("owner = %v", out["owner"])
	}
}

func TestERC721TransferQueuesPending(t *testing.T) {
	deps, stack := testContract(t, "0x1", nil)
	tool := contractToolByName(t, deps, "web3_erc721")
	out := runTool(t, tool, map[string]any{
		"action": "transferFrom", "token": testNFTAddr,
		"to": testAddr, "token_id": "1234",
	})
	e, err := stack.Pending.Get(out["pending_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != web3.KindContract || e.Selector != "0x23b872dd" || e.To != testNFTAddr {
		t.Fatalf("entry = %+v", e)
	}
}

func TestENSToolForward(t *testing.T) {
	deps, _ := testSigning(t, nil)
	// Registry→resolver→addr: resolver address is returned for the registry
	// call, resolved address for the resolver call — selector-blind mock is
	// fine because the two calls go to different `to` addresses.
	resolver := "0x00000000000000000000000000000000000000aa"
	want := "0x0000000000000000000000000000000000000bed"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     uint64          `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if req.Method == "eth_chainId" {
			resp["result"] = "0x1"
		} else {
			var raw []json.RawMessage
			var call struct {
				To string `json:"to"`
			}
			_ = json.Unmarshal(req.Params, &raw)
			_ = json.Unmarshal(raw[0], &call)
			if strings.EqualFold(call.To, "0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e") {
				resp["result"] = "0x" + strings.Repeat("0", 24) + resolver[2:]
			} else {
				resp["result"] = "0x" + strings.Repeat("0", 24) + want[2:]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	deps.Provider = web3.NewStaticProvider(srv.URL, "")
	tool := contractToolByName(t, deps, "web3_ens")
	out := runTool(t, tool, map[string]any{"name": "alice.eth"})
	if !strings.EqualFold(out["address"].(string), want) {
		t.Fatalf("address = %v", out["address"])
	}
}

func TestENSToolUnsupportedChain(t *testing.T) {
	deps, _ := testSigning(t, nil)
	srv := scriptedRPC(t, map[string]any{"eth_chainId": "0x2105"})
	t.Cleanup(srv.Close)
	deps.Provider = web3.NewStaticProvider(srv.URL, "")
	tool := contractToolByName(t, deps, "web3_ens")
	expectErr(t, tool, map[string]any{"name": "alice.eth"}, "not supported")
}
