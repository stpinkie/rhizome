// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3tools

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/tools"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// Web3ApprovalAction is the normalized signing request shown to approval
// hooks — the same view a human approver sees in the pending queue.
type Web3ApprovalAction struct {
	Tool     string `json:"tool"`
	Kind     string `json:"kind"`
	Summary  string `json:"summary"`
	From     string `json:"from"`
	To       string `json:"to,omitempty"`
	ValueWei string `json:"value_wei,omitempty"`
	ChainID  uint64 `json:"chain_id,omitempty"`
	Selector string `json:"selector,omitempty"`
}

// ApprovalHook vets a normalized signing request before it enters the
// pending queue. It is a veto pass, not authorization — human approval is
// still required. agent.HookManager adapts to this at registration time.
type ApprovalHook interface {
	ApproveWeb3Action(ctx context.Context, action *Web3ApprovalAction) (approved bool, reason string)
}

// SigningDeps bundles the shared state the signing tools operate on. Emit
// may be nil; Hook may be nil (no veto pass).
type SigningDeps struct {
	Provider *web3.Provider
	Stack    *web3.SigningStack
	Hook     ApprovalHook
	// ApproveTTL overrides the pending-entry TTL (0 → store default 15m).
	ApproveTTL time.Duration
	// Emit publishes a runtime event (web3.*) — nil-safe.
	Emit func(kind string, attrs map[string]any)
	// Cfg + ConfigPath let the web3_watch tool persist watch definitions
	// (tools.web3.watches) back to config.json. Nil Cfg disables mutations.
	Cfg        *config.Config
	ConfigPath string
}

func (d *SigningDeps) emit(kind string, attrs map[string]any) {
	if d != nil && d.Emit != nil {
		d.Emit(kind, attrs)
	}
}

// SignTool is a web3_* tool that touches wallet state or queues approvals.
type SignTool struct {
	deps   *SigningDeps
	name   string
	desc   string
	params map[string]any
	// gated marks tools that additionally require tools.web3.signing.enabled.
	gated bool
	run   func(ctx context.Context, d *SigningDeps, args map[string]any) (any, error)
}

func (t *SignTool) Name() string               { return t.name }
func (t *SignTool) Description() string        { return t.desc }
func (t *SignTool) Parameters() map[string]any { return t.params }

func (t *SignTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	if t.gated && (t.deps == nil || t.deps.Stack == nil || t.deps.Stack.Policy == nil ||
		!t.deps.Stack.Policy.Enabled) {
		return tools.ErrorResult(t.name +
			" is disabled — set tools.web3.enabled and tools.web3.signing.enabled")
	}
	out, err := t.run(ctx, t.deps, args)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s failed: %v", t.name, err))
	}
	data, err := json.Marshal(out)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: marshal result: %v", t.name, err))
	}
	return tools.SilentResult(string(data))
}

