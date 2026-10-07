package settlement

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// MockRail is the in-memory fixture SettlementRail Tracks 102/103 code
// and test against. It models the Smart Invoice semantics the rail
// exposes — deterministic session addresses, funded/unlocked/liveness
// verification, the lock→resolve lifecycle with the resolver's
// balance/DefaultResolutionRate fee — without any chain, wallet, or
// network. Addresses and tx hashes are deterministic function of the
// correlation id.
type MockRail struct {
	mu      sync.Mutex
	cfg     RailConfig
	nowFn   func() time.Time
	escrows map[string]*mockEscrow
	txSeq   int
}

type mockEscrow struct {
	terms       Terms // with TerminationTime resolved
	correlation string
	balance     *big.Int
	released    *big.Int
	locked      bool
	lockDetails [32]byte
	resolved    bool
}

// NewMockRail builds the fixture rail. cfg follows RailConfig semantics;
// zero values are fine for tests that don't exercise window/fee paths —
// DisputeWindowSecs<=0 defaults to 24h.
func NewMockRail(cfg RailConfig) *MockRail {
	if cfg.DisputeWindowSecs <= 0 {
		cfg.DisputeWindowSecs = 24 * 60 * 60
	}
	return &MockRail{cfg: cfg, nowFn: time.Now, escrows: map[string]*mockEscrow{}}
}

// SetNowFunc swaps the clock — tests drive termination-window behavior
// deterministically.
func (m *MockRail) SetNowFunc(f func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nowFn = f
}

// PredictEscrowAddr returns the deterministic session address for a
// correlation id — keccak256("mock:"+id)[12:], mirroring the on-chain
// determinism Tests 102/103 rely on.
func (m *MockRail) PredictEscrowAddr(correlationID string) string {
	h := web3.Keccak256([]byte("mock:" + correlationID))
	return web3.ChecksumAddress(h[12:])
}

// Open records a funded escrow at the deterministic address.
func (m *MockRail) Open(_ context.Context, correlationID string, t Terms) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	addr := m.PredictEscrowAddr(correlationID)
	if _, exists := m.escrows[mockKey(addr)]; exists {
		return "", fmt.Errorf("settlement: escrow for correlation %q already open at %s", correlationID, addr)
	}
	if t.TerminationTime == 0 {
		t.TerminationTime = m.nowFn().Unix() + m.cfg.DisputeWindowSecs
	}
	m.escrows[mockKey(addr)] = &mockEscrow{
		terms:       t,
		correlation: correlationID,
		balance:     new(big.Int).Set(t.Amount),
		released:    new(big.Int),
	}
	m.txSeq++
	return m.txHash(addr, m.txSeq), nil
}

// VerifyLock mirrors RPCRail's gate against recorded state.
func (m *MockRail) VerifyLock(_ context.Context, sessionID string, t Terms) (bool, error) {
	if !web3.IsAddress(sessionID) {
		return false, fmt.Errorf("session %q is not a 0x address", sessionID)
	}
	if err := t.Validate(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.escrows[mockKey(sessionID)]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	ok = addrEq(e.terms.Buyer, t.Buyer) &&
		addrEq(e.terms.Seller, t.Seller) &&
		addrEq(e.terms.Token, t.Token) &&
		e.terms.Drawdown == t.Drawdown &&
		!e.locked &&
		e.terms.TerminationTime > m.nowFn().Unix() &&
		(t.TerminationTime == 0 || e.terms.TerminationTime == t.TerminationTime)
	if t.Drawdown {
		// Headroom check — the presented amount is this task's draw.
		ok = ok && new(big.Int).Add(e.released, t.Amount).Cmp(
			e.terms.Amount) <= 0
	} else {
		ok = ok && e.terms.Amount.Cmp(t.Amount) == 0 &&
			e.released.Sign() == 0 &&
			e.balance.Cmp(e.terms.Amount) >= 0 &&
			(t.TaskHash == ([32]byte{}) || e.terms.TaskHash == t.TaskHash)
	}
	return ok, nil
}

// Release moves the balance to released — the buyer-authorized payout.
func (m *MockRail) Release(_ context.Context, sessionID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.mustEscrow(sessionID)
	if err != nil {
		return "", err
	}
	if e.locked {
		return "", fmt.Errorf("release %s: escrow is locked", sessionID)
	}
	if e.balance.Sign() == 0 {
		return "", fmt.Errorf("release %s: balance is zero", sessionID)
	}
	e.released.Add(e.released, e.balance)
	e.balance.SetInt64(0)
	m.txSeq++
	return m.txHash(sessionID, m.txSeq), nil
}

// ReleasePartial moves `amount` from balance to released — the drawdown
// primitive the graduated rail adds. The mock implements it amount-wise
// so Track 128 fixtures can exercise partial-release sequences without a
// chain.
func (m *MockRail) ReleasePartial(
	_ context.Context, sessionID string, amount *big.Int,
) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.mustEscrow(sessionID)
	if err != nil {
		return "", err
	}
	if e.locked {
		return "", fmt.Errorf("release %s: escrow is locked", sessionID)
	}
	if amount == nil || amount.Sign() <= 0 {
		return "", fmt.Errorf("release %s: amount must be positive", sessionID)
	}
	if amount.Cmp(e.balance) > 0 {
		return "", fmt.Errorf("release %s: amount exceeds balance", sessionID)
	}
	e.released.Add(e.released, amount)
	e.balance.Sub(e.balance, amount)
	m.txSeq++
	return m.txHash(sessionID, m.txSeq), nil
}

