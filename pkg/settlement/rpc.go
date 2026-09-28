package settlement

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// Sender is the rail's transport seam: reads, transactions, receipts,
// and the chain's clock (block time — the clock contracts reason about,
// not the local wall clock). DirectSender covers plain JSON-RPC
// endpoints; Track 104 adds a WalletSender that signs locally.
type Sender interface {
	// Call performs eth_call and returns the raw return data.
	Call(ctx context.Context, to string, data []byte) ([]byte, error)
	// SendTx submits a transaction and returns its hash.
	SendTx(ctx context.Context, to string, data []byte, valueWei *big.Int) (txHash string, err error)
	// Receipt fetches a mined receipt (nil while pending).
	Receipt(ctx context.Context, txHash string) (*web3.Receipt, error)
	// Now returns the latest block's unix timestamp.
	Now(ctx context.Context) (int64, error)
}

// DirectSender talks to a JSON-RPC endpoint whose node can send
// transactions for `from` directly (unlocked/dev/test nodes — anvil,
// hardhat, FakeChain). Key-management senders are a Track 104 concern.
type DirectSender struct {
	client *web3.Client
	from   string
}

// NewDirectSender builds a Sender over c; `from` is the sender address
// stamped on transactions (and the party identity the contract checks —
// client for release, provider-or-client for lock, resolver for
// resolve).
func NewDirectSender(c *web3.Client, from string) *DirectSender {
	return &DirectSender{client: c, from: from}
}

// From reports the configured sender address.
func (s *DirectSender) From() string { return s.from }

// SetFrom swaps the sender address — tests drive different parties
// (buyer/seller/arbiter) through one endpoint by rotating it.
func (s *DirectSender) SetFrom(from string) { s.from = from }

func (s *DirectSender) Call(ctx context.Context, to string, data []byte) ([]byte, error) {
	params := []any{
		map[string]any{"to": to, "data": "0x" + hex.EncodeToString(data)},
		"latest",
	}
	raw, err := s.client.Call(ctx, "eth_call", params)
	if err != nil {
		return nil, err
	}
	var hexRet string
	if err := json.Unmarshal(raw, &hexRet); err != nil {
		return nil, fmt.Errorf("eth_call to %s: %w", to, err)
	}
	out, err := web3.ParseHexBytes(hexRet)
	if err != nil {
		return nil, fmt.Errorf("eth_call to %s: %w", to, err)
	}
	return out, nil
}

func (s *DirectSender) SendTx(ctx context.Context, to string, data []byte, valueWei *big.Int) (string, error) {
	tx := map[string]any{
		"from": s.from,
		"to":   to,
		"data": "0x" + hex.EncodeToString(data),
	}
	if valueWei != nil && valueWei.Sign() > 0 {
		tx["value"] = web3.QuantityString(valueWei)
	}
	raw, err := s.client.Call(ctx, "eth_sendTransaction", []any{tx})
	if err != nil {
		return "", err
	}
	var hash string
	if err := json.Unmarshal(raw, &hash); err != nil {
		return "", fmt.Errorf("eth_sendTransaction result: %w", err)
	}
	return hash, nil
}

func (s *DirectSender) Receipt(ctx context.Context, txHash string) (*web3.Receipt, error) {
	raw, err := s.client.Call(ctx, "eth_getTransactionReceipt", []any{txHash})
	if err != nil {
		return nil, err
	}
	return web3.DecodeReceipt(raw)
}

func (s *DirectSender) Now(ctx context.Context) (int64, error) {
	raw, err := s.client.Call(ctx, "eth_getBlockByNumber", []any{"latest", false})
	if err != nil {
		return 0, err
	}
	var blk struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(raw, &blk); err != nil {
		return 0, fmt.Errorf("eth_getBlockByNumber result: %w", err)
	}
	ts, err := web3.QuantityUint64(blk.Timestamp)
	if err != nil {
		return 0, fmt.Errorf("block timestamp %q: %w", blk.Timestamp, err)
	}
	if ts > uint64(1<<62) {
		return 0, fmt.Errorf("block timestamp %d overflows int64", ts)
	}
	return int64(ts), nil
}

