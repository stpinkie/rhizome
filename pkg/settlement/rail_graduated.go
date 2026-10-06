package settlement

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// GraduatedRail is the v0.16.0 Track 127 rail: the custom RhizomeEscrow
// contract (contracts/RhizomeEscrow.sol) — a single session-keyed
// deployment replacing Smart Invoice's clone-per-session factory. What
// the graduation buys over the survey's v1 pick:
//
//   - release(sessionId, amount): amount-based partials — the drawdown
//     primitive Track 128 rides (Smart Invoice only had milestone-indexed
//     whole releases).
//   - claim(sessionId): a native seller pull after the dispute deadline —
//     the claim gap the survey documented (Smart Invoice's only
//     post-window valve paid the *client*).
//   - arbiter per session: escrow_arbiter lands in the open() call, so a
//     Track 129 ERC-792 adapter contract can occupy the slot without a
//     redeploy.
//
// Session-id convention: the wire session_id is the bytes32 session key
// (keccak256(correlationID), 0x-hex) — NOT an escrow clone address.
// VerifyLock/verbs validate that shape; watch filters key on
// contract-address + sessionId topic instead of contract-address alone.
type GraduatedRail struct {
	cfg       RailConfig
	snd       Sender
	nowFn     func(ctx context.Context) (int64, error)
	confirmTo time.Duration

	decMu    sync.Mutex
	decimals *uint8
}

// NewGraduatedRail validates cfg and returns the graduated rail. cfg.Kind
// must be RailKindRhizome (config resolves it from escrow_rail).
func NewGraduatedRail(cfg RailConfig, snd Sender) (*GraduatedRail, error) {
	if snd == nil {
		return nil, fmt.Errorf("settlement: nil Sender")
	}
	switch {
	case cfg.ChainID == 0:
		return nil, fmt.Errorf("settlement: chain id required")
	case !web3.IsAddress(cfg.Factory):
		return nil, fmt.Errorf("settlement: contract %q is not a 0x address", cfg.Factory)
	case !web3.IsAddress(cfg.Arbiter):
		return nil, fmt.Errorf("settlement: arbiter %q is not a 0x address", cfg.Arbiter)
	case cfg.DisputeWindowSecs <= 0:
		return nil, fmt.Errorf("settlement: dispute window must be positive")
	}
	return &GraduatedRail{
		cfg: cfg, snd: snd, nowFn: snd.Now, confirmTo: 2 * time.Minute,
	}, nil
}

// SessionID returns the on-chain session key for a correlation id —
// computable off-chain, so the buyer can present the session_id before
// open() lands (the graduation's predictDeterministicAddress analogue).
func (r *GraduatedRail) SessionID(correlationID string) string {
	return bytes32Hex(GraduatedSessionID(correlationID))
}

// SessionLogFilter returns the (contract address, session topic) pair
// the event watcher filters on — graduated escrows share one contract
// address, so the indexed sessionId topic does the per-session scoping
// that the clone address did under Smart Invoice.
func (r *GraduatedRail) SessionLogFilter(sessionID string) (string, string, error) {
	sid, err := parseSessionID(sessionID)
	if err != nil {
		return "", "", err
	}
	return r.cfg.Factory, bytes32Hex(sid), nil
}

