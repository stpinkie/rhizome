// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"context"
	"fmt"
	"strings"
)

// ResolvedContract is the outcome of contract-name resolution: a concrete
// address plus the ABI when one is known (registry entry or embedded).
type ResolvedContract struct {
	Address string
	ABI     *ABI
	// Source records how the address resolved: "literal"|"registry"|"ens".
	Source string
	Label  string // registry label when Source == "registry"
}

// ResolveContract resolves name → contract in order: 0x literal → ABI
// registry label → ENS name. registry may be nil (label lookups miss).
// ENS needs the provider for on-chain lookups; provider may be nil when the
// caller only expects literals/labels.
func ResolveContract(ctx context.Context, p *Provider, registry *ABIRegistry,
	name string,
) (*ResolvedContract, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("contract is required")
	}
	if IsAddress(name) {
		addr, _ := NormalizeAddress(name)
		rc := &ResolvedContract{Address: addr, Source: "literal"}
		if registry != nil {
			if e, err := registry.ByAddress(addr); err == nil && e != nil {
				rc.ABI = e.ABI
				rc.Label = e.Label
			}
		}
		return rc, nil
	}
	if registry != nil && ValidABILabel(name) && !strings.HasSuffix(name, ".eth") {
		if e, err := registry.Get(name); err == nil {
			if e.Address == "" {
				return nil, fmt.Errorf(
					"ABI %q has no bound address — pass the contract address explicitly", name)
			}
			if len(e.ChainIDs) > 0 && p != nil {
				chainID, err := ChainID(ctx, p)
				if err != nil {
					return nil, fmt.Errorf("chain id: %w", err)
				}
				ok := false
				for _, id := range e.ChainIDs {
					if id == chainID {
						ok = true
						break
					}
				}
				if !ok {
					return nil, fmt.Errorf("contract %q is registered for chains %v, not %d",
						name, e.ChainIDs, chainID)
				}
			}
			return &ResolvedContract{
				Address: e.Address, ABI: e.ABI, Source: "registry", Label: e.Label,
			}, nil
		}
	}
	// ENS: namehash covers *.eth (and DNS-imported names).
	if p == nil {
		return nil, fmt.Errorf("cannot resolve %q — not an address or registered label, and no endpoint for ENS", name)
	}
	addr, err := ENSResolve(ctx, p, name)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", name, err)
	}
	return &ResolvedContract{Address: addr, Source: "ens"}, nil
}
