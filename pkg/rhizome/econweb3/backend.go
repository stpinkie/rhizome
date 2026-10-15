// Package econweb3 is the web3 close-out rail for paired settlement
// (Track 143). It implements econ.Settler: the mesh drives the
// two-phase handshake and owns the in-flight journal; this package owns
// the payment transaction — native or ERC-20 transfer of the accrued
// total to the peer's manifest-signed payout address — through either
// the shared pending-approval queue (default) or the wallet signer
// opt-in (the market's settlement_signer=wallet posture), plus the
// payee-side on-chain verification.
package econweb3

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// Backend implements econ.Settler over the shared web3 stack: provider
// for endpoint resolution and reads, SigningStack for wallet keys, the
// pending-approval queue, the spend ledger, and the signing policy.
type Backend struct {
	provider   *web3.Provider
	stack      *web3.SigningStack
	signer     string // "approval" | "wallet"
	from       string // configured settle_from; "" → wallet default
	approveTTL time.Duration
}

// Options configures the backend. Signer defaults to "approval".
type Options struct {
	// Signer is "approval" (pending queue + signing policy) or
	// "wallet" (sign + broadcast inline). From is the payer EVM address
	// (mesh.economy.settle_from); empty resolves the wallet default at
	// offer time. ApproveTTL bounds each queued entry's lifetime
	// (0 → the store default).
	Signer     string
	From       string
	ApproveTTL time.Duration
}

// New builds the rail. stack may be nil only when the caller never
// queues or signs (verify-only) — Queue/SignRaw then error clearly.
func New(provider *web3.Provider, stack *web3.SigningStack, opts Options) (*Backend, error) {
	signer := opts.Signer
	if signer == "" {
		signer = "approval"
	}
	if signer != "approval" && signer != "wallet" {
		return nil, fmt.Errorf("econweb3: signer must be approval or wallet, got %q", opts.Signer)
	}
	if opts.From != "" && !web3.IsAddress(opts.From) {
		return nil, fmt.Errorf("econweb3: settle_from %q is not a 0x address", opts.From)
	}
	return &Backend{
		provider:   provider,
		stack:      stack,
		signer:     signer,
		from:       opts.From,
		approveTTL: opts.ApproveTTL,
	}, nil
}

// SignerMode reports "approval" or "wallet".
func (b *Backend) SignerMode() string { return b.signer }

// PayerAddress resolves the signing address committed as payment.from:
// the configured settle_from (must be a stored key) or the wallet
// default.
func (b *Backend) PayerAddress() (string, error) {
	if b.stack == nil || b.stack.Wallets == nil {
		return "", fmt.Errorf("econweb3: no wallet store configured")
	}
	if b.from != "" {
		if !b.stack.Wallets.Has(b.from) {
			return "", fmt.Errorf(
				"econweb3: settle_from %s has no key in the web3 wallet", b.from)
		}
		return b.from, nil
	}
	addr, err := b.stack.Wallets.Default()
	if err != nil {
		return "", fmt.Errorf("econweb3: wallet default: %w", err)
	}
	if addr == "" {
		return "", fmt.Errorf(
			"econweb3: no wallet — create one with `rhizome wallet create` or set mesh.economy.settle_from")
	}
	return addr, nil
}

// transferData ABI-encodes ERC-20 transfer(to, amount) — selector
// 0xa9059cbb.
func transferData(to string, amount *big.Int) ([]byte, error) {
	addrT, err := web3.ParseABIType("address")
	if err != nil {
		return nil, err
	}
	uintT, err := web3.ParseABIType("uint256")
	if err != nil {
		return nil, err
	}
	args, err := web3.EncodeABIArguments([]*web3.ABIType{addrT, uintT}, []any{to, amount})
	if err != nil {
		return nil, fmt.Errorf("encode transfer args: %w", err)
	}
	return append(web3.MethodSelector("transfer(address,uint256)"), args...), nil
}

// baseUnits converts a canonical-decimal ledger amount into the asset's
// base units; the result must be integral (you cannot pay a fraction of
// the asset's smallest unit).
func baseUnits(amount string, decimals int64) (*big.Int, error) {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(amount))
	if !ok || r.Sign() < 0 {
		return nil, fmt.Errorf("amount %q is not a decimal", amount)
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(decimals), nil))
	r.Mul(r, scale)
	if !r.IsInt() {
		return nil, fmt.Errorf("amount %s is finer than %d-decimal base units", amount, decimals)
	}
	return r.Num(), nil
}

const nativeDecimals = 18