// Open funds a session: ERC-20 approve(contract, amount) then
// open(sessionId, seller, token, amount, taskHash, window, arbiter).
// Two transactions — the approve confirms first so open's transferFrom
// cannot race it on slow mempools. The returned hash is the open tx.
func (r *GraduatedRail) Open(
	ctx context.Context, correlationID string, t Terms,
) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	window := r.cfg.DisputeWindowSecs
	if t.TerminationTime != 0 {
		now, err := r.nowFn(ctx)
		if err != nil {
			return "", fmt.Errorf("open: chain time: %w", err)
		}
		window = t.TerminationTime - now
		if window <= 0 {
			return "", fmt.Errorf("open: termination_time %d is in the past", t.TerminationTime)
		}
	}

	approve, err := erc20ABI.Method("approve", 2)
	if err != nil {
		return "", err
	}
	appData, err := approve.PackArgs([]any{r.cfg.Factory, t.Amount.String()})
	if err != nil {
		return "", err
	}
	appHash, err := r.snd.SendTx(ctx, t.Token, appData, nil)
	if err != nil {
		return "", fmt.Errorf("open: approve: %w", err)
	}
	if err := r.waitConfirmed(ctx, appHash); err != nil {
		return "", fmt.Errorf("open: approve %s: %w", appHash, err)
	}

	open, err := graduatedABI.Method("open", 8)
	if err != nil {
		return "", err
	}
	openData, err := open.PackArgs([]any{
		bytes32Hex(GraduatedSessionID(correlationID)),
		t.Seller,
		t.Token,
		t.Amount.String(),
		bytes32Hex(t.TaskHash),
		fmt.Sprintf("%d", window),
		r.cfg.Arbiter,
		t.Drawdown,
	})
	if err != nil {
		return "", err
	}
	openHash, err := r.snd.SendTx(ctx, r.cfg.Factory, openData, nil)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	return openHash, nil
}

// graduatedSession is the decoded sessionOf() row.
type graduatedSession struct {
	buyer, seller, arbiter, token string
	amount, released              *big.Int
	deadline                      int64
	status                        uint8
	taskHash                      [32]byte
	drawdown                      bool
}

func (r *GraduatedRail) sessionOf(
	ctx context.Context, sessionID string,
) (*graduatedSession, error) {
	sid, err := parseSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	m, err := graduatedABI.Method("sessionOf", 1)
	if err != nil {
		return nil, err
	}
	data, err := m.PackArgs([]any{bytes32Hex(sid)})
	if err != nil {
		return nil, err
	}
	out, err := r.snd.Call(ctx, r.cfg.Factory, data)
	if err != nil {
		return nil, fmt.Errorf("sessionOf %s: %w", sessionID, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("sessionOf %s: %w", sessionID, ErrNotFound)
	}
	vals, err := m.UnpackOutputs(out)
	if err != nil {
		return nil, fmt.Errorf("sessionOf %s: %w", sessionID, err)
	}
	if len(vals) != 10 {
		return nil, fmt.Errorf("sessionOf %s: %d outputs, want 10", sessionID, len(vals))
	}
	s := &graduatedSession{}
	s.buyer, _ = vals[0].(string)
	s.seller, _ = vals[1].(string)
	s.arbiter, _ = vals[2].(string)
	s.token, _ = vals[3].(string)
	if s.amount, err = uintFromOut(vals[4]); err != nil {
		return nil, fmt.Errorf("sessionOf amount: %w", err)
	}
	if s.released, err = uintFromOut(vals[5]); err != nil {
		return nil, fmt.Errorf("sessionOf released: %w", err)
	}
	if dl, err := uintFromOut(vals[6]); err != nil {
		return nil, fmt.Errorf("sessionOf deadline: %w", err)
	} else {
		s.deadline = dl.Int64()
	}
	sv, _ := vals[7].(string)
	st64, err := strconv.ParseUint(sv, 10, 8)
	if err != nil {
		return nil, fmt.Errorf("sessionOf status: %w", err)
	}
	s.status = uint8(st64)
	th, _ := vals[8].(string)
	raw, err := web3.ParseHexBytes(th)
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("sessionOf taskHash: bad output %v", vals[8])
	}
	copy(s.taskHash[:], raw)
	s.drawdown, _ = vals[9].(bool)
	return s, nil
}

