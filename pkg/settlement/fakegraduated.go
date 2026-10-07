package settlement

import (
	"encoding/hex"
	"math/big"
	"strconv"
	"testing"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// FakeChain graduated mode — models contracts/RhizomeEscrow.sol (the
// v0.16.0 Track 127 contract): session-keyed escrow state inside one
// contract at cfg.Factory, ERC-20 transferFrom funding (approve-first),
// amount-based releases, native seller claim after the deadline, buyer
// withdraw after deadline + CLAIM_GRACE, arbiter resolve on locked
// sessions. Events emit with the contract address and the sessionId as
// topic[1] — the real contract's indexed-signature shape.
//
// Tests select the mode with NewFakeGraduatedChain; the RPC surface is
// identical — only the contract semantics differ.

const graduatedClaimGrace = int64(14 * 24 * 3600) // mirrors CLAIM_GRACE

// gradSession is the fake's per-session state (mirrors the contract's
// Session struct).
type gradSession struct {
	buyer, seller, arbiter, token string
	amount, released              *big.Int
	deadline                      int64
	status                        uint8 // graduatedStatus* constants
	taskHash                      [32]byte
	drawdown                      bool // remainder refunds to buyer; no seller claim
}

// NewFakeGraduatedChain starts a FakeChain that answers the graduated
// contract's surface at cfg.Factory (escrow_rail=rhizome posture).
func NewFakeGraduatedChain(t *testing.T, cfg RailConfig) *FakeChain {
	fc := NewFakeChain(t, cfg)
	fc.mu.Lock()
	fc.gradMode = true
	fc.gradSessions = map[string]*gradSession{}
	fc.mu.Unlock()
	return fc
}

// GraduatedSnapshot returns a session's fake state for assertions.
func (fc *FakeChain) GraduatedSnapshot(sessionID string) (gradSession, bool) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	s, ok := fc.gradSessions[normSessionID(sessionID)]
	if !ok {
		return gradSession{}, false
	}
	return *s, true
}

// normSessionID normalizes a 0x-hex session id for map keying.
func normSessionID(s string) string {
	sid, err := parseSessionID(s)
	if err != nil {
		return s
	}
	return hex.EncodeToString(sid[:])
}

// gradView answers eth_call reads on the graduated contract.
func (fc *FakeChain) gradView(data []byte) (any, *rpcError) {
	s := hex.EncodeToString(data[:4])
	if s == hex.EncodeToString(sel("sessionOf", graduatedABI, 1)) ||
		s == hex.EncodeToString(sel("sessions", graduatedABI, 1)) {
		args, rerr := fc.decodeArgs([]string{"bytes32"}, data)
		if rerr != nil {
			return nil, rerr
		}
		sid := normSessionID(mustStr(args[0]))
		e := fc.gradSessions[sid]
		if e == nil {
			// Unopened slot — the real mapping getter returns zeros.
			const zero = "0x0000000000000000000000000000000000000000"
			e = &gradSession{
				buyer: zero, seller: zero, arbiter: zero, token: zero,
				amount: new(big.Int), released: new(big.Int),
			}
		}
		return encRet(
			[]string{
				"address", "address", "address", "address",
				"uint128", "uint128", "uint64", "uint8", "bytes32", "bool",
			},
			[]any{
				e.buyer, e.seller, e.arbiter, e.token,
				e.amount.String(), e.released.String(),
				itoa(e.deadline), itoa(int64(e.status)),
				"0x" + hex.EncodeToString(e.taskHash[:]), e.drawdown,
			})
	}
	return nil, rpcErr(-32601, "fakechain: unknown graduated selector")
}

