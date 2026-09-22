// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// EIP-137 namehash vectors.
func TestENSNamehash(t *testing.T) {
	cases := map[string]string{
		"":         "0000000000000000000000000000000000000000000000000000000000000000",
		"eth":      "93cdeb708b7545dc668eb9280176169d1c33cfd8ed6f04690a0bcc88a93fc4ae",
		"foo.eth":  "de9b09fd7c5f901e23a3f19fecc54828e9c848539801e86591bd9801b019f84f",
		"FOO.eth.": "de9b09fd7c5f901e23a3f19fecc54828e9c848539801e86591bd9801b019f84f",
	}
	for name, want := range cases {
		got := hex.EncodeToString(ENSNamehash(name))
		if got != want {
			t.Fatalf("namehash(%q) = %s, want %s", name, got, want)
		}
	}
}

const (
	testResolverAddr = "0x00000000000000000000000000000000000000aa"
	testResolvedAddr = "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"
)

// abiWord packs a value into one 32-byte 0x word (addresses, uints).
func abiWord(v string) string {
	return "0x" + strings.Repeat("0", 64-len(strings.TrimPrefix(v, "0x"))) +
		strings.TrimPrefix(v, "0x")
}

// abiString encodes a dynamic-string return: offset + length + padded data.
func abiString(s string) string {
	data := hex.EncodeToString([]byte(s))
	if pad := len(data) % 64; pad != 0 {
		data += strings.Repeat("0", 64-pad)
	}
	return fmt.Sprintf("0x%064x%064x%s", 32, len(s), data)
}

// ensServer answers the registry→resolver→record flow; chainID is the
// endpoint's eth_chainId result.
func ensServer(t *testing.T, chainID string) *Provider {
	srv := rpcServer(t, func(method string, params json.RawMessage, _ uint64) (any, *RPCError) {
		switch method {
		case "eth_chainId":
			return chainID, nil
		case "eth_call":
			var raw []json.RawMessage
			if err := json.Unmarshal(params, &raw); err != nil || len(raw) == 0 {
				return nil, &RPCError{Code: -32602, Message: "bad params"}
			}
			var call struct {
				To   string `json:"to"`
				Data string `json:"data"`
			}
			if err := json.Unmarshal(raw[0], &call); err != nil {
				return nil, &RPCError{Code: -32602, Message: "bad params"}
			}
			to := strings.ToLower(call.To)
			sel := call.Data[:10]
			switch {
			case to == strings.ToLower(ensRegistryAddr):
				// resolver(node) → resolver address
				return abiWord(testResolverAddr), nil
			case to == testResolverAddr && sel == methodSel(t, "addr"):
				return abiWord(testResolvedAddr), nil
			case to == testResolverAddr && sel == methodSel(t, "name"):
				return abiString("alice.eth"), nil
			}
			return "0x", nil
		}
		return nil, &RPCError{Code: -32601, Message: "unexpected " + method}
	})
	t.Cleanup(srv.Close)
	return NewStaticProvider(srv.URL, "")
}

func methodSel(t *testing.T, name string) string {
	m, err := ensABI.Method(name, 1)
	if err != nil {
		t.Fatal(err)
	}
	return "0x" + hex.EncodeToString(m.Selector())
}

func TestENSForwardResolve(t *testing.T) {
	p := ensServer(t, "0x1")
	addr, err := ENSResolve(context.Background(), p, "alice.eth")
	if err != nil {
		t.Fatal(err)
	}
	if addr != testResolvedAddr {
		t.Fatalf("resolved %s, want %s", addr, testResolvedAddr)
	}
}

func TestENSReverseResolve(t *testing.T) {
	p := ensServer(t, "0x1")
	name, err := ENSReverse(context.Background(), p, testResolvedAddr)
	if err != nil {
		t.Fatal(err)
	}
	if name != "alice.eth" {
		t.Fatalf("reverse = %q", name)
	}
}

func TestENSUnsupportedChain(t *testing.T) {
	p := ensServer(t, "0x2105") // base
	if _, err := ENSResolve(context.Background(), p, "alice.eth"); err == nil ||
		!strings.Contains(err.Error(), "not supported") {
		t.Fatalf("want unsupported-chain error, got %v", err)
	}
	if _, err := ENSReverse(context.Background(), p, testResolvedAddr); err == nil {
		t.Fatal("reverse on unsupported chain should fail")
	}
}

func TestENSNoResolver(t *testing.T) {
	srv := rpcServer(t, func(method string, _ json.RawMessage, _ uint64) (any, *RPCError) {
		if method == "eth_chainId" {
			return "0x1", nil
		}
		return abiWord("0x0"), nil // registry answers zero resolver
	})
	defer srv.Close()
	p := NewStaticProvider(srv.URL, "")
	if _, err := ENSResolve(context.Background(), p, "ghost.eth"); err == nil ||
		!strings.Contains(err.Error(), "no resolver") {
		t.Fatalf("want no-resolver error, got %v", err)
	}
}

func TestENSMalformedResponse(t *testing.T) {
	srv := rpcServer(t, func(method string, _ json.RawMessage, _ uint64) (any, *RPCError) {
		if method == "eth_chainId" {
			return "0x1", nil
		}
		return "0xnothex", nil
	})
	defer srv.Close()
	p := NewStaticProvider(srv.URL, "")
	if _, err := ENSResolve(context.Background(), p, "x.eth"); err == nil {
		t.Fatal("malformed resolver response should error")
	}
}
