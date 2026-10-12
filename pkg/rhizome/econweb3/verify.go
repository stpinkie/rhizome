package econweb3

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// PendingError marks a retryable verification outcome — the transaction
// is not yet visible or not yet mined. The payee's settle handler
// surfaces it as a transport error (payer retries phase 2) rather than
// a settle_reject divergence.
type PendingError struct{ Reason string }

func (e *PendingError) Error() string { return "payment verification pending: " + e.Reason }

// Is satisfies errors.Is(econ.ErrVerifyPending) so payee handlers can
// classify retryable outcomes without importing this package.
func (e *PendingError) Is(target error) bool { return target == econ.ErrVerifyPending }

// chainTx is the decoded eth_getTransactionByHash object — full input
// (the display decoder truncates it) and chainId, which the payee binds
// to the committed payment chain.
type chainTx struct {
	Hash  string
	From  string
	To    string
	Value *big.Int
	Input []byte
	Chain *big.Int // nil when the endpoint omits chainId
	Mined bool
}

// lookupTx fetches one transaction by hash; nil when the endpoint
// reports it absent.
func (b *Backend) lookupTx(ctx context.Context, txHash string) (*chainTx, error) {
	if b.provider == nil {
		return nil, fmt.Errorf("econweb3: no web3 provider configured")
	}
	raw, err := b.provider.Call(ctx, "eth_getTransactionByHash", []any{txHash})
	if err != nil {
		return nil, err
	}
	return decodeChainTx(raw)
}

func decodeChainTx(raw json.RawMessage) (*chainTx, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var rt struct {
		Hash        string `json:"hash"`
		From        string `json:"from"`
		To          string `json:"to"`
		Value       string `json:"value"`
		Input       string `json:"input"`
		ChainID     string `json:"chainId"`
		BlockNumber string `json:"blockNumber"`
	}
	if err := json.Unmarshal(raw, &rt); err != nil {
		return nil, fmt.Errorf("decode transaction: %w", err)
	}
	tx := &chainTx{Hash: rt.Hash, From: rt.From, To: rt.To}
	var err error
	if tx.Value, err = web3.ParseQuantity(rt.Value); err != nil {
		return nil, fmt.Errorf("transaction value %q: %w", rt.Value, err)
	}
	if tx.Input, err = web3.ParseHexBytes(rt.Input); err != nil {
		return nil, fmt.Errorf("transaction input: %w", err)
	}
	if rt.ChainID != "" {
		if tx.Chain, err = web3.ParseQuantity(rt.ChainID); err != nil {
			return nil, fmt.Errorf("transaction chainId %q: %w", rt.ChainID, err)
		}
	}
	tx.Mined = rt.BlockNumber != "" && rt.BlockNumber != "0x0"
	return tx, nil
}

// Verify checks that txHash fulfills the payment commitment on-chain:
// transaction exists, right chain, sent by payment.from, paying exactly
// the committed amount to the committed destination (payment.address
// for native; payment.asset + transfer() calldata for ERC-20), with a
// successful receipt. Not-found and unmined outcomes are PendingError —
// everything else is a permanent mismatch.
func (b *Backend) Verify(ctx context.Context, p *econ.SettlePayment, amount, txHash string) error {
	if p == nil {
		return fmt.Errorf("econweb3: missing payment commitment")
	}
	if !web3.IsHash32(txHash) {
		return fmt.Errorf("econweb3: tx hash %q is not a 0x32-byte hash", txHash)
	}
	tx, err := b.lookupTx(ctx, txHash)
	if err != nil {
		return &PendingError{Reason: "transaction lookup failed: " + err.Error()}
	}
	if tx == nil {
		return &PendingError{Reason: "transaction not found"}
	}
	if tx.Chain != nil {
		want, err := parseChainID(p.ChainID)
		if err != nil {
			return err
		}
		if tx.Chain.Uint64() != want {
			return fmt.Errorf(
				"tx %s is on chain %s, payment committed to chain %s",
				txHash, tx.Chain, p.ChainID)
		}
	}
	if p.From != "" && !strings.EqualFold(tx.From, p.From) {
		return fmt.Errorf("tx %s sender %s != committed payment.from %s",
			txHash, tx.From, p.From)
	}
	if p.Asset == "native" {
		want, err := baseUnits(amount, nativeDecimals)
		if err != nil {
			return err
		}
		if !strings.EqualFold(tx.To, p.Address) {
			return fmt.Errorf("tx %s pays %s, not committed payout %s",
				txHash, tx.To, p.Address)
		}
		if tx.Value.Cmp(want) != 0 {
			return fmt.Errorf("tx %s pays %s wei, expected %s wei",
				txHash, tx.Value, want)
		}
	} else {
		if !web3.IsAddress(p.Asset) {
			return fmt.Errorf("econweb3: payment asset %q is not a contract address", p.Asset)
		}
		dec, err := b.assetDecimals(ctx, p.Asset)
		if err != nil {
			return err
		}
		want, err := baseUnits(amount, dec)
		if err != nil {
			return err
		}
		if !strings.EqualFold(tx.To, p.Asset) {
			return fmt.Errorf("tx %s targets %s, not committed asset %s",
				txHash, tx.To, p.Asset)
		}
		wantData, err := transferData(p.Address, want)
		if err != nil {
			return err
		}
		if hex.EncodeToString(tx.Input) != hex.EncodeToString(wantData) {
			return fmt.Errorf(
				"tx %s calldata is not transfer(%s, %s) on %s",
				txHash, p.Address, want, p.Asset)
		}
	}
	rc, err := b.receipt(ctx, txHash)
	if err != nil {
		return &PendingError{Reason: "receipt lookup failed: " + err.Error()}
	}
	if rc == nil {
		return &PendingError{Reason: "transaction not yet mined"}
	}
	if rc.Status != "success" {
		return fmt.Errorf("tx %s reverted on-chain", txHash)
	}
	return nil
}

func (b *Backend) receipt(ctx context.Context, txHash string) (*web3.Receipt, error) {
	if b.provider == nil {
		return nil, fmt.Errorf("econweb3: no web3 provider configured")
	}
	raw, err := b.provider.Call(ctx, "eth_getTransactionReceipt", []any{txHash})
	if err != nil {
		return nil, err
	}
	return web3.DecodeReceipt(raw)
}
