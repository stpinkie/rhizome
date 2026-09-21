// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"fmt"
	"math/big"
	"path/filepath"

	"github.com/stpinkie/rhizome/pkg/config"
)

// WalletDir returns the web3 state directory under rhizome home.
func WalletDir(rhizomeHome string) string {
	return filepath.Join(rhizomeHome, "web3")
}

// SigningStack bundles the file-backed signing state rooted at
// <RHIZOME_HOME>/web3 — wallet keys, the pending-approval queue, and the
// spend ledger — so the agent tools, daemon API, and CLI share one view.
type SigningStack struct {
	Wallets *WalletStore
	Pending *PendingStore
	Ledger  *SpendLedger
	Policy  *SigningPolicy
	// Home is the web3 state dir (<RHIZOME_HOME>/web3).
	Home string
}

// OpenSigningStack opens the wallet/pending/ledger stores under
// WalletDir(rhizomeHome) and builds the signing policy from cfg. The stores
// are file-backed — opening is cheap and safe even when signing is off.
func OpenSigningStack(rhizomeHome string, cfg *config.Web3SigningConfig) (*SigningStack, error) {
	pol, err := PolicyFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	dir := WalletDir(rhizomeHome)
	return &SigningStack{
		Wallets: OpenWalletStore(dir),
		Pending: OpenPendingStore(dir),
		Ledger:  OpenSpendLedger(rhizomeHome),
		Policy:  pol,
		Home:    dir,
	}, nil
}

// PolicyFromConfig converts the signing config section into a SigningPolicy.
// Decimal wei strings parse to big.Int; malformed values are an error at
// startup rather than a silent unlimited policy.
func PolicyFromConfig(cfg *config.Web3SigningConfig) (*SigningPolicy, error) {
	p := &SigningPolicy{}
	if cfg == nil {
		return p, nil
	}
	p.Enabled = cfg.Enabled
	p.FromAddresses = append([]string(nil), cfg.FromAddresses...)
	p.AllowContracts = append([]string(nil), cfg.AllowContracts...)
	p.AllowMethods = append([]string(nil), cfg.AllowMethods...)
	p.ChainIDs = append([]uint64(nil), cfg.ChainIDs...)
	var err error
	if p.MaxValueWeiPerTx, err = parseWei(cfg.MaxValueWeiPerTx); err != nil {
		return nil, fmt.Errorf("tools.web3.signing.max_value_wei_per_tx: %w", err)
	}
	if p.MaxValueWeiPerDay, err = parseWei(cfg.MaxValueWeiPerDay); err != nil {
		return nil, fmt.Errorf("tools.web3.signing.max_value_wei_per_day: %w", err)
	}
	return p, nil
}

// parseWei parses a decimal wei string; empty means "no cap" (nil).
func parseWei(s string) (*big.Int, error) {
	if s == "" {
		return nil, nil
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Sign() < 0 {
		return nil, fmt.Errorf("invalid decimal wei value %q", s)
	}
	return v, nil
}