// gradTx applies a graduated-contract transaction.
func (fc *FakeChain) gradTx(from string, data []byte) ([]*fakeLog, *rpcError) {
	s := hex.EncodeToString(data[:4])
	switch s {
	case hex.EncodeToString(sel("open", graduatedABI, 8)):
		return fc.gradOpen(from, data)
	case hex.EncodeToString(sel("release", graduatedABI, 2)):
		return fc.gradRelease(from, data)
	case hex.EncodeToString(sel("dispute", graduatedABI, 2)):
		return fc.gradDispute(from, data)
	case hex.EncodeToString(sel("resolve", graduatedABI, 4)):
		return fc.gradResolve(from, data)
	case hex.EncodeToString(sel("claim", graduatedABI, 1)):
		return fc.gradClaim(from, data)
	case hex.EncodeToString(sel("withdraw", graduatedABI, 1)):
		return fc.gradWithdraw(from, data)
	case hex.EncodeToString(sel("submitEvidence", graduatedABI, 2)):
		return fc.gradEvidence(from, data)
	}
	return nil, rpcErr(-32601, "fakechain: unsupported graduated tx selector")
}

func (fc *FakeChain) gradOpen(from string, data []byte) ([]*fakeLog, *rpcError) {
	args, rerr := fc.decodeArgs(
		[]string{
			"bytes32", "address", "address", "uint128",
			"bytes32", "uint64", "address", "bool",
		}, data)
	if rerr != nil {
		return nil, rerr
	}
	sid := normSessionID(mustStr(args[0]))
	seller := normAddr(mustStr(args[1]))
	token := normAddr(mustStr(args[2]))
	amount := mustBig(args[3])
	var taskHash [32]byte
	if b, err := hex.DecodeString(trimHex(mustStr(args[4]))); err == nil {
		copy(taskHash[:], b)
	}
	window := mustBig(args[5]).Int64()
	arbiter := normAddr(mustStr(args[6]))
	drawdown, _ := args[7].(bool)

	if fc.gradSessions[sid] != nil {
		return nil, revert("session exists")
	}
	if seller == "" || seller == normAddr(from) || token == "" || arbiter == "" ||
		amount.Sign() <= 0 || window < 3600 || window > 365*86400 {
		return nil, revert("bad open args")
	}
	// transferFrom(buyer, contract, amount) — honors the recorded
	// approve() allowance like the real contract does: the spender is
	// the contract itself (it initiates the token call inside open()).
	if !fc.tokenPull(
		token, normAddr(from), normAddr(fc.factory), normAddr(fc.factory), amount) {
		return nil, revert("funding transfer failed")
	}
	fc.gradSessions[sid] = &gradSession{
		buyer: normAddr(from), seller: seller, arbiter: arbiter,
		token: token, amount: amount, released: new(big.Int),
		deadline: fc.now + window, status: graduatedStatusOpen,
		taskHash: taskHash, drawdown: drawdown,
	}
	return []*fakeLog{{
		address: normAddr(fc.factory),
		topics: []string{
			topicOf("Opened(bytes32,address,address,address,uint128,uint64,bytes32)"),
			topicBytes32Str(sid), topicAddr(from), topicAddr(seller),
		},
		data: "0x" + hex.EncodeToString(concat(
			wordAddr(token), wordBig(amount), wordBig(big.NewInt(fc.now+window)),
			wordBytes32(taskHash))),
	}}, nil
}

func (fc *FakeChain) gradRelease(from string, data []byte) ([]*fakeLog, *rpcError) {
	args, rerr := fc.decodeArgs([]string{"bytes32", "uint128"}, data)
	if rerr != nil {
		return nil, rerr
	}
	e, rerr := fc.mustGrad(data, from)
	if rerr != nil {
		return nil, rerr
	}
	if from != e.buyer {
		return nil, revert("buyer only")
	}
	if fc.now > e.deadline {
		return nil, revert("window lapsed")
	}
	amount := mustBig(args[1])
	if amount.Sign() <= 0 ||
		new(big.Int).Add(e.released, amount).Cmp(e.amount) > 0 {
		return nil, revert("bad amount")
	}
	e.released.Add(e.released, amount)
	fc.tokenMove(e.token, normAddr(fc.factory), e.seller, amount)
	return []*fakeLog{{
		address: normAddr(fc.factory),
		topics: []string{
			topicOf("Released(bytes32,uint128,uint128)"),
			topicBytes32Str(normSessionID(mustStr(args[0]))),
		},
		data: "0x" + hex.EncodeToString(concat(wordBig(amount), wordBig(e.released))),
	}}, nil
}