// assetDecimals resolves an ERC-20's decimals() over eth_call; the
// result must be a small uint (a missing/short return is a malformed
// contract response, never a silent 18).
func (b *Backend) assetDecimals(ctx context.Context, asset string) (int64, error) {
	raw, err := b.call(ctx, asset, web3.MethodSelector("decimals()"))
	if err != nil {
		return 0, fmt.Errorf("decimals() on %s: %w", asset, err)
	}
	var ret string
	if err := json.Unmarshal(raw, &ret); err != nil {
		return 0, fmt.Errorf("decimals() on %s: bad return: %w", asset, err)
	}
	data, err := web3.ParseHexBytes(ret)
	if err != nil {
		return 0, fmt.Errorf("decimals() on %s: bad return: %w", asset, err)
	}
	if len(data) != 32 {
		return 0, fmt.Errorf("decimals() on %s: want 32-byte word, got %d", asset, len(data))
	}
	d := new(big.Int).SetBytes(data)
	if !d.IsInt64() || d.Int64() < 0 || d.Int64() > 36 {
		return 0, fmt.Errorf("decimals() on %s: implausible %s", asset, d)
	}
	return d.Int64(), nil
}

// call performs eth_call(to, data, latest) and returns the hex string.
func (b *Backend) call(ctx context.Context, to string, data []byte) (json.RawMessage, error) {
	if b.provider == nil {
		return nil, fmt.Errorf("econweb3: no web3 provider configured")
	}
	return b.provider.Call(ctx, "eth_call", []any{
		map[string]string{"to": to, "data": "0x" + hex.EncodeToString(data)},
		"latest",
	})
}

// paymentTx renders the transfer as (to, data, valueWei): a plain value
// send for native assets or a transfer() call against the token contract.
func (b *Backend) paymentTx(
	ctx context.Context, p *econ.SettlePayment, amount string,
) (to string, data []byte, valueWei *big.Int, err error) {
	if p == nil {
		return "", nil, nil, fmt.Errorf("econweb3: missing payment commitment")
	}
	if !web3.IsAddress(p.Address) {
		return "", nil, nil, fmt.Errorf("econweb3: payment address %q is not a 0x address", p.Address)
	}
	if p.Asset == "native" {
		v, err := baseUnits(amount, nativeDecimals)
		if err != nil {
			return "", nil, nil, err
		}
		return p.Address, nil, v, nil
	}
	if !web3.IsAddress(p.Asset) {
		return "", nil, nil, fmt.Errorf(
			"econweb3: payment asset %q is neither %q nor a contract address", p.Asset, "native")
	}
	dec, err := b.assetDecimals(ctx, p.Asset)
	if err != nil {
		return "", nil, nil, err
	}
	v, err := baseUnits(amount, dec)
	if err != nil {
		return "", nil, nil, err
	}
	data, err = transferData(p.Address, v)
	if err != nil {
		return "", nil, nil, err
	}
	return p.Asset, data, new(big.Int), nil
}

// Queue submits the transfer to the pending-approval queue under the
// caller-minted pending id, after the same signing-policy evaluation the
// web3_send tool applies — the human gate stays the human gate.
func (b *Backend) Queue(ctx context.Context, p *econ.SettlePayment, amount, pendingID string) error {
	if p == nil {
		return fmt.Errorf("econweb3: missing payment commitment")
	}
	if b.stack == nil || b.stack.Pending == nil {
		return fmt.Errorf("econweb3: no pending-approval queue configured")
	}
	from, err := b.PayerAddress()
	if err != nil {
		return err
	}
	if !strings.EqualFold(from, p.From) {
		return fmt.Errorf("econweb3: signer %s does not match offer commitment payment.from %s",
			from, p.From)
	}
	to, data, valueWei, err := b.paymentTx(ctx, p, amount)
	if err != nil {
		return err
	}
	chainID, err := parseChainID(p.ChainID)
	if err != nil {
		return err
	}
	kind := web3.KindSend
	summary := fmt.Sprintf("paired settle: pay %s native to %s on chain %s",
		amount, p.Address, p.ChainID)
	if p.Asset != "native" {
		kind = web3.KindContract
		summary = fmt.Sprintf("paired settle: transfer %s of %s to %s on chain %s",
			amount, p.Asset, p.Address, p.ChainID)
	}
	req := &web3.SignRequest{
		Kind: string(kind), ChainID: chainID, From: from, To: to,
		ValueWei: valueWei, Data: data,
	}
	if b.stack.Policy != nil {
		if dec := b.stack.Policy.Evaluate(req, b.stack.Ledger, time.Now().UTC()); !dec.Allowed {
			return fmt.Errorf("econweb3: signing policy denied: %s", dec.Reason)
		}
	}
	entry := &web3.PendingEntry{
		ID:      pendingID,
		Kind:    kind,
		ChainID: chainID,
		From:    from,
		To:      to,
		Data:    "0x" + hex.EncodeToString(data),
		Summary: summary,
	}
	if valueWei != nil {
		entry.ValueWei = valueWei.String()
	}
	entry.Selector = req.SelectorHex()
	if b.approveTTL > 0 {
		entry.ExpiresAt = time.Now().UTC().Add(b.approveTTL)
	}
	if _, err := b.stack.Pending.Submit(entry); err != nil {
		return fmt.Errorf("econweb3: queue payment: %w", err)
	}
	return nil
}

