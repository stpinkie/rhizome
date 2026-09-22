// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveLiteralAddress(t *testing.T) {
	rc, err := ResolveContract(context.Background(), nil, nil,
		"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48")
	if err != nil {
		t.Fatal(err)
	}
	if rc.Source != "literal" || rc.Address != "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48" {
		t.Fatalf("resolved = %+v", rc)
	}
}

func TestResolveLiteralPicksUpRegisteredABI(t *testing.T) {
	reg := OpenABIRegistry(t.TempDir())
	if _, err := reg.Add("usdc", testERC20ishABI,
		"0xA0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", nil); err != nil {
		t.Fatal(err)
	}
	rc, err := ResolveContract(context.Background(), nil, reg,
		"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	if err != nil {
		t.Fatal(err)
	}
	if rc.ABI == nil || rc.Label != "usdc" {
		t.Fatalf("literal resolution should attach registered ABI: %+v", rc)
	}
}

func TestResolveRegistryLabel(t *testing.T) {
	reg := OpenABIRegistry(t.TempDir())
	if _, err := reg.Add("usdc", testERC20ishABI,
		"0xA0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", nil); err != nil {
		t.Fatal(err)
	}
	rc, err := ResolveContract(context.Background(), nil, reg, "usdc")
	if err != nil {
		t.Fatal(err)
	}
	if rc.Source != "registry" || rc.Label != "usdc" ||
		rc.Address != "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48" {
		t.Fatalf("resolved = %+v", rc)
	}
}

func TestResolveLabelWithoutAddress(t *testing.T) {
	reg := OpenABIRegistry(t.TempDir())
	if _, err := reg.Add("abi-only", testERC20ishABI, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveContract(context.Background(), nil, reg, "abi-only"); err == nil {
		t.Fatal("label without bound address should error")
	}
}

func TestResolveLabelChainGated(t *testing.T) {
	reg := OpenABIRegistry(t.TempDir())
	if _, err := reg.Add("usdc", testERC20ishABI,
		"0xA0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", []uint64{1}); err != nil {
		t.Fatal(err)
	}
	// Endpoint on sepolia — label is mainnet-only.
	srv := rpcServer(t, func(method string, _ json.RawMessage, _ uint64) (any, *RPCError) {
		return "0xaa36a7", nil // 11155111
	})
	defer srv.Close()
	p := NewStaticProvider(srv.URL, "")
	if _, err := ResolveContract(context.Background(), p, reg, "usdc"); err == nil {
		t.Fatal("chain-restricted label resolved on wrong chain")
	}
}

func TestResolveUnknownFallsToENS(t *testing.T) {
	srv := rpcServer(t, func(method string, _ json.RawMessage, _ uint64) (any, *RPCError) {
		if method == "eth_chainId" {
			return "0x5", nil // unsupported chain → ENS must fail clearly
		}
		return nil, &RPCError{Code: -32601, Message: "no"}
	})
	defer srv.Close()
	p := NewStaticProvider(srv.URL, "")
	_, err := ResolveContract(context.Background(), p, nil, "nothing.eth")
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected ENS unsupported-chain error, got %v", err)
	}
}

func TestResolveUnknownNoProvider(t *testing.T) {
	if _, err := ResolveContract(context.Background(), nil, nil, "nothing.eth"); err == nil {
		t.Fatal("unresolvable name without provider should error")
	}
}
