// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package settlement

import (
	"context"
	"fmt"
	"math/big"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// WalletSender signs transactions locally with a key from the shared
// web3 wallet store (<home>/web3/wallets.json — keyring or
// RHIZOME_WALLET_PASSPHRASE decryption, per the wallet's existing
// posture) and broadcasts via eth_sendRawTransaction. It is the opt-in
// autonomous signer (settlement_signer=wallet) — buy_max_cost_* caps and
// the per-send chain pin bound what it can spend.
//
// Reads (Call/Receipt/Now) reuse DirectSender's JSON-RPC transport; only
// SendTx differs.
type WalletSender struct {
	*DirectSender
	provider *web3.Provider    // tx pipeline: fill defaults, broadcast
	store    *web3.WalletStore // key material — decrypted per send
	chainID  uint64            // pinned chain — SendTx refuses mismatch
}

// NewWalletSender builds a Sender that signs for `from` with keys from
// store. provider must point at the same endpoint as c (the module uses
// web3.NewStaticProvider over its resolved endpoint). chainID pins the
// network — every send re-checks the live chain id before signing.
func NewWalletSender(
	c *web3.Client, provider *web3.Provider, store *web3.WalletStore,
	from string, chainID uint64,
) *WalletSender {
	return &WalletSender{
		DirectSender: NewDirectSender(c, from),
		provider:     provider,
		store:        store,
		chainID:      chainID,
	}
}

// SendTx signs and broadcasts a contract call. The live eth_chainId must
// equal the pinned chain — a flipped endpoint or misconfigured chain
// refuses rather than signing into the wrong network. Key material is
// decrypted for the duration of the sign and zeroed immediately after.
func (s *WalletSender) SendTx(
	ctx context.Context, to string, data []byte, valueWei *big.Int,
) (string, error) {
	if s.provider == nil || s.store == nil {
		return "", fmt.Errorf("wallet sender not configured")
	}
	live, err := web3.ChainID(ctx, s.provider)
	if err != nil {
		return "", fmt.Errorf("chain id check: %w", err)
	}
	if live != s.chainID {
		return "", fmt.Errorf(
			"chain pin: endpoint is on chain %d, sender pinned to %d — refusing to sign",
			live, s.chainID)
	}
	key, err := s.store.PrivateKey(s.From())
	if err != nil {
		return "", fmt.Errorf("wallet key for %s: %w", s.From(), err)
	}
	defer key.Zero()

	req := &web3.TxRequest{
		ChainID: s.chainID, To: to, Data: data, ValueWei: valueWei,
	}
	if err := web3.FillTxDefaults(ctx, s.provider, s.From(), req); err != nil {
		return "", fmt.Errorf("fill tx: %w", err)
	}
	raw, _, err := web3.SignTx(req, key)
	if err != nil {
		return "", fmt.Errorf("sign tx: %w", err)
	}
	hash, err := web3.SendRawTransaction(ctx, s.provider, raw)
	if err != nil {
		return "", fmt.Errorf("broadcast: %w", err)
	}
	return hash, nil
}