func (fc *FakeChain) gradDispute(from string, data []byte) ([]*fakeLog, *rpcError) {
	args, rerr := fc.decodeArgs([]string{"bytes32", "bytes32"}, data)
	if rerr != nil {
		return nil, rerr
	}
	e, rerr := fc.mustGrad(data, from)
	if rerr != nil {
		return nil, rerr
	}
	if from != e.buyer && from != e.seller {
		return nil, revert("party only")
	}
	if fc.now > e.deadline {
		return nil, revert("window lapsed")
	}
	e.status = graduatedStatusLocked
	var evh [32]byte
	if b, err := hex.DecodeString(trimHex(mustStr(args[1]))); err == nil {
		copy(evh[:], b)
	}
	return []*fakeLog{{
		address: normAddr(fc.factory),
		topics: []string{
			topicOf("Disputed(bytes32,address,bytes32)"),
			topicBytes32Str(normSessionID(mustStr(args[0]))), topicAddr(from),
		},
		data: "0x" + hex.EncodeToString(evh[:]),
	}}, nil
}

func (fc *FakeChain) gradResolve(from string, data []byte) ([]*fakeLog, *rpcError) {
	args, rerr := fc.decodeArgs(
		[]string{"bytes32", "uint128", "uint128", "bytes32"}, data)
	if rerr != nil {
		return nil, rerr
	}
	sid := normSessionID(mustStr(args[0]))
	e := fc.gradSessions[sid]
	if e == nil || e.status != graduatedStatusLocked {
		return nil, revert("not locked")
	}
	if from != e.arbiter {
		return nil, revert("arbiter only")
	}
	cA, pA := mustBig(args[1]), mustBig(args[2])
	remaining := new(big.Int).Sub(e.amount, e.released)
	if new(big.Int).Add(cA, pA).Cmp(remaining) != 0 {
		return nil, revert("awards must equal balance")
	}
	e.status = graduatedStatusResolved
	if pA.Sign() > 0 {
		fc.tokenMove(e.token, normAddr(fc.factory), e.seller, pA)
	}
	if cA.Sign() > 0 {
		fc.tokenMove(e.token, normAddr(fc.factory), e.buyer, cA)
	}
	var ruling [32]byte
	if b, err := hex.DecodeString(trimHex(mustStr(args[3]))); err == nil {
		copy(ruling[:], b)
	}
	return []*fakeLog{{
		address: normAddr(fc.factory),
		topics: []string{
			topicOf("Resolved(bytes32,uint128,uint128,bytes32)"),
			topicBytes32Str(sid),
		},
		data: "0x" + hex.EncodeToString(
			concat(wordBig(cA), wordBig(pA), wordBytes32(ruling))),
	}}, nil
}

func (fc *FakeChain) gradClaim(from string, data []byte) ([]*fakeLog, *rpcError) {
	args, rerr := fc.decodeArgs([]string{"bytes32"}, data)
	if rerr != nil {
		return nil, rerr
	}
	e, rerr := fc.mustGrad(data, from)
	if rerr != nil {
		return nil, rerr
	}
	if from != e.seller {
		return nil, revert("seller only")
	}
	if fc.now <= e.deadline {
		return nil, revert("window still running")
	}
	if e.drawdown {
		return nil, revert("drawdown remainder refunds to buyer")
	}
	remaining := new(big.Int).Sub(e.amount, e.released)
	e.status = graduatedStatusClosed
	if remaining.Sign() > 0 {
		fc.tokenMove(e.token, normAddr(fc.factory), e.seller, remaining)
	}
	return []*fakeLog{{
		address: normAddr(fc.factory),
		topics: []string{
			topicOf("Claimed(bytes32,uint128)"),
			topicBytes32Str(normSessionID(mustStr(args[0]))),
		},
		data: "0x" + hex.EncodeToString(wordBig(remaining)),
	}}, nil
}

