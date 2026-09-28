package settlement

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// FakeChain is a self-contained JSON-RPC endpoint emulating the Smart
// Invoice surface the RPCRail drives: factory createDeterministic +
// predictDeterministicAddress + resolutionRateOf, the per-session
// escrow's getters and lifecycle verbs (release/lock/resolve/withdraw
// with the real contract's guards), and a bare-bones ERC-20 ledger per
// deployed token. It gives tests deterministic settlement E2E without a
// live chain, wallet, or network.
//
// Transaction model: eth_sendTransaction applies state immediately and
// mines a success receipt (anvil posture); guard violations return a
// JSON-RPC revert error at send time. The block clock is a controllable
// seam (SetNow/Advance) — block.timestamp, not wall time.
type FakeChain struct {
	srv *httptest.Server

	mu       sync.Mutex
	chainID  uint64
	factory  string
	wrapped  string
	now      int64
	block    uint64
	txSeq    int
	escrows  map[string]*fakeEscrow
	tokens   map[string]map[string]*big.Int // token -> holder -> balance
	rates    map[string]int64               // resolver -> resolutionRate
	receipts map[string]*fakeReceipt
	logs     []*fakeLog
	nonces   map[string]uint64 // sender -> tx count (eth_getTransactionCount)
}

type fakeEscrow struct {
	client, provider, resolver, token, wrapped string
	resolverType                               int
	terminationTime, resolutionRate            int64
	details                                    [32]byte
	amounts                                    []*big.Int
	total, released                            *big.Int
	milestone                                  int
	locked                                     bool
}

type fakeReceipt struct {
	status string // "0x1"|"0x0"
	block  uint64
	logs   []*fakeLog
}

type fakeLog struct {
	address string
	topics  []string
	data    string
	block   uint64
	txHash  string
	index   int
}

// NewFakeChain starts the endpoint; cfg supplies chain id, factory
// address, and wrapped-native token (init-time requirement). The caller
// drives time via SetNow/Advance.
func NewFakeChain(t *testing.T, cfg RailConfig) *FakeChain {
	t.Helper()
	fc := &FakeChain{
		chainID:  cfg.ChainID,
		factory:  cfg.Factory,
		wrapped:  cfg.WrappedNative,
		now:      time.Now().Unix(),
		escrows:  map[string]*fakeEscrow{},
		tokens:   map[string]map[string]*big.Int{},
		rates:    map[string]int64{},
		receipts: map[string]*fakeReceipt{},
		nonces:   map[string]uint64{},
	}
	fc.srv = httptest.NewServer(http.HandlerFunc(fc.handle))
	t.Cleanup(fc.srv.Close)
	return fc
}

// Endpoint is the JSON-RPC URL to point a web3.Client at.
func (fc *FakeChain) Endpoint() string { return fc.srv.URL }

// SetNow pins the block timestamp (unix seconds).
func (fc *FakeChain) SetNow(unix int64) { fc.mu.Lock(); fc.now = unix; fc.mu.Unlock() }

// Advance moves the block clock forward — drives termination boundaries.
func (fc *FakeChain) Advance(secs int64) { fc.mu.Lock(); fc.now += secs; fc.mu.Unlock() }

// DeployToken registers an ERC-20 ledger at addr (any address becomes a
// token — the fake doesn't model code).
func (fc *FakeChain) DeployToken(addr string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.deployTokenLocked(addr)
}

func (fc *FakeChain) deployTokenLocked(addr string) {
	if fc.tokens[normAddr(addr)] == nil {
		fc.tokens[normAddr(addr)] = map[string]*big.Int{}
	}
}

// Mint credits a token balance — the test's funding seam.
func (fc *FakeChain) Mint(token, holder string, amount *big.Int) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.deployTokenLocked(token)
	l := fc.tokens[normAddr(token)]
	h := normAddr(holder)
	if l[h] == nil {
		l[h] = new(big.Int)
	}
	l[h].Add(l[h], amount)
}

// Balance reports a token balance.
func (fc *FakeChain) Balance(token, holder string) *big.Int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if b := fc.tokens[normAddr(token)][normAddr(holder)]; b != nil {
		return new(big.Int).Set(b)
	}
	return new(big.Int)
}