// RPCRail is the Smart Invoice-backed SettlementRail. One RailConfig
// pins factory/token/arbiter/window; per-session facts ride in Terms.
type RPCRail struct {
	cfg       RailConfig
	snd       Sender
	nowFn     func(ctx context.Context) (int64, error) // seam: defaults to snd.Now
	confirmTo time.Duration

	decMu    sync.Mutex
	decimals *uint8 // cached TokenDecimals result — token decimals are immutable
}

// NewRPCRail validates cfg and returns the rail.
func NewRPCRail(cfg RailConfig, snd Sender) (*RPCRail, error) {
	if snd == nil {
		return nil, fmt.Errorf("settlement: nil Sender")
	}
	switch {
	case cfg.ChainID == 0:
		return nil, fmt.Errorf("settlement: chain id required")
	case !web3.IsAddress(cfg.Factory):
		return nil, fmt.Errorf("settlement: factory %q is not a 0x address", cfg.Factory)
	case !web3.IsAddress(cfg.Arbiter):
		return nil, fmt.Errorf("settlement: arbiter %q is not a 0x address", cfg.Arbiter)
	case !web3.IsAddress(cfg.WrappedNative):
		return nil, fmt.Errorf("settlement: wrapped native %q is not a 0x address", cfg.WrappedNative)
	case cfg.DisputeWindowSecs <= 0:
		return nil, fmt.Errorf("settlement: dispute window must be positive")
	}
	return &RPCRail{cfg: cfg, snd: snd, nowFn: snd.Now, confirmTo: 2 * time.Minute}, nil
}

// PredictEscrowAddr returns the deterministic escrow address for a
// correlation id — the session_id the market protocol presents — without
// deploying anything (eth_call predictDeterministicAddress).
func (r *RPCRail) PredictEscrowAddr(ctx context.Context, correlationID string) (string, error) {
	m, err := factoryABI.Method("predictDeterministicAddress", 2)
	if err != nil {
		return "", err
	}
	data, err := m.PackArgs([]any{bytes32Hex(InvoiceTypeEscrow), bytes32Hex(CorrelationSalt(correlationID))})
	if err != nil {
		return "", err
	}
	out, err := r.snd.Call(ctx, r.cfg.Factory, data)
	if err != nil {
		return "", fmt.Errorf("predict escrow address: %w", err)
	}
	vals, err := m.UnpackOutputs(out)
	if err != nil {
		return "", fmt.Errorf("predict escrow address: %w", err)
	}
	addr, _ := vals[0].(string)
	if !web3.IsAddress(addr) {
		return "", fmt.Errorf("predict escrow address: bad result %v", vals[0])
	}
	return addr, nil
}