// SigningTools constructs the wallet/pending/signing tool set.
func SigningTools(deps *SigningDeps) []tools.Tool {
	strProp := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			m["required"] = required
		}
		return m
	}

	send := &SignTool{
		deps: deps, name: "web3_send", gated: true,
		desc: "Queue an ETH/contract transaction for HUMAN APPROVAL — nothing is signed or " +
			"broadcast until a person approves the pending request (rhizome web3 approve, " +
			"daemon API, or dashboard). Subject to tools.web3.signing policy allowlists and " +
			"spend caps. Returns a pending_id for web3_send_status.",
		params: obj(map[string]any{
			"to":     strProp("Recipient address (0x…)"),
			"value":  strProp("Wei value: decimal or 0x-hex (default 0)"),
			"ether":  strProp("Ether-denominated value (decimal, e.g. \"0.05\") — alternative to value"),
			"data":   strProp("Optional calldata (0x…) for contract calls"),
			"from":   strProp("Signer address (default: wallet default)"),
			"reason": strProp("Human-facing note explaining why this send is needed"),
		}, "to"),
		run: runSend,
	}
	sign := &SignTool{
		deps: deps, name: "web3_sign", gated: true,
		desc: "Queue an EIP-191 personal-sign message for HUMAN APPROVAL. The signature " +
			"proves control of the wallet address — approvals and logins can carry the " +
			"same weight as a transaction. Returns a pending_id.",
		params: obj(map[string]any{
			"message": strProp("Message to sign — UTF-8 text, or 0x-hex for raw bytes"),
			"from":    strProp("Signer address (default: wallet default)"),
			"reason":  strProp("Human-facing note explaining why this signature is needed"),
		}, "message"),
		run: runSign,
	}
	approve := &SignTool{
		deps: deps, name: "web3_approve", gated: true,
		desc: "Queue an ERC-20 approve(spender, amount) transaction for HUMAN APPROVAL. " +
			"amount is in the token's base units (decimal string), or \"unlimited\" for " +
			"the uint256 maximum. Prefer exact amounts — unlimited approvals let the " +
			"spender drain the token balance later.",
		params: obj(map[string]any{
			"token":   strProp("ERC-20 contract address (0x…)"),
			"spender": strProp("Address being approved to spend (0x…)"),
			"amount":  strProp("Base-unit amount (decimal) or \"unlimited\""),
			"from":    strProp("Signer address (default: wallet default)"),
			"reason":  strProp("Human-facing note"),
		}, "token", "spender", "amount"),
		run: runApprove,
	}
	wallet := &SignTool{
		deps: deps, name: "web3_wallet",
		desc: "List wallet addresses with labels, the default signer, and balances " +
			"(eth_getBalance per address). Read-only — private keys are never exposed.",
		params: obj(map[string]any{
			"include_balances": map[string]any{
				"type":        "boolean",
				"description": "Fetch ETH balances (default true)",
			},
		}),
		run: runWallet,
	}
	pending := &SignTool{
		deps: deps, name: "web3_pending",
		desc: "List pending signing approvals and their status (pending/approved/sent/" +
			"rejected/expired/failed). Use web3_send_status for receipt detail on one entry.",
		params: obj(map[string]any{
			"status": strProp("Filter by status: pending|approved|sent|rejected|expired|failed (default: all)"),
		}),
		run: runPending,
	}
	sendStatus := &SignTool{
		deps: deps, name: "web3_send_status",
		desc: "Check one queued approval by pending_id (or tx_hash): status, tx hash, " +
			"and on-chain receipt once the transaction is mined.",
		params: obj(map[string]any{
			"id":      strProp("Pending request id"),
			"tx_hash": strProp("Transaction hash (0x…)"),
		}),
		run: runSendStatus,
	}

	return []tools.Tool{send, sign, approve, wallet, pending, sendStatus}
}

// RegisterSigning registers the wallet/approval web3_* tools into reg.
// Read tools (wallet/pending/send_status) require tools.web3.enabled;
// mutating tools (send/sign/approve) additionally require
// tools.web3.signing.enabled — enforced per-call in Execute so a
// registration-time snapshot can't bypass the gate.
func RegisterSigning(reg *tools.ToolRegistry, deps *SigningDeps) {
	if reg == nil || deps == nil || deps.Stack == nil {
		return
	}
	for _, t := range SigningTools(deps) {
		reg.Register(t)
	}
}

// --- shared helpers ---

// resolveFrom picks the signer: explicit arg or the wallet default.
func resolveFrom(d *SigningDeps, args map[string]any) (string, error) {
	from := strArg(args, "from")
	if from != "" {
		if !web3.IsAddress(from) {
			return "", fmt.Errorf("from %q is not a valid address", from)
		}
		if !d.Stack.Wallets.Has(from) {
			return "", fmt.Errorf("from %s is not in this wallet", from)
		}
		return from, nil
	}
	def, err := d.Stack.Wallets.Default()
	if err != nil {
		return "", fmt.Errorf("no default wallet address — create one with `rhizome wallet create`")
	}
	return def, nil
}