// SetResolutionRate configures a resolver's rate (the factory's
// resolutionRateOf); zero mirrors the contract's default-20 fallback.
func (fc *FakeChain) SetResolutionRate(resolver string, rate int64) {
	fc.mu.Lock()
	fc.rates[normAddr(resolver)] = rate
	fc.mu.Unlock()
}

// EscrowAddr computes the deterministic address a createDeterministic
// with (InvoiceTypeEscrow, CorrelationSalt(correlationID)) deploys to —
// the fake mirrors RPCRail's predictDeterministicAddress scheme so the
// rail's predict-and-open path is exercised end to end.
func (fc *FakeChain) EscrowAddr(correlationID string) string {
	return fc.escrowAddrFor(InvoiceTypeEscrow, CorrelationSalt(correlationID))
}

// EscrowSnapshot returns a copy of a deployed escrow's state — test
// introspection that takes the lock rather than racing the handler.
func (fc *FakeChain) EscrowSnapshot(addr string) (fakeEscrow, bool) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	e, ok := fc.escrows[normAddr(addr)]
	if !ok {
		return fakeEscrow{}, false
	}
	return *e, true
}

func (fc *FakeChain) escrowAddrFor(typ, salt [32]byte) string {
	h := web3.Keccak256(append(append([]byte("fakechain:"), typ[:]...), salt[:]...))
	return web3.ChecksumAddress(h[12:])
}

// --- JSON-RPC plumbing -------------------------------------------------

type rpcReq struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
	ID     json.RawMessage   `json:"id"`
}

func (fc *FakeChain) handle(w http.ResponseWriter, r *http.Request) {
	var req rpcReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fc.reply(w, nil, nil, rpcErr(-32700, "parse error"))
		return
	}
	fc.mu.Lock()
	res, rerr := fc.dispatch(req)
	fc.mu.Unlock()
	fc.reply(w, req.ID, res, rerr)
}

type rpcError struct {
	code    int
	message string
}

func (e *rpcError) Error() string { return e.message }

func rpcErr(code int, msg string) *rpcError { return &rpcError{code, msg} }
func revert(msg string) *rpcError           { return rpcErr(3, "execution reverted: "+msg) }