// Open deploys the session escrow and funds it. terminationTime is
// t.TerminationTime when set, else chain-now + cfg.DisputeWindowSecs
// (the contract rejects deadlines in the past or >2yr out — the rail
// lets the chain speak rather than duplicating its bounds).
func (r *RPCRail) Open(ctx context.Context, correlationID string, t Terms) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	escrowAddr, err := r.PredictEscrowAddr(ctx, correlationID)
	if err != nil {
		return "", err
	}
	term := t.TerminationTime
	if term == 0 {
		now, err := r.nowFn(ctx)
		if err != nil {
			return "", fmt.Errorf("open: chain time: %w", err)
		}
		term = now + r.cfg.DisputeWindowSecs
	}
	data, err := r.encodeInitData(t.Buyer, t.Token, term, t.TaskHash)
	if err != nil {
		return "", err
	}
	create, err := factoryABI.Method("createDeterministic", 5)
	if err != nil {
		return "", err
	}
	createData, err := create.PackArgs([]any{
		t.Seller,
		[]any{t.Amount.String()},
		"0x" + hex.EncodeToString(data),
		bytes32Hex(InvoiceTypeEscrow),
		bytes32Hex(CorrelationSalt(correlationID)),
	})
	if err != nil {
		return "", err
	}
	createHash, err := r.snd.SendTx(ctx, r.cfg.Factory, createData, nil)
	if err != nil {
		return "", fmt.Errorf("open: createDeterministic: %w", err)
	}
	if err := r.waitConfirmed(ctx, createHash); err != nil {
		return "", fmt.Errorf("open: createDeterministic %s: %w", createHash, err)
	}
	// Fund: plain ERC-20 transfer of the buyer's own tokens — the escrow
	// accrues balance via balanceOf, no approve dance.
	transfer, err := erc20ABI.Method("transfer", 2)
	if err != nil {
		return "", err
	}
	fundData, err := transfer.PackArgs([]any{escrowAddr, t.Amount.String()})
	if err != nil {
		return "", err
	}
	if _, err := r.snd.SendTx(ctx, t.Token, fundData, nil); err != nil {
		return "", fmt.Errorf("open: funding transfer (escrow %s deployed): %w", escrowAddr, err)
	}
	return createHash, nil
}

// VerifyLock implements the sell-side pre-work gate entirely over
// eth_call: exists, live, unlocked, unreleased, funded, terms-consistent.
func (r *RPCRail) VerifyLock(ctx context.Context, sessionID string, t Terms) (bool, error) {
	if !web3.IsAddress(sessionID) {
		return false, fmt.Errorf("session %q is not a 0x address", sessionID)
	}
	if err := t.Validate(); err != nil {
		return false, err
	}
	token, err := r.viewAddr(ctx, sessionID, "token")
	if err != nil {
		return false, err // includes ErrNotFound for un-deployed addresses
	}
	client, err := r.viewAddr(ctx, sessionID, "client")
	if err != nil {
		return false, err
	}
	provider, err := r.viewAddr(ctx, sessionID, "provider")
	if err != nil {
		return false, err
	}
	total, err := r.viewUint(ctx, sessionID, "total")
	if err != nil {
		return false, err
	}
	released, err := r.viewUint(ctx, sessionID, "released")
	if err != nil {
		return false, err
	}
	locked, err := r.viewBool(ctx, sessionID, "locked")
	if err != nil {
		return false, err
	}
	termination, err := r.viewUint(ctx, sessionID, "terminationTime")
	if err != nil {
		return false, err
	}
	balance, err := r.balanceOf(ctx, token, sessionID)
	if err != nil {
		return false, err
	}
	now, err := r.nowFn(ctx)
	if err != nil {
		return false, fmt.Errorf("verify lock: chain time: %w", err)
	}
	// A non-zero TaskHash additionally binds the escrow to the committed
	// task: the buyer writes it into init `details` at Open (Track 103), so
	// a hash mismatch means this escrow pays for different work.
	detailsOK := true
	if t.TaskHash != ([32]byte{}) {
		details, err := r.viewBytes32(ctx, sessionID, "details")
		if err != nil {
			return false, err
		}
		detailsOK = details == t.TaskHash
	}
	ok := addrEq(token, t.Token) &&
		addrEq(client, t.Buyer) &&
		addrEq(provider, t.Seller) &&
		total.Cmp(t.Amount) == 0 &&
		released.Sign() == 0 &&
		!locked &&
		balance.Cmp(total) >= 0 &&
		termination.Int64() > now &&
		detailsOK &&
		(t.TerminationTime == 0 || termination.Int64() == t.TerminationTime)
	return ok, nil
}

// TokenDecimals resolves the configured payment token's decimals() — the
// exponent market code needs to turn human price strings ("0.50") into
// base units. Cached: decimals never change.
func (r *RPCRail) TokenDecimals(ctx context.Context) (uint8, error) {
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
	n, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		return 0, fmt.Errorf("decimals: bad uint8 output %v", vals[0])
	}
	d := uint8(n)
	r.decimals = &d
	return d, nil
}