// VerifyLock implements the sell-side pre-work gate over eth_call: the
// session exists (status Open), matches the presented terms exactly,
// nothing released yet, and the dispute deadline is in the future.
func (r *GraduatedRail) VerifyLock(
	ctx context.Context, sessionID string, t Terms,
) (bool, error) {
	if _, err := parseSessionID(sessionID); err != nil {
		return false, err
	}
	if err := t.Validate(); err != nil {
		return false, err
	}
	s, err := r.sessionOf(ctx, sessionID)
	if err != nil {
		return false, err
	}
	if s.status == graduatedStatusNone {
		return false, nil // unopened session — strict false, not an error
	}
	now, err := r.nowFn(ctx)
	if err != nil {
		return false, fmt.Errorf("verify lock: chain time: %w", err)
	}
	ok := s.status == graduatedStatusOpen &&
		addrEq(s.buyer, t.Buyer) &&
		addrEq(s.seller, t.Seller) &&
		addrEq(s.arbiter, r.cfg.Arbiter) &&
		addrEq(s.token, t.Token) &&
		s.deadline > now &&
		s.drawdown == t.Drawdown &&
		(t.TerminationTime == 0 || s.deadline == t.TerminationTime)
	if t.Drawdown {
		// Drawdown verify is a headroom check: the wire amount is this
		// task's draw, the on-chain amount is the session budget — the
		// draw must fit the remainder. taskHash is a session-level
		// commitment under drawdown (one session spans many tasks), so
		// the per-task hash equality check doesn't apply.
		ok = ok && new(big.Int).Add(s.released, t.Amount).Cmp(s.amount) <= 0
	} else {
		taskOK := t.TaskHash == ([32]byte{}) || s.taskHash == t.TaskHash
		ok = ok && s.amount.Cmp(t.Amount) == 0 && s.released.Sign() == 0 && taskOK
	}
	return ok, nil
}

// Release pays the session's entire remaining balance to the seller —
// the full-settle form of the partial verb.
func (r *GraduatedRail) Release(ctx context.Context, sessionID string) (string, error) {
	s, err := r.sessionOf(ctx, sessionID)
	if err != nil {
		return "", err
	}
	remaining := new(big.Int).Sub(s.amount, s.released)
	if remaining.Sign() <= 0 {
		return "", fmt.Errorf("release: nothing left to release")
	}
	return r.ReleasePartial(ctx, sessionID, remaining)
}

// ReleasePartial is the drawdown primitive: pays `amount` of the
// remaining escrow to the seller. Amount-bound and session-keyed —
// what Smart Invoice's milestone-indexed release() could not express.
func (r *GraduatedRail) ReleasePartial(
	ctx context.Context, sessionID string, amount *big.Int,
) (string, error) {
	if amount == nil || amount.Sign() <= 0 {
		return "", fmt.Errorf("release: amount must be positive")
	}
	return r.verb(ctx, sessionID, "release",
		[]any{bytes32Hex0(sessionID), amount.String()})
}

// Claim is the native seller-pull the graduated contract adds — after
// the dispute deadline the seller takes the remaining balance outright.
func (r *GraduatedRail) Claim(ctx context.Context, sessionID string) (string, error) {
	return r.verb(ctx, sessionID, "claim", []any{bytes32Hex0(sessionID)})
}

// Dispute locks the session for arbitration — either party on-chain.
func (r *GraduatedRail) Dispute(
	ctx context.Context, sessionID string, details [32]byte,
) (string, error) {
	return r.verb(ctx, sessionID, "dispute",
		[]any{bytes32Hex0(sessionID), bytes32Hex(details)})
}

// Withdraw is the buyer's post-grace clawback: after deadline +
// CLAIM_GRACE the remaining balance returns to the buyer.
func (r *GraduatedRail) Withdraw(ctx context.Context, sessionID string) (string, error) {
	return r.verb(ctx, sessionID, "withdraw", []any{bytes32Hex0(sessionID)})
}