// evaluatePolicy runs the signing policy against req, then the optional
// hook veto pass with the normalized action view.
func evaluatePolicy(ctx context.Context, d *SigningDeps, tool string,
	req *web3.SignRequest, summary string,
) error {
	decision := d.Stack.Policy.Evaluate(req, d.Stack.Ledger, time.Now().UTC())
	if !decision.Allowed {
		return fmt.Errorf("policy denied: %s", decision.Reason)
	}
	if d.Hook != nil {
		action := &Web3ApprovalAction{
			Tool: tool, Kind: req.Kind, Summary: summary,
			From: req.From, To: req.To, ChainID: req.ChainID,
			Selector: req.SelectorHex(),
		}
		if req.ValueWei != nil {
			action.ValueWei = req.ValueWei.String()
		}
		ok, reason := d.Hook.ApproveWeb3Action(ctx, action)
		if !ok {
			return fmt.Errorf("denied by approval hook: %s", reason)
		}
	}
	return nil
}

// submitPending enqueues a pending entry and emits web3.pending.
func submitPending(d *SigningDeps, e *web3.PendingEntry) (*web3.PendingEntry, error) {
	if d.ApproveTTL > 0 {
		e.ExpiresAt = time.Now().UTC().Add(d.ApproveTTL)
	}
	out, err := d.Stack.Pending.Submit(e)
	if err != nil {
		return nil, err
	}
	d.emit("web3.pending", map[string]any{
		"id":      out.ID,
		"kind":    string(out.Kind),
		"from":    out.From,
		"to":      out.To,
		"summary": out.Summary,
	})
	return out, nil
}

// parseWeiArg accepts decimal or 0x-hex wei.
func parseWeiArg(v string) (*big.Int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return new(big.Int), nil
	}
	if strings.HasPrefix(v, "0x") || strings.HasPrefix(v, "0X") {
		n, ok := new(big.Int).SetString(v[2:], 16)
		if !ok {
			return nil, fmt.Errorf("invalid hex wei %q", v)
		}
		return n, nil
	}
	n, ok := new(big.Int).SetString(v, 10)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("invalid wei amount %q", v)
	}
	return n, nil
}

var weiPerEther = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

// etherToWei converts a decimal ether string to wei via exact rational math.
func etherToWei(s string) (*big.Int, error) {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(s))
	if !ok || r.Sign() < 0 {
		return nil, fmt.Errorf("invalid ether amount %q", s)
	}
	r.Mul(r, new(big.Rat).SetInt(weiPerEther))
	if !r.IsInt() {
		return nil, fmt.Errorf("ether amount %q is finer than 1 wei", s)
	}
	return r.Num(), nil
}

// resolveValue reads value|ether args into wei.
func resolveValue(args map[string]any) (*big.Int, error) {
	if eth := strArg(args, "ether"); eth != "" {
		if strArg(args, "value") != "" {
			return nil, fmt.Errorf("pass either value or ether, not both")
		}
		return etherToWei(eth)
	}
	return parseWeiArg(strArg(args, "value"))
}

// liveChainID binds the request to the endpoint's chain at submit time —
// ExecuteApproved re-verifies before signing, so an endpoint swap between
// queue and approval fails closed.
func liveChainID(ctx context.Context, d *SigningDeps) (uint64, error) {
	if d.Provider == nil {
		return 0, fmt.Errorf("no web3 endpoint configured")
	}
	return web3.ChainID(ctx, d.Provider)
}

// --- tool implementations ---

func runSend(ctx context.Context, d *SigningDeps, args map[string]any) (any, error) {
	to := strArg(args, "to")
	if !web3.IsAddress(to) {
		return nil, fmt.Errorf("to %q is not a valid address", to)
	}
	from, err := resolveFrom(d, args)
	if err != nil {
		return nil, err
	}
	value, err := resolveValue(args)
	if err != nil {
		return nil, err
	}
	data, err := web3.ParseHexBytes(strArg(args, "data"))
	if err != nil {
		return nil, fmt.Errorf("data: %w", err)
	}
	chainID, err := liveChainID(ctx, d)
	if err != nil {
		return nil, err
	}
	summary := fmt.Sprintf("Send %s wei from %s to %s on chain %d", value, from, to, chainID)
	if len(data) >= 4 {
		summary += fmt.Sprintf(" with calldata 0x%x… (%d bytes)", data[:4], len(data))
	} else if len(data) > 0 {
		summary += fmt.Sprintf(" with calldata 0x%x (%d bytes)", data, len(data))
	}
	if reason := strArg(args, "reason"); reason != "" {
		summary += " — " + reason
	}
	req := &web3.SignRequest{
		Kind: string(web3.KindSend), ChainID: chainID, From: from, To: to,
		ValueWei: value, Data: data,
	}
	if err := evaluatePolicy(ctx, d, "web3_send", req, summary); err != nil {
		return nil, err
	}
	e, err := submitPending(d, &web3.PendingEntry{
		Kind: web3.KindSend, ChainID: chainID, From: from, To: to,
		ValueWei: value.String(), Data: hexOrEmpty(data),
		Selector: req.SelectorHex(), Summary: summary,
	})
	if err != nil {
		return nil, err
	}
	return pendingResult(e), nil
}