// Release sends escrow.release() — client-only on-chain.
func (r *RPCRail) Release(ctx context.Context, sessionID string) (string, error) {
	return r.verb(ctx, sessionID, "release", nil)
}

// Claim sends escrow.lock(claimDetails) — see Rail.Claim for the
// seller-claim semantics of Smart Invoice.
func (r *RPCRail) Claim(ctx context.Context, sessionID string) (string, error) {
	return r.verb(ctx, sessionID, "lock", []any{bytes32Hex(bytes32Label("claim"))})
}

// Dispute sends escrow.lock(details).
func (r *RPCRail) Dispute(ctx context.Context, sessionID string, details [32]byte) (string, error) {
	return r.verb(ctx, sessionID, "lock", []any{bytes32Hex(details)})
}

// Withdraw sends escrow.withdraw() — the client-only refund path that
// unlocks once terminationTime has lapsed.
func (r *RPCRail) Withdraw(ctx context.Context, sessionID string) (string, error) {
	return r.verb(ctx, sessionID, "withdraw", nil)
}

// WithSender returns a rail sharing this rail's config but sending
// through s — buys scope a per-purchase sender (pending-id attribution,
// per-party `from`) without mutating the parent's identity. The decimals
// cache does not carry over (it's per-sender state under a mutex — a
// fresh rail re-reads it once).
func (r *RPCRail) WithSender(s Sender) *RPCRail {
	return &RPCRail{
		cfg: r.cfg, snd: s, nowFn: r.nowFn, confirmTo: r.confirmTo,
	}
}

// Resolve sends escrow.resolve(clientAward, providerAward, details) —
// resolver-only on-chain, INDIVIDUAL resolver type.
func (r *RPCRail) Resolve(
	ctx context.Context,
	sessionID string,
	clientAward, providerAward *big.Int,
	details [32]byte,
) (string, error) {
	if clientAward == nil || providerAward == nil || clientAward.Sign() < 0 || providerAward.Sign() < 0 {
		return "", fmt.Errorf("resolve: awards must be non-negative")
	}
	return r.verb(ctx, sessionID, "resolve",
		[]any{clientAward.String(), providerAward.String(), bytes32Hex(details)})
}

// verb packs a no-arg-or-fixed-arg escrow call and submits it.
func (r *RPCRail) verb(ctx context.Context, sessionID, name string, args []any) (string, error) {
	if !web3.IsAddress(sessionID) {
		return "", fmt.Errorf("session %q is not a 0x address", sessionID)
	}
	m, err := escrowABI.Method(name, len(args))
	if err != nil {
		return "", err
	}
	data, err := m.PackArgs(args)
	if err != nil {
		return "", err
	}
	hash, err := r.snd.SendTx(ctx, sessionID, data, nil)
	if err != nil {
		return "", fmt.Errorf("%s on %s: %w", name, sessionID, err)
	}
	return hash, nil
}

// encodeInitData builds the 9-field init tuple Smart Invoice's
// _handleData decodes: (client, resolverType, resolver, token,
// terminationTime, details, wrappedNativeToken, requireVerification,
// factory). All static — a flat EncodeABIArguments word run.
func (r *RPCRail) encodeInitData(client, token string, terminationTime int64, details [32]byte) ([]byte, error) {
	types := []*web3.ABIType{
		mustType("address"), mustType("uint8"), mustType("address"), mustType("address"),
		mustType("uint256"), mustType("bytes32"), mustType("address"), mustType("bool"),
		mustType("address"),
	}
	return web3.EncodeABIArguments(types, []any{
		client,
		"0", // ADR.INDIVIDUAL — designated-address resolver (v1 arbiter model)
		r.cfg.Arbiter,
		token,
		strconv.FormatInt(terminationTime, 10),
		bytes32Hex(details),
		r.cfg.WrappedNative,
		false, // requireVerification=false: client needn't verify() before lock
		r.cfg.Factory,
	})
}