// Claim locks the escrow — the provider-initiated forced-resolution path
// (see Rail.Claim for the semantic mapping).
func (m *MockRail) Claim(_ context.Context, sessionID string) (string, error) {
	return m.lock(sessionID, bytes32Label("claim"))
}

// Dispute locks the escrow with caller-supplied details.
func (m *MockRail) Dispute(_ context.Context, sessionID string, details [32]byte) (string, error) {
	return m.lock(sessionID, details)
}

// Withdraw refunds the balance to the client once terminationTime has
// lapsed — the contract's buyer safety valve.
func (m *MockRail) Withdraw(_ context.Context, sessionID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.mustEscrow(sessionID)
	if err != nil {
		return "", err
	}
	switch {
	case e.locked:
		return "", fmt.Errorf("withdraw %s: escrow is locked", sessionID)
	case e.balance.Sign() == 0:
		return "", fmt.Errorf("withdraw %s: balance is zero", sessionID)
	case e.terms.TerminationTime > m.nowFn().Unix():
		return "", fmt.Errorf("withdraw %s: escrow still live until %d",
			sessionID, e.terms.TerminationTime)
	}
	e.balance.SetInt64(0)
	m.txSeq++
	return m.txHash(sessionID, m.txSeq), nil
}

func (m *MockRail) lock(sessionID string, details [32]byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.mustEscrow(sessionID)
	if err != nil {
		return "", err
	}
	switch {
	case e.locked:
		return "", fmt.Errorf("lock %s: already locked", sessionID)
	case e.balance.Sign() == 0:
		return "", fmt.Errorf("lock %s: balance is zero", sessionID)
	case e.terms.TerminationTime <= m.nowFn().Unix():
		return "", fmt.Errorf("lock %s: terminated", sessionID)
	}
	e.locked = true
	e.lockDetails = details
	m.txSeq++
	return m.txHash(sessionID, m.txSeq), nil
}

// Resolve applies the arbiter award with the contract's exact-sum
// constraint: awards must total balance − balance/DefaultResolutionRate.
func (m *MockRail) Resolve(
	_ context.Context,
	sessionID string,
	clientAward, providerAward *big.Int,
	_ [32]byte,
) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.mustEscrow(sessionID)
	if err != nil {
		return "", err
	}
	if !e.locked {
		return "", fmt.Errorf("resolve %s: not locked", sessionID)
	}
	if e.balance.Sign() == 0 {
		return "", fmt.Errorf("resolve %s: balance is zero", sessionID)
	}
	fee := new(big.Int).Div(e.balance, big.NewInt(DefaultResolutionRate))
	want := new(big.Int).Sub(e.balance, fee)
	got := new(big.Int).Add(clientAward, providerAward)
	if got.Cmp(want) != 0 {
		return "", fmt.Errorf("resolve %s: awards %s != balance-fee %s", sessionID, got, want)
	}
	if clientAward.Sign() < 0 || providerAward.Sign() < 0 {
		return "", fmt.Errorf("resolve %s: negative award", sessionID)
	}
	// Matches the contract: resolve() does not bump `released`.
	e.balance.SetInt64(0)
	e.locked = false
	e.resolved = true
	m.txSeq++
	return m.txHash(sessionID, m.txSeq), nil
}

// EscrowBalance reports the escrow's token balance — a test helper for
// 102/103 so the clawback posture is inspectable without chain state.
func (m *MockRail) EscrowBalance(sessionID string) (*big.Int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.mustEscrow(sessionID)
	if err != nil {
		return nil, err
	}
	return new(big.Int).Set(e.balance), nil
}

// EscrowLocked reports the lock flag — a test helper for 102/103.
func (m *MockRail) EscrowLocked(sessionID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.mustEscrow(sessionID)
	if err != nil {
		return false, err
	}
	return e.locked, nil
}

func (m *MockRail) mustEscrow(sessionID string) (*mockEscrow, error) {
	e, ok := m.escrows[mockKey(sessionID)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	return e, nil
}

// mockKey normalizes escrow map keys — EVM addresses are
// case-insensitive, so a buyer presenting the lowercase form of a
// checksummed session_id must still hit the lock.
func mockKey(addr string) string { return strings.ToLower(addr) }

func (m *MockRail) txHash(seed string, seq int) string {
	h := web3.Keccak256([]byte(fmt.Sprintf("mocktx:%s:%d", seed, seq)))
	return "0x" + hex.EncodeToString(h)
}