// QueuedTx polls the queued entry for the broadcast hash.
func (b *Backend) QueuedTx(pendingID string) (string, bool, error) {
	if b.stack == nil || b.stack.Pending == nil {
		return "", false, fmt.Errorf("econweb3: no pending-approval queue configured")
	}
	e, err := b.stack.Pending.Get(pendingID)
	if err != nil {
		return "", false, err
	}
	switch e.Status {
	case web3.StatusSent:
		return e.TxHash, true, nil
	case web3.StatusRejected:
		return "", true, fmt.Errorf("settle payment %s rejected by approver", pendingID)
	case web3.StatusExpired:
		return "", true, fmt.Errorf("settle payment %s expired awaiting approval", pendingID)
	case web3.StatusFailed:
		return "", true, fmt.Errorf("settle payment %s failed: %s", pendingID, e.Error)
	default:
		return "", false, nil
	}
}

// SignRaw builds and signs the transfer — the wallet signer mode. The
// live eth_chainId must equal the committed payment chain: a flipped
// endpoint refuses rather than signing into the wrong network. Key
// material is zeroed immediately after signing.
func (b *Backend) SignRaw(
	ctx context.Context, p *econ.SettlePayment, amount string,
) ([]byte, string, error) {
	if p == nil {
		return nil, "", fmt.Errorf("econweb3: missing payment commitment")
	}
	if b.stack == nil || b.stack.Wallets == nil {
		return nil, "", fmt.Errorf("econweb3: no wallet store configured")
	}
	from, err := b.PayerAddress()
	if err != nil {
		return nil, "", err
	}
	if !strings.EqualFold(from, p.From) {
		return nil, "", fmt.Errorf("econweb3: signer %s does not match offer commitment payment.from %s",
			from, p.From)
	}
	chainID, err := parseChainID(p.ChainID)
	if err != nil {
		return nil, "", err
	}
	live, err := web3.ChainID(ctx, b.provider)
	if err != nil {
		return nil, "", fmt.Errorf("econweb3: chain id check: %w", err)
	}
	if live != chainID {
		return nil, "", fmt.Errorf(
			"econweb3: chain pin: endpoint is on chain %d, payment committed to %d — refusing to sign",
			live, chainID)
	}
	to, data, valueWei, err := b.paymentTx(ctx, p, amount)
	if err != nil {
		return nil, "", err
	}
	key, err := b.stack.Wallets.PrivateKey(from)
	if err != nil {
		return nil, "", fmt.Errorf("econweb3: wallet key for %s: %w", from, err)
	}
	defer key.Zero()
	req := &web3.TxRequest{ChainID: chainID, To: to, Data: data, ValueWei: valueWei}
	if err := web3.FillTxDefaults(ctx, b.provider, from, req); err != nil {
		return nil, "", fmt.Errorf("econweb3: fill tx: %w", err)
	}
	raw, hash, err := web3.SignTx(req, key)
	if err != nil {
		return nil, "", fmt.Errorf("econweb3: sign tx: %w", err)
	}
	return raw, hash, nil
}

// Broadcast submits signed raw bytes. A re-broadcast of an already
// landed tx returns its hash — crash-resume safe by construction.
func (b *Backend) Broadcast(ctx context.Context, raw []byte) (string, error) {
	if b.provider == nil {
		return "", fmt.Errorf("econweb3: no web3 provider configured")
	}
	local := "0x" + hex.EncodeToString(web3.Keccak256(raw))
	hash, err := web3.SendRawTransaction(ctx, b.provider, raw)
	if err == nil {
		return hash, nil
	}
	// The send may have landed while the reply was lost — or this is a
	// resume re-broadcast. If the hash is visible on-chain, the outcome
	// is already what we wanted.
	if tx, lookErr := b.lookupTx(ctx, local); lookErr == nil && tx != nil {
		return local, nil
	}
	return "", err
}

func parseChainID(s string) (uint64, error) {
	id, ok := new(big.Int).SetString(strings.TrimSpace(s), 10)
	if !ok || !id.IsUint64() || id.Sign() < 0 {
		return 0, fmt.Errorf("econweb3: chain_id %q is not a decimal chain id", s)
	}
	return id.Uint64(), nil
}