func (fc *FakeChain) reply(w http.ResponseWriter, id json.RawMessage, result any, rerr *rpcError) {
	w.Header().Set("Content-Type", "application/json")
	if len(id) == 0 {
		id = json.RawMessage("1")
	}
	resp := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		resp["error"] = map[string]any{"code": rerr.code, "message": rerr.message}
	} else {
		resp["result"] = result
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (fc *FakeChain) dispatch(req rpcReq) (any, *rpcError) {
	switch req.Method {
	case "eth_chainId":
		return "0x" + strconv.FormatUint(fc.chainID, 16), nil
	case "eth_blockNumber":
		return "0x" + strconv.FormatUint(fc.block, 16), nil
	case "eth_getBlockByNumber":
		return map[string]any{
			"number":    "0x" + strconv.FormatUint(fc.block, 16),
			"timestamp": "0x" + strconv.FormatInt(fc.now, 16),
		}, nil
	case "eth_call":
		return fc.ethCall(req.Params)
	case "eth_sendTransaction":
		return fc.ethSendTransaction(req.Params)
	case "eth_sendRawTransaction":
		return fc.ethSendRawTransaction(req.Params)
	case "eth_getTransactionCount":
		return fc.ethGetTransactionCount(req.Params)
	case "eth_estimateGas":
		return "0x186a0", nil // 100 000 — enough headroom for escrow verbs
	case "eth_gasPrice":
		return "0x3b9aca00", nil // 1 gwei — legacy pricing path
	case "eth_getTransactionReceipt":
		return fc.ethGetReceipt(req.Params)
	case "eth_getLogs":
		return fc.ethGetLogs(req.Params)
	default:
		return nil, rpcErr(-32601, "method not supported: "+req.Method)
	}
}

type callObj struct {
	To    string `json:"to"`
	From  string `json:"from"`
	Data  string `json:"data"`
	Value string `json:"value"`
}

func (fc *FakeChain) ethCall(params []json.RawMessage) (any, *rpcError) {
	var c callObj
	if len(params) == 0 || json.Unmarshal(params[0], &c) != nil {
		return nil, rpcErr(-32602, "invalid eth_call params")
	}
	data, _ := hex.DecodeString(trimHex(c.Data))
	if len(data) < 4 {
		return "0x", nil
	}
	to := normAddr(c.To)
	switch {
	case to == normAddr(fc.factory):
		return fc.factoryView(data)
	default:
		if e := fc.escrows[to]; e != nil {
			return fc.escrowView(e, data)
		}
		if l := fc.tokens[to]; l != nil {
			return fc.tokenView(l, data)
		}
		// No code at the address — the empty-return posture the rail
		// maps to ErrNotFound.
		return "0x", nil
	}
}

// --- views -------------------------------------------------------------

func sel(name string, abi *web3.ABI, arity int) []byte {
	m, err := abi.Method(name, arity)
	if err != nil {
		panic(fmt.Sprintf("fakechain selector %s/%d: %v", name, arity, err))
	}
	return m.Selector()
}

func encRet(types []string, vals []any) (string, *rpcError) {
	ts := make([]*web3.ABIType, len(types))
	for i, s := range types {
		t, err := web3.ParseABIType(s)
		if err != nil {
			return "", rpcErr(-32000, "fakechain type "+s)
		}
		ts[i] = t
	}
	enc, err := web3.EncodeABIArguments(ts, vals)
	if err != nil {
		return "", rpcErr(-32000, "fakechain encode: "+err.Error())
	}
	return "0x" + hex.EncodeToString(enc), nil
}

func (fc *FakeChain) factoryView(data []byte) (any, *rpcError) {
	switch hex.EncodeToString(data[:4]) {
	case hex.EncodeToString(sel("predictDeterministicAddress", factoryABI, 2)):
		args, rerr := fc.decodeArgs([]string{"bytes32", "bytes32"}, data)
		if rerr != nil {
			return nil, rerr
		}
		typ, _ := hexString32(args[0])
		salt, _ := hexString32(args[1])
		return encRet([]string{"address"}, []any{fc.escrowAddrFor(typ, salt)})
	case hex.EncodeToString(sel("resolutionRateOf", factoryABI, 1)):
		args, rerr := fc.decodeArgs([]string{"address"}, data)
		if rerr != nil {
			return nil, rerr
		}
		resolver, _ := args[0].(string)
		rate := fc.rates[normAddr(resolver)]
		return encRet([]string{"uint256"}, []any{strconv.FormatInt(rate, 10)})
	}
	return nil, rpcErr(-32601, "fakechain: unknown factory selector")
}

func (fc *FakeChain) escrowView(e *fakeEscrow, data []byte) (any, *rpcError) {
	s := hex.EncodeToString(data[:4])
	one := func(name string, retTy string, val any) (any, *rpcError) {
		if s == hex.EncodeToString(sel(name, escrowABI, 0)) {
			return encRet([]string{retTy}, []any{val})
		}
		return nil, nil
	}
	if r, rerr := one("token", "address", e.token); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("client", "address", e.client); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("provider", "address", e.provider); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("resolver", "address", e.resolver); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("resolverType", "uint8", strconv.Itoa(e.resolverType)); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("terminationTime", "uint256", strconv.FormatInt(e.terminationTime, 10)); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("resolutionRate", "uint256", strconv.FormatInt(e.resolutionRate, 10)); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("details", "bytes32", "0x"+hex.EncodeToString(e.details[:])); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("total", "uint256", e.total.String()); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("released", "uint256", e.released.String()); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("locked", "bool", e.locked); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("milestone", "uint256", strconv.Itoa(e.milestone)); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("disputeId", "uint256", "0"); r != nil || rerr != nil {
		return r, rerr
	}
	if r, rerr := one("wrappedNativeToken", "address", e.wrapped); r != nil || rerr != nil {
		return r, rerr
	}
	if s == hex.EncodeToString(sel("getAmounts", escrowABI, 0)) {
		vals := make([]any, len(e.amounts))
		for i, a := range e.amounts {
			vals[i] = a.String()
		}
		return encRet([]string{"uint256[]"}, []any{vals})
	}
	return nil, rpcErr(-32601, "fakechain: unknown escrow selector")
}

func (fc *FakeChain) tokenView(l map[string]*big.Int, data []byte) (any, *rpcError) {
	s := hex.EncodeToString(data[:4])
	switch s {
	case hex.EncodeToString(sel("balanceOf", erc20ABI, 1)):
		args, rerr := fc.decodeArgs([]string{"address"}, data)
		if rerr != nil {
			return nil, rerr
		}
		holder, _ := args[0].(string)
		bal := l[normAddr(holder)]
		if bal == nil {
			bal = new(big.Int)
		}
		return encRet([]string{"uint256"}, []any{bal.String()})
	case hex.EncodeToString(sel("decimals", erc20ABI, 0)):
		return encRet([]string{"uint8"}, []any{"18"})
	case hex.EncodeToString(sel("symbol", erc20ABI, 0)):
		return encRet([]string{"string"}, []any{"TKN"})
	case hex.EncodeToString(sel("allowance", erc20ABI, 2)):
		return encRet([]string{"uint256"}, []any{"0"})
	}
	return nil, rpcErr(-32601, "fakechain: unknown token selector")
}

func (fc *FakeChain) decodeArgs(types []string, data []byte) ([]any, *rpcError) {
	ts := make([]*web3.ABIType, len(types))
	for i, s := range types {
		t, err := web3.ParseABIType(s)
		if err != nil {
			return nil, rpcErr(-32000, "fakechain type "+s)
		}
		ts[i] = t
	}
	vals, err := web3.DecodeABIArguments(ts, data[4:])
	if err != nil {
		return nil, rpcErr(-32602, "fakechain decode: "+err.Error())
	}
	return vals, nil
}

// --- transactions ------------------------------------------------------

func (fc *FakeChain) ethSendTransaction(params []json.RawMessage) (any, *rpcError) {
	var c callObj
	if len(params) == 0 || json.Unmarshal(params[0], &c) != nil {
		return nil, rpcErr(-32602, "invalid eth_sendTransaction params")
	}
	data, _ := hex.DecodeString(trimHex(c.Data))
	return fc.applyTx(normAddr(c.From), normAddr(c.To), data, c.To)
}

// ethSendRawTransaction decodes a signed tx (sender recovered from the
// signature, chain id pinned) and applies it through the same path.
func (fc *FakeChain) ethSendRawTransaction(params []json.RawMessage) (any, *rpcError) {
	var hexRaw string
	if len(params) == 0 || json.Unmarshal(params[0], &hexRaw) != nil {
		return nil, rpcErr(-32602, "invalid eth_sendRawTransaction params")
	}
	dt, err := web3.ParseSignedTxHex(hexRaw)
	if err != nil {
		return nil, rpcErr(-32602, "fakechain: raw tx decode: "+err.Error())
	}
	if dt.ChainID != fc.chainID {
		return nil, revert(fmt.Sprintf(
			"chain id mismatch: tx signed for %d, chain is %d", dt.ChainID, fc.chainID))
	}
	if want := fc.nonces[normAddr(dt.From)]; dt.Nonce != want {
		return nil, revert(fmt.Sprintf("nonce %d, expected %d", dt.Nonce, want))
	}
	return fc.applyTx(normAddr(dt.From), normAddr(dt.To), dt.Data, dt.To)
}

// ethGetTransactionCount returns the sender's mined-tx count.
func (fc *FakeChain) ethGetTransactionCount(params []json.RawMessage) (any, *rpcError) {
	var addr string
	if len(params) == 0 || json.Unmarshal(params[0], &addr) != nil {
		return nil, rpcErr(-32602, "invalid eth_getTransactionCount params")
	}
	return "0x" + strconv.FormatUint(fc.nonces[normAddr(addr)], 16), nil
}

// applyTx dispatches a mined-immediately transaction and bumps the
// sender nonce — the shared core of eth_sendTransaction and
// eth_sendRawTransaction.
func (fc *FakeChain) applyTx(
	from, to string, data []byte, toLabel string,
) (any, *rpcError) {
	if len(data) < 4 {
		return nil, revert("empty calldata")
	}
	var logs []*fakeLog
	var rerr *rpcError
	switch {
	case to == normAddr(fc.factory):
		logs, rerr = fc.txFactory(from, data)
	case fc.tokens[to] != nil:
		logs, rerr = fc.txToken(from, to, data)
	case fc.escrows[to] != nil:
		logs, rerr = fc.txEscrow(from, to, data)
	default:
		rerr = revert("no code at " + toLabel)
	}
	if rerr != nil {
		return nil, rerr
	}
	fc.block++
	fc.txSeq++
	fc.nonces[from]++
	hash := "0x" + hex.EncodeToString(web3.Keccak256(
		[]byte(fmt.Sprintf("faketx:%d:%s:%s", fc.txSeq, from, hex.EncodeToString(data)))))
	for i, l := range logs {
		l.block = fc.block
		l.txHash = hash
		l.index = i
		fc.logs = append(fc.logs, l)
	}
	fc.receipts[hash] = &fakeReceipt{status: "0x1", block: fc.block, logs: logs}
	return hash, nil
}

func (fc *FakeChain) txFactory(from string, data []byte) ([]*fakeLog, *rpcError) {
	if hex.EncodeToString(data[:4]) != hex.EncodeToString(sel("createDeterministic", factoryABI, 5)) {
		return nil, rpcErr(-32601, "fakechain: unsupported factory tx selector")
	}
	args, rerr := fc.decodeArgs([]string{"address", "uint256[]", "bytes", "bytes32", "bytes32"}, data)
	if rerr != nil {
		return nil, rerr
	}
	provider, _ := args[0].(string)
	amounts, _ := args[1].([]any)
	initHex, _ := args[2].(string)
	typ, _ := hexString32(args[3])
	salt, _ := hexString32(args[4])
	init, rerr := fc.decodeInitData(initHex)
	if rerr != nil {
		return nil, rerr
	}
	// Guards mirroring _handleData.
	if init.terminationTime <= fc.now || init.terminationTime > fc.now+63113904 {
		return nil, revert("DurationEnded")
	}
	addr := fc.escrowAddrFor(typ, salt)
	if fc.escrows[normAddr(addr)] != nil {
		return nil, revert("create2 collision")
	}
	total := new(big.Int)
	amts := make([]*big.Int, len(amounts))
	for i, a := range amounts {
		s, _ := a.(string)
		amts[i], _ = new(big.Int).SetString(s, 10)
		total.Add(total, amts[i])
	}
	rate := fc.rates[normAddr(init.resolver)]
	if rate == 0 {
		rate = DefaultResolutionRate
	}
	fc.escrows[normAddr(addr)] = &fakeEscrow{
		client: init.client, provider: provider, resolver: init.resolver,
		resolverType: init.resolverType, token: init.token,
		terminationTime: init.terminationTime, resolutionRate: rate,
		details: init.details, wrapped: init.wrapped,
		amounts: amts, total: total, released: new(big.Int),
	}
	log := &fakeLog{
		address: normAddr(fc.factory),
		topics: []string{
			topicOf("LogNewInvoice(uint256,address,uint256[],bytes32,uint256)"),
			topicUint(uint64(len(fc.escrows))),
			topicAddr(addr),
			topicBytes32(typ),
		},
	}
	if enc, err := web3.EncodeABIArguments(
		[]*web3.ABIType{mustType("uint256[]"), mustType("uint256")},
		[]any{amounts, "0"},
	); err == nil {
		log.data = "0x" + hex.EncodeToString(enc)
	}
	return []*fakeLog{log}, nil
}

type fakeInit struct {
	client, resolver, token, wrapped string
	resolverType                     int
	terminationTime                  int64
	details                          [32]byte
}

func (fc *FakeChain) decodeInitData(initHex string) (*fakeInit, *rpcError) {
	raw, err := hex.DecodeString(trimHex(initHex))
	if err != nil {
		return nil, rpcErr(-32602, "fakechain: bad init data")
	}
	types := []*web3.ABIType{
		mustType("address"), mustType("uint8"), mustType("address"), mustType("address"),
		mustType("uint256"), mustType("bytes32"), mustType("address"), mustType("bool"),
		mustType("address"),
	}
	vals, err := web3.DecodeABIArguments(types, raw)
	if err != nil {
		return nil, rpcErr(-32602, "fakechain: init decode: "+err.Error())
	}
	var details [32]byte
	if b, derr := hex.DecodeString(trimHex(mustStr(vals[5]))); derr == nil {
		copy(details[:], b)
	}
	return &fakeInit{
		client:          mustStr(vals[0]),
		resolverType:    int(mustInt64(vals[1])),
		resolver:        mustStr(vals[2]),
		token:           mustStr(vals[3]),
		terminationTime: mustInt64(vals[4]),
		details:         details,
		wrapped:         mustStr(vals[6]),
	}, nil
}

func (fc *FakeChain) txToken(from, token string, data []byte) ([]*fakeLog, *rpcError) {
	if hex.EncodeToString(data[:4]) != hex.EncodeToString(sel("transfer", erc20ABI, 2)) {
		return nil, rpcErr(-32601, "fakechain: unsupported token tx selector")
	}
	args, rerr := fc.decodeArgs([]string{"address", "uint256"}, data)
	if rerr != nil {
		return nil, rerr
	}
	dest, _ := args[0].(string)
	amtS, _ := args[1].(string)
	amt, _ := new(big.Int).SetString(amtS, 10)
	l := fc.tokens[token]
	if l[from] == nil || l[from].Cmp(amt) < 0 {
		return nil, revert("ERC20: transfer amount exceeds balance")
	}
	l[from].Sub(l[from], amt)
	d := normAddr(dest)
	if l[d] == nil {
		l[d] = new(big.Int)
	}
	l[d].Add(l[d], amt)
	return []*fakeLog{{
		address: token,
		topics:  []string{topicOf("Transfer(address,address,uint256)"), topicAddr(from), topicAddr(d)},
		data:    "0x" + hex.EncodeToString(wordBig(amt)),
	}}, nil
}

func (fc *FakeChain) txEscrow(from, addr string, data []byte) ([]*fakeLog, *rpcError) {
	e := fc.escrows[addr]
	s := hex.EncodeToString(data[:4])
	bal := fc.tokenBal(e.token, addr)
	switch s {
	case hex.EncodeToString(sel("release", escrowABI, 0)):
		if e.locked {
			return nil, revert("Locked")
		}
		if from != normAddr(e.client) {
			return nil, revert("NotClient")
		}
		if bal.Sign() == 0 {
			return nil, revert("BalanceIsZero")
		}
		var amt *big.Int
		if e.milestone < len(e.amounts) {
			amt = new(big.Int).Set(e.amounts[e.milestone])
			if e.milestone == len(e.amounts)-1 && amt.Cmp(bal) < 0 {
				amt = new(big.Int).Set(bal)
			}
		} else {
			amt = new(big.Int).Set(bal)
		}
		if bal.Cmp(amt) < 0 {
			return nil, revert("InsufficientBalance")
		}
		e.milestone++
		fc.tokenMove(e.token, addr, e.provider, amt)
		e.released.Add(e.released, amt)
		return []*fakeLog{{
			address: addr,
			topics:  []string{topicOf("Release(uint256,uint256)")},
			data: "0x" + hex.EncodeToString(append(
				wordBig(big.NewInt(int64(e.milestone-1))), wordBig(amt)...)),
		}}, nil
	case hex.EncodeToString(sel("lock", escrowABI, 1)):
		if e.locked {
			return nil, revert("Locked")
		}
		if bal.Sign() == 0 {
			return nil, revert("BalanceIsZero")
		}
		if fc.now >= e.terminationTime {
			return nil, revert("Terminated")
		}
		if from != normAddr(e.client) && from != normAddr(e.provider) {
			return nil, revert("NotParty")
		}
		e.locked = true
		return []*fakeLog{{
			address: addr,
			topics:  []string{topicOf("Lock(address,bytes32)"), topicAddr(from)},
			data:    "0x" + hex.EncodeToString(data[4:36]),
		}}, nil
	case hex.EncodeToString(sel("resolve", escrowABI, 3)):
		if !e.locked {
			return nil, revert("Locked")
		}
		if bal.Sign() == 0 {
			return nil, revert("BalanceIsZero")
		}
		if from != normAddr(e.resolver) {
			return nil, revert("NotResolver")
		}
		args, rerr := fc.decodeArgs([]string{"uint256", "uint256", "bytes32"}, data)
		if rerr != nil {
			return nil, rerr
		}
		cA := mustBig(args[0])
		pA := mustBig(args[1])
		fee := new(big.Int).Div(bal, big.NewInt(e.resolutionRate))
		if new(big.Int).Add(cA, pA).Cmp(new(big.Int).Sub(bal, fee)) != 0 {
			return nil, revert("ResolutionMismatch")
		}
		if pA.Sign() > 0 {
			fc.tokenMove(e.token, addr, e.provider, pA)
		}
		if cA.Sign() > 0 {
			fc.tokenMove(e.token, addr, e.client, cA)
		}
		if fee.Sign() > 0 {
			fc.tokenMove(e.token, addr, e.resolver, fee)
		}
		e.milestone = len(e.amounts)
		e.locked = false
		details, _ := hexString32(args[2])
		return []*fakeLog{{
			address: addr,
			topics:  []string{topicOf("Resolve(address,uint256,uint256,uint256,bytes32)"), topicAddr(from)},
			data: "0x" + hex.EncodeToString(append(append(append(
				wordBig(cA), wordBig(pA)...), wordBig(fee)...), details[:]...)),
		}}, nil
	case hex.EncodeToString(sel("withdraw", escrowABI, 0)):
		if e.locked {
			return nil, revert("Locked")
		}
		if fc.now <= e.terminationTime {
			return nil, revert("Terminated")
		}
		if bal.Sign() == 0 {
			return nil, revert("BalanceIsZero")
		}
		fc.tokenMove(e.token, addr, e.client, bal)
		e.milestone = len(e.amounts)
		return []*fakeLog{{
			address: addr,
			topics:  []string{topicOf("Withdraw(uint256)")},
			data:    "0x" + hex.EncodeToString(wordBig(bal)),
		}}, nil
	}
	return nil, rpcErr(-32601, "fakechain: unsupported escrow tx selector")
}

func (fc *FakeChain) tokenBal(token, holder string) *big.Int {
	if b := fc.tokens[normAddr(token)][normAddr(holder)]; b != nil {
		return new(big.Int).Set(b)
	}
	return new(big.Int)
}

func (fc *FakeChain) tokenMove(token, from, to string, amt *big.Int) {
	l := fc.tokens[normAddr(token)]
	f, t := normAddr(from), normAddr(to)
	if l[f] == nil {
		l[f] = new(big.Int)
	}
	l[f].Sub(l[f], amt)
	if l[t] == nil {
		l[t] = new(big.Int)
	}
	l[t].Add(l[t], amt)
}

// --- receipts and logs ---------------------------------------------------

func (fc *FakeChain) ethGetReceipt(params []json.RawMessage) (any, *rpcError) {
	var hash string
	if len(params) == 0 || json.Unmarshal(params[0], &hash) != nil {
		return nil, rpcErr(-32602, "invalid receipt params")
	}
	rc := fc.receipts[hash]
	if rc == nil {
		return nil, nil // pending: null receipt
	}
	logs := make([]map[string]any, len(rc.logs))
	for i, l := range rc.logs {
		logs[i] = map[string]any{
			"address": l.address, "topics": l.topics, "data": l.data,
			"blockNumber": "0x" + strconv.FormatUint(l.block, 16),
			"logIndex":    "0x" + strconv.Itoa(l.index),
		}
	}
	return map[string]any{
		"transactionHash": hash,
		"status":          rc.status,
		"blockNumber":     "0x" + strconv.FormatUint(rc.block, 16),
		"gasUsed":         "0x5208",
		"logs":            logs,
	}, nil
}

func (fc *FakeChain) ethGetLogs(params []json.RawMessage) (any, *rpcError) {
	var filter struct {
		Address   any    `json:"address"`
		Topics    []any  `json:"topics"`
		FromBlock string `json:"fromBlock"`
		ToBlock   string `json:"toBlock"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params[0], &filter)
	}
	fromB, _ := web3.QuantityUint64(filter.FromBlock)
	toB := fc.block
	if filter.ToBlock != "" && filter.ToBlock != "latest" {
		toB, _ = web3.QuantityUint64(filter.ToBlock)
	}
	var addrs map[string]bool
	switch a := filter.Address.(type) {
	case string:
		addrs = map[string]bool{normAddr(a): true}
	case []any:
		addrs = map[string]bool{}
		for _, s := range a {
			if str, ok := s.(string); ok {
				addrs[normAddr(str)] = true
			}
		}
	}
	var out []map[string]any
	for _, l := range fc.logs {
		if l.block < fromB || l.block > toB {
			continue
		}
		if addrs != nil && !addrs[normAddr(l.address)] {
			continue
		}
		if !topicsMatch(filter.Topics, l.topics) {
			continue
		}
		out = append(out, map[string]any{
			"address": l.address, "topics": l.topics, "data": l.data,
			"blockNumber":     "0x" + strconv.FormatUint(l.block, 16),
			"transactionHash": l.txHash,
			"logIndex":        "0x" + strconv.Itoa(l.index),
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, nil
}

func topicsMatch(filter []any, actual []string) bool {
	for i, f := range filter {
		if f == nil {
			continue
		}
		if i >= len(actual) {
			return false
		}
		s, ok := f.(string)
		if !ok {
			continue // OR arrays: accept-any in this fixture
		}
		if !equalTopic(s, actual[i]) {
			return false
		}
	}
	return true
}

func equalTopic(a, b string) bool { return strings.EqualFold(a, b) }

// --- small helpers -------------------------------------------------------

func normAddr(s string) string { return strings.ToLower(s) }

func trimHex(s string) string {
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		return s[2:]
	}
	return s
}

func hexString32(v any) ([32]byte, error) {
	var out [32]byte
	s, _ := v.(string)
	b, err := hex.DecodeString(trimHex(s))
	if err == nil {
		copy(out[:], b)
	}
	return out, err
}

func topicOf(sig string) string {
	return "0x" + hex.EncodeToString(web3.Keccak256([]byte(sig)))
}

func topicAddr(addr string) string {
	return "0x000000000000000000000000" + normAddr(addr)[2:]
}

func topicUint(v uint64) string {
	//nolint:gosec // G115: topics carry counters/ids, far below int64 range.
	return "0x" + hex.EncodeToString(wordBig(big.NewInt(int64(v))))
}

func topicBytes32(b [32]byte) string { return "0x" + hex.EncodeToString(b[:]) }

func wordBig(n *big.Int) []byte {
	w := make([]byte, 32)
	if n != nil {
		b := n.Bytes()
		copy(w[32-len(b):], b)
	}
	return w
}

func mustBig(v any) *big.Int {
	s, _ := v.(string)
	n, _ := new(big.Int).SetString(s, 10)
	if n == nil {
		return new(big.Int)
	}
	return n
}

// mustStr/mustInt64 extract decoded-ABI values whose Go shapes the codec
// guarantees (address→checksummed string, uint→decimal string, bool→bool).
// A wrong shape means the decoder itself is broken — panic, like sel().
func mustStr(v any) string {
	s, ok := v.(string)
	if !ok {
		panic(fmt.Sprintf("fakechain: decode produced %T, want string", v))
	}
	return s
}

func mustInt64(v any) int64 {
	n, err := strconv.ParseInt(mustStr(v), 10, 64)
	if err != nil {
		panic(fmt.Sprintf("fakechain: decode produced bad int64 %v", v))
	}
	return n
}
