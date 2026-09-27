package settlement

import (
	"context"
	"errors"
	"math/big"
)

// Sentinel errors the rails return. VerifyLock reports a clean "terms do
// not match on-chain state" via (false, nil); failures to even evaluate
// the check surface as errors.
var (
	// ErrNoEscrowConfig marks "escrow_* fields unset — fixture default".
	ErrNoEscrowConfig = errors.New("settlement: no escrow configuration")
	// ErrNotFound marks a session id with no escrow behind it.
	ErrNotFound = errors.New("settlement: escrow not found for session")
	// ErrNotSupported marks lifecycle verbs a rail cannot perform (kept
	// for future rails; Smart Invoice v1 implements every verb).
	ErrNotSupported = errors.New("settlement: operation not supported by this rail")
	// ErrReverted marks a chain-side revert (receipt status 0x0 or an
	// RPC revert error reaching the rail).
	ErrReverted = errors.New("settlement: transaction reverted on-chain")
)

// Rail is the SettlementRail from the v0.14.0 design: the escrow
// lifecycle Tracks 102/103 code against. Session-id convention:
//
//   - Open(correlationID, …) takes the buyer's pre-session reference; the
//     resulting on-chain session key is the escrow clone address,
//     computable before deployment via PredictEscrowAddr(correlationID).
//   - Every other verb takes that escrow address — the session_id the
//     market protocol presents and verifies.
//
// All verbs return the transaction hash (hex) of the state-changing
// call; confirmation/Watching is the caller's concern (web3.WatchRunner).
// Open additionally confirms the deploy tx before funding so the
// returned hash corresponds to a live escrow.
type Rail interface {
	// Open deploys the session escrow and funds it: createDeterministic
	// (seller = provider, amounts = [amount]) then token.transfer. The
	// returned hash is the create tx — the escrow's on-chain birth.
	Open(ctx context.Context, correlationID string, t Terms) (txHash string, err error)

	// VerifyLock is the sell-side pre-work gate (eth_call only — no gas):
	// the escrow exists, is unlocked, unreleased, funded ≥ total, live
	// (terminationTime in the future), and its client/provider/token/
	// total/terminationTime match t. Returns (false, nil) on mismatch.
	VerifyLock(ctx context.Context, sessionID string, t Terms) (ok bool, err error)

	// Release is the buyer-side milestone release: escrow.release()
	// (client-only on-chain; the rail does not enforce msg.sender — the
	// submitting key/Sender does).
	Release(ctx context.Context, sessionID string) (txHash string, err error)

	// Claim is the seller's forced-resolution path. Smart Invoice has no
	// unilateral seller-claim: after the dispute window the safety valve
	// is the CLIENT's withdraw(), not the provider's. The honest v1
	// mapping is lock(bytes32) submitted by the provider — freezing the
	// escrow so the configured arbiter must resolve() an award — not a
	// pull. Callers needing true unilateral claim must wait for the
	// Track 127 graduated contract; see docs/design/escrow-survey.md.
	Claim(ctx context.Context, sessionID string) (txHash string, err error)

	// Dispute is lock(bytes32) submitted by the buyer — same call as
	// Claim; the contract accepts either party and the Lock event's
	// indexed sender records who pulled it.
	Dispute(ctx context.Context, sessionID string, details [32]byte) (txHash string, err error)

	// Resolve is the arbiter's path: resolve(clientAward, providerAward,
	// details), ADR.INDIVIDUAL resolvers only. On-chain constraint:
	// clientAward + providerAward must equal balance −
	// balance/resolutionRate (the resolver's fee); the rail surfaces the
	// contract's ResolutionMismatch revert rather than pre-validating.
	Resolve(
		ctx context.Context,
		sessionID string,
		clientAward, providerAward *big.Int,
		details [32]byte,
	) (txHash string, err error)
}

// SettlementRail is the spec-named alias for Rail — the design docs say
// "SettlementRail", the package idiom says settlement.Rail.
type SettlementRail = Rail