func runSign(ctx context.Context, d *SigningDeps, args map[string]any) (any, error) {
	msg := strArg(args, "message")
	if msg == "" {
		return nil, fmt.Errorf("message is required")
	}
	msgBytes := []byte(msg)
	if strings.HasPrefix(msg, "0x") {
		b, err := web3.ParseHexBytes(msg)
		if err != nil {
			return nil, fmt.Errorf("message hex: %w", err)
		}
		msgBytes = b
	}
	if len(msgBytes) > 64*1024 {
		return nil, fmt.Errorf("message too large (%d bytes, max 64 KiB)", len(msgBytes))
	}
	from, err := resolveFrom(d, args)
	if err != nil {
		return nil, err
	}
	chainID, err := liveChainID(ctx, d)
	if err != nil {
		return nil, err
	}
	summary := fmt.Sprintf("EIP-191 sign %d-byte message with %s", len(msgBytes), from)
	if reason := strArg(args, "reason"); reason != "" {
		summary += " — " + reason
	}
	req := &web3.SignRequest{
		Kind: string(web3.KindSign), ChainID: chainID, From: from,
	}
	if err := evaluatePolicy(ctx, d, "web3_sign", req, summary); err != nil {
		return nil, err
	}
	e, err := submitPending(d, &web3.PendingEntry{
		Kind: web3.KindSign, ChainID: chainID, From: from,
		Message: hexOrEmpty(msgBytes), Summary: summary,
	})
	if err != nil {
		return nil, err
	}
	return pendingResult(e), nil
}

func runApprove(ctx context.Context, d *SigningDeps, args map[string]any) (any, error) {
	token := strArg(args, "token")
	if !web3.IsAddress(token) {
		return nil, fmt.Errorf("token %q is not a valid address", token)
	}
	spender := strArg(args, "spender")
	if !web3.IsAddress(spender) {
		return nil, fmt.Errorf("spender %q is not a valid address", spender)
	}
	amountStr := strArg(args, "amount")
	var amount *big.Int
	if strings.EqualFold(amountStr, "unlimited") {
		amount = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	} else {
		var err error
		amount, err = parseWeiArg(amountStr)
		if err != nil {
			return nil, fmt.Errorf("amount: %w", err)
		}
	}
	from, err := resolveFrom(d, args)
	if err != nil {
		return nil, err
	}
	data, err := packERC20Approve(spender, amount)
	if err != nil {
		return nil, err
	}
	chainID, err := liveChainID(ctx, d)
	if err != nil {
		return nil, err
	}
	summary := fmt.Sprintf("ERC-20 approve %s to spend %s base units of %s (chain %d)",
		spender, amountStr, token, chainID)
	if reason := strArg(args, "reason"); reason != "" {
		summary += " — " + reason
	}
	req := &web3.SignRequest{
		Kind: string(web3.KindApprove), ChainID: chainID, From: from, To: token,
		ValueWei: new(big.Int), Data: data,
	}
	if err := evaluatePolicy(ctx, d, "web3_approve", req, summary); err != nil {
		return nil, err
	}
	e, err := submitPending(d, &web3.PendingEntry{
		Kind: web3.KindApprove, ChainID: chainID, From: from, To: token,
		ValueWei: "0", Data: hexOrEmpty(data),
		Selector: req.SelectorHex(), Summary: summary,
	})
	if err != nil {
		return nil, err
	}
	return pendingResult(e), nil
}