func (fc *FakeChain) gradWithdraw(from string, data []byte) ([]*fakeLog, *rpcError) {
	args, rerr := fc.decodeArgs([]string{"bytes32"}, data)
	if rerr != nil {
		return nil, rerr
	}
	e, rerr := fc.mustGrad(data, from)
	if rerr != nil {
		return nil, rerr
	}
	if from != e.buyer {
		return nil, revert("buyer only")
	}
	refundAt := e.deadline
	if !e.drawdown {
		refundAt += graduatedClaimGrace // grace exists for seller claim rights
	}
	if fc.now <= refundAt {
		return nil, revert("refund window still running")
	}
	remaining := new(big.Int).Sub(e.amount, e.released)
	e.status = graduatedStatusClosed
	if remaining.Sign() > 0 {
		fc.tokenMove(e.token, normAddr(fc.factory), e.buyer, remaining)
	}
	return []*fakeLog{{
		address: normAddr(fc.factory),
		topics: []string{
			topicOf("Withdrawn(bytes32,uint128)"),
			topicBytes32Str(normSessionID(mustStr(args[0]))),
		},
		data: "0x" + hex.EncodeToString(wordBig(remaining)),
	}}, nil
}

func (fc *FakeChain) gradEvidence(from string, data []byte) ([]*fakeLog, *rpcError) {
	args, rerr := fc.decodeArgs([]string{"bytes32", "string"}, data)
	if rerr != nil {
		return nil, rerr
	}
	sid := normSessionID(mustStr(args[0]))
	e := fc.gradSessions[sid]
	if e == nil || e.status != graduatedStatusLocked {
		return nil, revert("not locked")
	}
	if from != e.buyer && from != e.seller {
		return nil, revert("party only")
	}
	enc, encErr := web3.EncodeABIArguments(
		[]*web3.ABIType{mustType("string")}, []any{mustStr(args[1])})
	if encErr != nil {
		return nil, rpcErr(-32000, "fakechain: evidence encode: "+encErr.Error())
	}
	return []*fakeLog{{
		address: normAddr(fc.factory),
		topics: []string{
			topicOf("Evidence(address,uint256,address,string)"),
			topicAddr(e.arbiter), topicBytes32Str(sid), topicAddr(from),
		},
		data: "0x" + hex.EncodeToString(enc),
	}}, nil
}

// mustGrad loads a session by the calldata's first arg and enforces the
// "must be Open" guard the contract applies to party verbs.
func (fc *FakeChain) mustGrad(data []byte, _ string) (*gradSession, *rpcError) {
	args, rerr := fc.decodeArgs([]string{"bytes32"}, data)
	if rerr != nil {
		return nil, rerr
	}
	e := fc.gradSessions[normSessionID(mustStr(args[0]))]
	if e == nil || e.status != graduatedStatusOpen {
		return nil, revert("not open")
	}
	return e, nil
}

// tokenPull mirrors transferFrom: spends allowance[owner][spender] and
// moves owner → dest. Returns false like a reverting ERC-20 when the
// allowance or balance doesn't cover the amount.
func (fc *FakeChain) tokenPull(
	token, owner, spender, dest string, amount *big.Int,
) bool {
	l := fc.tokens[token]
	if l == nil {
		return false
	}
	allow := fc.allowances[token][normAddr(owner)][normAddr(spender)]
	if allow == nil || allow.Cmp(amount) < 0 {
		return false
	}
	f, t := normAddr(owner), normAddr(dest)
	if l[f] == nil || l[f].Cmp(amount) < 0 {
		return false
	}
	allow.Sub(allow, amount)
	l[f].Sub(l[f], amount)
	if l[t] == nil {
		l[t] = new(big.Int)
	}
	l[t].Add(l[t], amount)
	return true
}

// --- small encode/topic helpers shared by both fake modes --------------

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func concat(chunks ...[]byte) []byte {
	var out []byte
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

func wordAddr(addr string) []byte {
	w := make([]byte, 32)
	raw, _ := hex.DecodeString(trimHex(addr))
	copy(w[32-len(raw):], raw)
	return w
}

func wordBytes32(b [32]byte) []byte {
	w := make([]byte, 32)
	copy(w, b[:])
	return w
}

// topicBytes32Str renders a bytes32 log topic from a hex string
// (normSessionID output) — complements topicBytes32([32]byte).
func topicBytes32Str(hexStr string) string {
	raw, _ := hex.DecodeString(trimHex("0x" + hexStr))
	w := make([]byte, 32)
	copy(w[32-len(raw):], raw)
	return "0x" + hex.EncodeToString(w)
}