// Resolve is the arbiter's split — awards must consume the remaining
// balance exactly; the contract speaks on mismatch.
func (r *GraduatedRail) Resolve(
	ctx context.Context,
	sessionID string,
	clientAward, providerAward *big.Int,
	details [32]byte,
) (string, error) {
	if clientAward == nil || providerAward == nil ||
		clientAward.Sign() < 0 || providerAward.Sign() < 0 {
		return "", fmt.Errorf("resolve: awards must be non-negative")
	}
	return r.verb(ctx, sessionID, "resolve", []any{
		bytes32Hex0(sessionID), clientAward.String(),
		providerAward.String(), bytes32Hex(details),
	})
}

// TokenDecimals resolves the configured payment token's decimals().
func (r *GraduatedRail) TokenDecimals(ctx context.Context) (uint8, error) {
	r.decMu.Lock()
	defer r.decMu.Unlock()
	if r.decimals != nil {
		return *r.decimals, nil
	}
	m, err := erc20ABI.Method("decimals", 0)
	if err != nil {
		return 0, err
	}
	data, err := m.PackArgs(nil)
	if err != nil {
		return 0, err
	}
	out, err := r.snd.Call(ctx, r.cfg.Token, data)
	if err != nil {
		return 0, fmt.Errorf("decimals on %s: %w", r.cfg.Token, err)
	}
	if len(out) == 0 {
		return 0, fmt.Errorf("decimals on %s: %w", r.cfg.Token, ErrNotFound)
	}
	vals, err := m.UnpackOutputs(out)
	if err != nil {
		return 0, fmt.Errorf("decimals on %s: %w", r.cfg.Token, err)
	}
	s, _ := vals[0].(string)
	n, err := strconvParseUint8(s)
	if err != nil {
		return 0, fmt.Errorf("decimals: bad uint8 output %v", vals[0])
	}
	r.decimals = &n
	return n, nil
}

// WithSender returns a rail sharing config but sending through s.
func (r *GraduatedRail) WithSender(s Sender) *GraduatedRail {
	return &GraduatedRail{
		cfg: r.cfg, snd: s, nowFn: r.nowFn, confirmTo: r.confirmTo,
	}
}

func (r *GraduatedRail) verb(
	ctx context.Context, sessionID, name string, args []any,
) (string, error) {
	if _, err := parseSessionID(sessionID); err != nil {
		return "", err
	}
	m, err := graduatedABI.Method(name, len(args))
	if err != nil {
		return "", err
	}
	data, err := m.PackArgs(args)
	if err != nil {
		return "", err
	}
	hash, err := r.snd.SendTx(ctx, r.cfg.Factory, data, nil)
	if err != nil {
		return "", fmt.Errorf("%s on %s: %w", name, sessionID, err)
	}
	return hash, nil
}

func (r *GraduatedRail) waitConfirmed(ctx context.Context, hash string) error {
	ctx, cancel := context.WithTimeout(ctx, r.confirmTo)
	defer cancel()
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	for {
		rc, err := r.snd.Receipt(ctx, hash)
		if err != nil {
			return err
		}
		if rc != nil {
			if rc.Status == "reverted" {
				return ErrReverted
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("receipt for %s: %w", hash, ctx.Err())
		case <-tick.C:
		}
	}
}

// parseSessionID validates a graduated session id: 0x + 64 hex.
func parseSessionID(sessionID string) ([32]byte, error) {
	var out [32]byte
	raw, err := web3.ParseHexBytes(sessionID)
	if err != nil || len(raw) != 32 {
		return out, fmt.Errorf(
			"session %q is not a 0x-prefixed bytes32", sessionID)
	}
	copy(out[:], raw)
	return out, nil
}

// bytes32Hex0 is bytes32Hex for a string that must already be a bytes32 —
// verb args take the validated sessionID straight through.
func bytes32Hex0(sessionID string) string {
	sid, _ := parseSessionID(sessionID)
	return bytes32Hex(sid)
}

func uintFromOut(v any) (*big.Int, error) {
	s, _ := v.(string)
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("bad uint output %v", v)
	}
	return n, nil
}

func strconvParseUint8(s string) (uint8, error) {
	n, err := strconv.ParseUint(s, 10, 8)
	return uint8(n), err
}