func mustType(canonical string) *web3.ABIType {
	t, err := web3.ParseABIType(canonical)
	if err != nil {
		panic(fmt.Sprintf("settlement: type %s: %v", canonical, err))
	}
	return t
}

// viewAddr/viewUint/viewBool run a no-arg getter and type-assert the
// decoded output. A call against an un-deployed address returns empty
// data — surfaced as ErrNotFound, not a decode error.
func (r *RPCRail) viewAddr(ctx context.Context, escrow, name string) (string, error) {
	v, err := r.view(ctx, escrow, name)
	if err != nil {
		return "", err
	}
	s, _ := v.(string)
	if !web3.IsAddress(s) {
		return "", fmt.Errorf("%s: bad address output %v", name, v)
	}
	return s, nil
}

func (r *RPCRail) viewUint(ctx context.Context, escrow, name string) (*big.Int, error) {
	v, err := r.view(ctx, escrow, name)
	if err != nil {
		return nil, err
	}
	s, _ := v.(string)
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("%s: bad uint output %v", name, v)
	}
	return n, nil
}

func (r *RPCRail) viewBool(ctx context.Context, escrow, name string) (bool, error) {
	v, err := r.view(ctx, escrow, name)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s: bad bool output %v", name, v)
	}
	return b, nil
}

func (r *RPCRail) viewBytes32(ctx context.Context, escrow, name string) ([32]byte, error) {
	var out [32]byte
	v, err := r.view(ctx, escrow, name)
	if err != nil {
		return out, err
	}
	s, _ := v.(string)
	raw, err := web3.ParseHexBytes(s)
	if err != nil || len(raw) != 32 {
		return out, fmt.Errorf("%s: bad bytes32 output %v", name, v)
	}
	copy(out[:], raw)
	return out, nil
}

func (r *RPCRail) view(ctx context.Context, escrow, name string) (any, error) {
	m, err := escrowABI.Method(name, 0)
	if err != nil {
		return nil, err
	}
	data, err := m.PackArgs(nil)
	if err != nil {
		return nil, err
	}
	out, err := r.snd.Call(ctx, escrow, data)
	if err != nil {
		return nil, fmt.Errorf("%s on %s: %w", name, escrow, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s on %s: %w", name, escrow, ErrNotFound)
	}
	vals, err := m.UnpackOutputs(out)
	if err != nil {
		return nil, fmt.Errorf("%s on %s: %w", name, escrow, err)
	}
	return vals[0], nil
}

func (r *RPCRail) balanceOf(ctx context.Context, token, account string) (*big.Int, error) {
	m, err := erc20ABI.Method("balanceOf", 1)
	if err != nil {
		return nil, err
	}
	data, err := m.PackArgs([]any{account})
	if err != nil {
		return nil, err
	}
	out, err := r.snd.Call(ctx, token, data)
	if err != nil {
		return nil, fmt.Errorf("balanceOf on %s: %w", token, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("balanceOf on %s: %w", token, ErrNotFound)
	}
	vals, err := m.UnpackOutputs(out)
	if err != nil {
		return nil, err
	}
	s, _ := vals[0].(string)
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("balanceOf: bad uint output %v", vals[0])
	}
	return n, nil
}

// waitConfirmed polls for a receipt until mined or the confirmation
// window lapses; a 0x0 status maps to ErrReverted.
func (r *RPCRail) waitConfirmed(ctx context.Context, hash string) error {
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

// bytes32Hex renders a [32]byte as 0x-prefixed hex (the encoder's
// bytesN input form).
func bytes32Hex(b [32]byte) string { return "0x" + hex.EncodeToString(b[:]) }

// addrEq compares two 0x addresses case-insensitively (checksum-insensitive).
func addrEq(a, b string) bool { return strings.EqualFold(a, b) }
