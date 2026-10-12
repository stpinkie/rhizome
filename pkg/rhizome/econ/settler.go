package econ

import (
	"context"
	"errors"
)

// ErrVerifyPending marks a retryable on-chain verification outcome —
// the transaction is not yet visible or not yet mined. Payee handlers
// surface it as a transport error (payer retries phase 2), not a
// settle_reject divergence.
var ErrVerifyPending = errors.New("payment verification pending")

// IsVerifyPending reports whether err is a retryable verification
// outcome (ErrVerifyPending or a rail-specific type satisfying it).
func IsVerifyPending(err error) bool {
	return errors.Is(err, ErrVerifyPending)
}

// Settler is the close-out rail behind a settle_backend=web3 node —
// implemented by pkg/rhizome/econweb3 and declared here so the mesh
// drives it without importing the web3 stack. The mesh owns the
// two-phase handshake and the in-flight journal; the Settler owns
// calldata, signing, queueing, and on-chain verification.
type Settler interface {
	// PayerAddress resolves the signing address committed as
	// payment.from in web3 offers — settle_from when configured, else
	// the wallet default.
	PayerAddress() (string, error)

	// SignerMode reports "approval" (pending-approval queue, human
	// resolves) or "wallet" (immediate sign + broadcast — the market's
	// settlement_signer=wallet posture).
	SignerMode() string

	// Queue submits the payment transfer to the pending-approval queue
	// under the given (pre-journaled) pending id.
	Queue(ctx context.Context, p *SettlePayment, amount, pendingID string) error

	// QueuedTx polls a queued entry: (hash, true, nil) once broadcast;
	// ("", false, nil) while still awaiting resolution; err on a
	// terminal reject/expire/fail.
	QueuedTx(pendingID string) (txHash string, done bool, err error)

	// SignRaw builds and signs the payment transaction — wallet mode.
	// The mesh journals raw+hash BEFORE Broadcast so a restart can
	// re-broadcast or verify the identical tx instead of double-paying.
	SignRaw(ctx context.Context, p *SettlePayment, amount string) (raw []byte, txHash string, err error)

	// Broadcast submits signed raw bytes; identical re-broadcasts are
	// already-known and return the same hash.
	Broadcast(ctx context.Context, raw []byte) (txHash string, err error)

	// Verify checks that txHash fulfills the payment on-chain: right
	// chain, sender (payment.from), destination (payment.address for
	// native / payment.asset for ERC-20), exact amount in the asset's
	// base units, and a successful receipt. A nil transaction or an
	// unmined receipt returns a descriptive error — callers treat it
	// as retryable rather than divergence.
	Verify(ctx context.Context, p *SettlePayment, amount, txHash string) error
}
