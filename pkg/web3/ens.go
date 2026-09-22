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
)

// ENS registry address — deployed at the same address on every chain that
// has it (mainnet, sepolia, holesky, hoodi).
const ensRegistryAddr = "0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e"

// ensChains lists chains with the canonical ENS registry deployment.
var ensChains = map[uint64]bool{
	1:        true, // mainnet
	11155111: true, // sepolia
	17000:    true, // holesky
	560048:   true, // hoodi
}

// ensABI is the minimal registry+resolver surface used for forward
// (addr(bytes32)) and reverse (name(bytes32)) lookups.
var ensABI = mustABI(`[
	{"type":"function","name":"resolver","stateMutability":"view",
	 "inputs":[{"name":"node","type":"bytes32"}],
	 "outputs":[{"name":"","type":"address"}]},
	{"type":"function","name":"addr","stateMutability":"view",
	 "inputs":[{"name":"node","type":"bytes32"}],
	 "outputs":[{"name":"","type":"address"}]},
	{"type":"function","name":"name","stateMutability":"view",
	 "inputs":[{"name":"node","type":"bytes32"}],
	 "outputs":[{"name":"","type":"string"}]}
]`)

func mustABI(src string) *ABI {
	a, err := ParseABIJSON(json.RawMessage(src))
	if err != nil {
		panic("embedded ENS ABI: " + err.Error())
	}
	return a
}

// ENSNamehash computes the EIP-137 namehash of a dotted name.
func ENSNamehash(name string) []byte {
	node := make([]byte, 32)
	if name == "" {
		return node
	}
	labels := strings.Split(strings.ToLower(strings.TrimSuffix(name, ".")), ".")
	for i := len(labels) - 1; i >= 0; i-- {
		buf := make([]byte, 0, 64)
		buf = append(buf, node...)
		buf = append(buf, Keccak256([]byte(labels[i]))...)
		node = Keccak256(buf)
	}
	return node
}

// ensCall runs an eth_call against addr with the named ENS method.
func ensCall(ctx context.Context, p *Provider, addr, method string, node []byte) ([]any, error) {
	m, err := ensABI.Method(method, 1)
	if err != nil {
		return nil, err
	}
	data, err := m.PackArgs([]any{"0x" + hex.EncodeToString(node)})
	if err != nil {
		return nil, err
	}
	raw, err := p.Call(ctx, "eth_call", []any{
		map[string]any{"to": addr, "data": "0x" + hex.EncodeToString(data)}, "latest",
	})
	if err != nil {
		return nil, err
	}
	var ret string
	if err := json.Unmarshal(raw, &ret); err != nil {
		return nil, fmt.Errorf("eth_call %s: unexpected result %s", method, raw)
	}
	out, err := ParseHexBytes(ret)
	if err != nil {
		return nil, fmt.Errorf("eth_call %s: %w", method, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s returned empty (name unregistered or resolver unset)", method)
	}
	return m.UnpackOutputs(out)
}

// ENSResolve forward-resolves name to an address via registry→resolver→addr.
func ENSResolve(ctx context.Context, p *Provider, name string) (string, error) {
	chainID, err := ChainID(ctx, p)
	if err != nil {
		return "", fmt.Errorf("chain id: %w", err)
	}
	if !ensChains[chainID] {
		return "", fmt.Errorf(
			"ENS is not supported on chain %d (known deployments: mainnet, sepolia, holesky, hoodi)",
			chainID,
		)
	}
	node := ENSNamehash(name)
	out, err := ensCall(ctx, p, ensRegistryAddr, "resolver", node)
	if err != nil {
		return "", err
	}
	resolver, _ := out[0].(string)
	if resolver == "" || resolver == "0x0000000000000000000000000000000000000000" {
		return "", fmt.Errorf("no resolver for %q", name)
	}
	out, err = ensCall(ctx, p, resolver, "addr", node)
	if err != nil {
		return "", err
	}
	addr, _ := out[0].(string)
	if addr == "" || addr == "0x0000000000000000000000000000000000000000" {
		return "", fmt.Errorf("%q has no address record", name)
	}
	return NormalizeAddress(addr)
}

// ENSReverse resolves an address to its primary name via <hex>.addr.reverse.
func ENSReverse(ctx context.Context, p *Provider, address string) (string, error) {
	addr, err := NormalizeAddress(address)
	if err != nil {
		return "", err
	}
	chainID, err := ChainID(ctx, p)
	if err != nil {
		return "", fmt.Errorf("chain id: %w", err)
	}
	if !ensChains[chainID] {
		return "", fmt.Errorf("ENS is not supported on chain %d", chainID)
	}
	node := ENSNamehash(strings.ToLower(strings.TrimPrefix(addr, "0x")) + ".addr.reverse")
	out, err := ensCall(ctx, p, ensRegistryAddr, "resolver", node)
	if err != nil {
		return "", err
	}
	resolver, _ := out[0].(string)
	if resolver == "" || resolver == "0x0000000000000000000000000000000000000000" {
		return "", fmt.Errorf("no reverse record for %s", addr)
	}
	out, err = ensCall(ctx, p, resolver, "name", node)
	if err != nil {
		return "", err
	}
	name, _ := out[0].(string)
	if name == "" {
		return "", fmt.Errorf("no reverse record for %s", addr)
	}
	return name, nil
}