// packERC20Approve ABI-encodes approve(address,uint256) — selector 0x095ea7b3.
func packERC20Approve(spender string, amount *big.Int) ([]byte, error) {
	addrT, err := web3.ParseABIType("address")
	if err != nil {
		return nil, err
	}
	uintT, err := web3.ParseABIType("uint256")
	if err != nil {
		return nil, err
	}
	args, err := web3.EncodeABIArguments([]*web3.ABIType{addrT, uintT}, []any{spender, amount})
	if err != nil {
		return nil, fmt.Errorf("encode approve args: %w", err)
	}
	return append(web3.MethodSelector("approve(address,uint256)"), args...), nil
}

func runWallet(ctx context.Context, d *SigningDeps, args map[string]any) (any, error) {
	entries, err := d.Stack.Wallets.List()
	if err != nil {
		return nil, err
	}
	def, _ := d.Stack.Wallets.Default()
	type addrInfo struct {
		Address    string `json:"address"`
		Label      string `json:"label,omitempty"`
		Default    bool   `json:"default,omitempty"`
		BalanceWei string `json:"balance_wei,omitempty"`
		BalanceErr string `json:"balance_error,omitempty"`
	}
	out := make([]addrInfo, 0, len(entries))
	includeBalances := true
	if v, ok := args["include_balances"].(bool); ok {
		includeBalances = v
	}
	for _, e := range entries {
		info := addrInfo{Address: e.Address, Label: e.Label, Default: e.Address == def}
		if includeBalances && d.Provider != nil {
			bal, berr := quantityArg(ctx, d.Provider, "eth_getBalance",
				[]any{e.Address, "latest"})
			if berr != nil {
				info.BalanceErr = berr.Error()
			} else if wei, perr := web3.ParseQuantity(bal); perr != nil {
				info.BalanceErr = perr.Error()
			} else {
				info.BalanceWei = wei.String()
			}
		}
		out = append(out, info)
	}
	return map[string]any{
		"addresses": out,
		"default":   def,
		"count":     len(out),
	}, nil
}

func runPending(_ context.Context, d *SigningDeps, args map[string]any) (any, error) {
	entries, err := d.Stack.Pending.List()
	if err != nil {
		return nil, err
	}
	filter := strArg(args, "status")
	out := make([]*web3.PendingEntry, 0, len(entries))
	for _, e := range entries {
		if filter != "" && string(e.Status) != filter {
			continue
		}
		out = append(out, e)
	}
	return map[string]any{"pending": out, "count": len(out)}, nil
}

func runSendStatus(ctx context.Context, d *SigningDeps, args map[string]any) (any, error) {
	id := strArg(args, "id")
	txHash := strArg(args, "tx_hash")
	if id == "" && txHash == "" {
		return nil, fmt.Errorf("id or tx_hash is required")
	}
	var entry *web3.PendingEntry
	if id != "" {
		e, err := d.Stack.Pending.Get(id)
		if err != nil {
			return nil, err
		}
		entry = e
	} else {
		entries, err := d.Stack.Pending.List()
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if strings.EqualFold(e.TxHash, txHash) {
				entry = e
				break
			}
		}
		if entry == nil {
			return nil, fmt.Errorf("no pending request with tx_hash %s", txHash)
		}
	}
	res := map[string]any{"entry": entry}
	if entry.TxHash != "" && d.Provider != nil {
		raw, err := callRaw(ctx, d.Provider, "eth_getTransactionReceipt", []any{entry.TxHash})
		if err == nil {
			if rcpt, derr := web3.DecodeReceipt(raw); derr == nil && rcpt != nil {
				res["receipt"] = rcpt
			}
		}
	}
	return res, nil
}

func pendingResult(e *web3.PendingEntry) map[string]any {
	return map[string]any{
		"pending_id": e.ID,
		"status":     string(e.Status),
		"summary":    e.Summary,
		"expires_at": e.ExpiresAt.Format(time.RFC3339),
		"note": "Awaiting human approval — rhizome web3 pending lists it, " +
			"rhizome web3 approve " + e.ID + " executes it. Nothing is signed or " +
			"broadcast until then.",
	}
}

func hexOrEmpty(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return "0x" + hex.EncodeToString(b)
}
