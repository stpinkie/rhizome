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

	"github.com/stpinkie/rhizome/pkg/tools"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// Embedded minimal token ABIs — the registry isn't required for common
// ERC-20/721 calls.
var (
	erc20ABI = mustToolABI(`[
		{"type":"function","name":"name","stateMutability":"view","inputs":[],"outputs":[{"type":"string"}]},
		{"type":"function","name":"symbol","stateMutability":"view","inputs":[],"outputs":[{"type":"string"}]},
		{"type":"function","name":"decimals","stateMutability":"view","inputs":[],"outputs":[{"type":"uint8"}]},
		{"type":"function","name":"totalSupply","stateMutability":"view","inputs":[],"outputs":[{"type":"uint256"}]},
		{"type":"function","name":"balanceOf","stateMutability":"view",
		 "inputs":[{"name":"account","type":"address"}],"outputs":[{"type":"uint256"}]},
		{"type":"function","name":"allowance","stateMutability":"view",
		 "inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],
		 "outputs":[{"type":"uint256"}]},
		{"type":"function","name":"transfer","stateMutability":"nonpayable",
		 "inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],
		 "outputs":[{"type":"bool"}]},
		{"type":"function","name":"approve","stateMutability":"nonpayable",
		 "inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],
		 "outputs":[{"type":"bool"}]}
	]`)
	erc721ABI = mustToolABI(`[
		{"type":"function","name":"name","stateMutability":"view","inputs":[],"outputs":[{"type":"string"}]},
		{"type":"function","name":"symbol","stateMutability":"view","inputs":[],"outputs":[{"type":"string"}]},
		{"type":"function","name":"ownerOf","stateMutability":"view",
		 "inputs":[{"name":"tokenId","type":"uint256"}],"outputs":[{"type":"address"}]},
		{"type":"function","name":"balanceOf","stateMutability":"view",
		 "inputs":[{"name":"owner","type":"address"}],"outputs":[{"type":"uint256"}]},
		{"type":"function","name":"tokenURI","stateMutability":"view",
		 "inputs":[{"name":"tokenId","type":"uint256"}],"outputs":[{"type":"string"}]},
		{"type":"function","name":"transferFrom","stateMutability":"nonpayable",
		 "inputs":[{"name":"from","type":"address"},{"name":"to","type":"address"},{"name":"tokenId","type":"uint256"}],
		 "outputs":[]},
		{"type":"function","name":"safeTransferFrom","stateMutability":"nonpayable",
		 "inputs":[{"name":"from","type":"address"},{"name":"to","type":"address"},{"name":"tokenId","type":"uint256"}],
		 "outputs":[]}
	]`)
)

func mustToolABI(src string) *web3.ABI {
	a, err := web3.ParseABIJSON(json.RawMessage(src))
	if err != nil {
		panic("embedded token ABI: " + err.Error())
	}
	return a
}

// ContractTools returns the contract-call/send and token helper tools.
func ContractTools(deps *SigningDeps) []tools.Tool {
	strProp := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	arrProp := func(desc string) map[string]any {
		return map[string]any{"type": "array", "description": desc, "items": map[string]any{}}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			m["required"] = required
		}
		return m
	}

	call := &SignTool{
		deps: deps, name: "web3_contract_call",
		desc: "Call a contract method by name (read-only eth_call). contract accepts a " +
			"0x address, a registered ABI label, or an ENS name; args are ABI-coerced " +
			"JSON values. Returns decoded outputs.",
		params: obj(map[string]any{
			"contract": strProp("0x address, registry label, or ENS name"),
			"method":   strProp("Method name (overloads resolved by arg count)"),
			"args":     arrProp("Positional args — strings/numbers/bools per the ABI"),
			"block":    strProp("Block tag (default latest)"),
		}, "contract", "method"),
		run: runContractCall,
	}
	send := &SignTool{
		deps: deps, name: "web3_contract_send", gated: true,
		desc: "Queue a contract method call as a transaction for HUMAN APPROVAL. " +
			"Same resolution and encoding as web3_contract_call; policy allowlists " +
			"support \"label:method\" entries. Nothing broadcasts until approved.",
		params: obj(map[string]any{
			"contract": strProp("0x address, registry label, or ENS name"),
			"method":   strProp("Method name"),
			"args":     arrProp("Positional args"),
			"value":    strProp("Optional wei value (decimal or 0x-hex)"),
			"from":     strProp("Signer address (default: wallet default)"),
			"reason":   strProp("Human-facing note"),
		}, "contract", "method"),
		run: runContractSend,
	}
	erc20 := &SignTool{
		deps: deps, name: "web3_erc20",
		desc: "ERC-20 helper: info|balance|allowance are read-only; transfer|approve " +
			"queue a human-approved transaction. amount is in token units (" +
			"decimals-aware, e.g. \"5.25\" USDC) — or pass raw base units via amount_units.",
		params: obj(map[string]any{
			"action":       strProp("info|balance|allowance|transfer|approve"),
			"token":        strProp("Token contract (0x, label, or ENS)"),
			"owner":        strProp("Owner address (balance/allowance; default: wallet default)"),
			"to":           strProp("Recipient (transfer)"),
			"spender":      strProp("Spender (allowance/approve)"),
			"amount":       strProp("Token-unit amount (decimals-aware) or \"unlimited\" (approve)"),
			"amount_units": strProp("Raw base-unit amount (overrides amount)"),
			"from":         strProp("Signer address for writes (default: wallet default)"),
			"reason":       strProp("Human-facing note for writes"),
		}, "action", "token"),
		run: runERC20,
	}
	erc721 := &SignTool{
		deps: deps, name: "web3_erc721",
		desc: "ERC-721 helper: name|symbol|ownerOf|balanceOf|tokenURI are read-only; " +
			"transferFrom|safeTransferFrom queue a human-approved transaction.",
		params: obj(map[string]any{
			"action":   strProp("name|symbol|ownerOf|balanceOf|tokenURI|transferFrom|safeTransferFrom"),
			"token":    strProp("NFT contract (0x, label, or ENS)"),
			"token_id": strProp("Token ID (decimal)"),
			"owner":    strProp("Owner address (balanceOf)"),
			"to":       strProp("Recipient (transfers)"),
			"from":     strProp("Signer/source address (transfers; default: wallet default)"),
			"reason":   strProp("Human-facing note for writes"),
		}, "action", "token"),
		run: runERC721,
	}
	ens := &SignTool{
		deps: deps, name: "web3_ens",
		desc: "Resolve ENS names on-chain: {name: \"foo.eth\"} → address, or " +
			"{address: \"0x…\"} → primary name. Supported chains: mainnet, sepolia, " +
			"holesky, hoodi.",
		params: obj(map[string]any{
			"name":    strProp("ENS name to forward-resolve (e.g. vitalik.eth)"),
			"address": strProp("Address to reverse-resolve (0x…)"),
		}),
		run: runENS,
	}
	return []tools.Tool{call, send, erc20, erc721, ens}
}

// RegisterContracts registers the contract/token/ENS tools.
func RegisterContracts(reg *tools.ToolRegistry, deps *SigningDeps) {
	if reg == nil || deps == nil || deps.Stack == nil {
		return
	}
	for _, t := range ContractTools(deps) {
		reg.Register(t)
	}
}

// --- shared helpers ---

func argsArg(args map[string]any) []any {
	if v, ok := args["args"].([]any); ok {
		return v
	}
	return nil
}

// resolveContract resolves the contract arg and returns address + ABI.
func resolveContract(
	ctx context.Context,
	d *SigningDeps,
	args map[string]any,
	key string,
) (*web3.ResolvedContract, error) {
	name := strArg(args, key)
	if name == "" {
		return nil, fmt.Errorf("%s is required", key)
	}
	return web3.ResolveContract(ctx, d.Provider, d.Stack.Registry, name)
}

// contractCall eth_calls m on addr and decodes outputs.
func contractCall(ctx context.Context, d *SigningDeps, addr string,
	m *web3.ABIMethod, args []any, block string,
) ([]any, error) {
	data, err := m.PackArgs(args)
	if err != nil {
		return nil, fmt.Errorf("encode args: %w", err)
	}
	if block == "" {
		block = "latest"
	}
	raw, err := d.Provider.Call(ctx, "eth_call", []any{
		map[string]any{"to": addr, "data": "0x" + hex.EncodeToString(data)}, block,
	})
	if err != nil {
		return nil, err
	}
	var ret string
	if err := json.Unmarshal(raw, &ret); err != nil {
		return nil, fmt.Errorf("eth_call: unexpected result %s", raw)
	}
	out, err := web3.ParseHexBytes(ret)
	if err != nil {
		return nil, fmt.Errorf("eth_call result: %w", err)
	}
	if len(out) == 0 {
		if len(m.Outputs) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("empty return — wrong contract or method reverted")
	}
	return m.UnpackOutputs(out)
}

// queueContractSend builds the pending entry for a contract write.
func queueContractSend(ctx context.Context, d *SigningDeps, tool string,
	rc *web3.ResolvedContract, m *web3.ABIMethod, args []any,
	value *big.Int, from, reason string,
) (any, error) {
	data, err := m.PackArgs(args)
	if err != nil {
		return nil, fmt.Errorf("encode args: %w", err)
	}
	chainID, err := liveChainID(ctx, d)
	if err != nil {
		return nil, err
	}
	display := rc.Address
	if rc.Label != "" {
		display = rc.Label + "(" + rc.Address + ")"
	}
	summary := fmt.Sprintf("%s.%s(%s) from %s on chain %d",
		display, m.Name, joinArgs(args), from, chainID)
	if value != nil && value.Sign() > 0 {
		summary += fmt.Sprintf(" with %s wei", value)
	}
	if reason != "" {
		summary += " — " + reason
	}
	req := &web3.SignRequest{
		Kind: string(web3.KindContract), ChainID: chainID, From: from,
		To: rc.Address, ValueWei: value, Data: data,
	}
	if err := evaluatePolicy(ctx, d, tool, req, summary); err != nil {
		d.emit("web3.contract.rejected", map[string]any{
			"to": rc.Address, "method": m.Name, "reason": err.Error(),
		})
		return nil, err
	}
	e, err := submitPending(d, &web3.PendingEntry{
		Kind: web3.KindContract, ChainID: chainID, From: from, To: rc.Address,
		ValueWei: value.String(), Data: "0x" + hex.EncodeToString(data),
		Selector: req.SelectorHex(), Summary: summary,
	})
	if err != nil {
		return nil, err
	}
	d.emit("web3.contract.send", map[string]any{
		"id": e.ID, "to": rc.Address, "method": m.Name, "from": from,
	})
	return pendingResult(e), nil
}

func joinArgs(args []any) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		b, _ := json.Marshal(a)
		parts = append(parts, string(b))
	}
	return strings.Join(parts, ", ")
}

// requireSigning errors when the write path is gated off.
func requireSigning(d *SigningDeps, tool string) error {
	if d.Stack == nil || d.Stack.Policy == nil || !d.Stack.Policy.Enabled {
		return fmt.Errorf("%s write actions need tools.web3.signing.enabled", tool)
	}
	return nil
}

// tokenAmount converts a token-unit decimal string to base units using the
// token's decimals() — e.g. "5.25" with 6 decimals → 5250000.
func tokenAmount(ctx context.Context, d *SigningDeps, token *web3.ABI,
	addr, amount string,
) (*big.Int, error) {
	dec := uint64(18)
	if m, err := token.Method("decimals", 0); err == nil {
		out, err := contractCall(ctx, d, addr, m, nil, "latest")
		if err == nil && len(out) == 1 {
			if s, ok := out[0].(string); ok {
				if v, ok := new(big.Int).SetString(s, 10); ok && v.IsUint64() && v.Uint64() <= 77 {
					dec = v.Uint64()
				}
			}
		}
	}
	r, ok := new(big.Rat).SetString(strings.TrimSpace(amount))
	if !ok || r.Sign() < 0 {
		return nil, fmt.Errorf("invalid amount %q", amount)
	}
	scale := new(big.Int).Exp(big.NewInt(10), new(big.Int).SetUint64(dec), nil)
	r.Mul(r, new(big.Rat).SetInt(scale))
	if !r.IsInt() {
		return nil, fmt.Errorf("amount %q is finer than 1 base unit (%d decimals)", amount, dec)
	}
	return r.Num(), nil
}

// --- tool implementations ---

func runContractCall(ctx context.Context, d *SigningDeps, args map[string]any) (any, error) {
	rc, err := resolveContract(ctx, d, args, "contract")
	if err != nil {
		return nil, err
	}
	if rc.ABI == nil {
		return nil, fmt.Errorf("no ABI for %s — register one with `rhizome web3 abi add`", rc.Address)
	}
	callArgs := argsArg(args)
	m, err := rc.ABI.Method(strArg(args, "method"), len(callArgs))
	if err != nil {
		return nil, err
	}
	if !m.IsReadOnly() {
		return nil, fmt.Errorf("%s is not read-only — use web3_contract_send", m.Name)
	}
	out, err := contractCall(ctx, d, rc.Address, m, callArgs, strArg(args, "block"))
	if err != nil {
		return nil, err
	}
	d.emit("web3.contract.call", map[string]any{
		"to": rc.Address, "method": m.Name, "label": rc.Label,
	})
	return map[string]any{
		"contract": rc.Address, "label": rc.Label, "method": m.Signature(),
		"outputs": out,
	}, nil
}

func runContractSend(ctx context.Context, d *SigningDeps, args map[string]any) (any, error) {
	rc, err := resolveContract(ctx, d, args, "contract")
	if err != nil {
		return nil, err
	}
	if rc.ABI == nil {
		return nil, fmt.Errorf("no ABI for %s — register one with `rhizome web3 abi add`", rc.Address)
	}
	callArgs := argsArg(args)
	m, err := rc.ABI.Method(strArg(args, "method"), len(callArgs))
	if err != nil {
		return nil, err
	}
	if m.IsReadOnly() {
		return nil, fmt.Errorf("%s is read-only — use web3_contract_call", m.Name)
	}
	value, err := parseWeiArg(strArg(args, "value"))
	if err != nil {
		return nil, err
	}
	if m.StateMutability != "payable" && value.Sign() > 0 {
		return nil, fmt.Errorf("%s is not payable — drop value", m.Name)
	}
	from, err := resolveFrom(d, args)
	if err != nil {
		return nil, err
	}
	return queueContractSend(ctx, d, "web3_contract_send", rc, m, callArgs,
		value, from, strArg(args, "reason"))
}

func runERC20(ctx context.Context, d *SigningDeps, args map[string]any) (any, error) {
	action := strings.ToLower(strArg(args, "action"))
	rc, err := resolveContract(ctx, d, args, "token")
	if err != nil {
		return nil, err
	}
	abi := erc20ABI
	if rc.ABI != nil {
		abi = rc.ABI // a registered ABI wins — it may extend the surface
	}
	call := func(name string, callArgs []any) ([]any, error) {
		m, err := abi.Method(name, len(callArgs))
		if err != nil {
			return nil, err
		}
		return contractCall(ctx, d, rc.Address, m, callArgs, "latest")
	}
	owner := strArg(args, "owner")
	if owner == "" {
		owner, _ = d.Stack.Wallets.Default()
	}
	switch action {
	case "info":
		name, _ := call("name", nil)
		symbol, _ := call("symbol", nil)
		decimals, _ := call("decimals", nil)
		supply, _ := call("totalSupply", nil)
		return map[string]any{
			"token": rc.Address, "label": rc.Label,
			"name": firstOf(name), "symbol": firstOf(symbol),
			"decimals": firstOf(decimals), "total_supply": firstOf(supply),
		}, nil
	case "balance":
		out, err := call("balanceOf", []any{owner})
		if err != nil {
			return nil, err
		}
		return map[string]any{"token": rc.Address, "owner": owner, "balance_units": firstOf(out)}, nil
	case "allowance":
		spender := strArg(args, "spender")
		if !web3.IsAddress(spender) {
			return nil, fmt.Errorf("spender is required for allowance")
		}
		out, err := call("allowance", []any{owner, spender})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"token": rc.Address, "owner": owner, "spender": spender,
			"allowance_units": firstOf(out),
		}, nil
	case "transfer", "approve":
		if err := requireSigning(d, "web3_erc20"); err != nil {
			return nil, err
		}
		amount, err := erc20Amount(ctx, d, abi, rc.Address, args)
		if err != nil {
			return nil, err
		}
		from, err := resolveFrom(d, args)
		if err != nil {
			return nil, err
		}
		var m *web3.ABIMethod
		var callArgs []any
		if action == "transfer" {
			to := strArg(args, "to")
			if !web3.IsAddress(to) {
				return nil, fmt.Errorf("to is required for transfer")
			}
			m, err = erc20ABI.Method("transfer", 2)
			callArgs = []any{to, amount}
		} else {
			spender := strArg(args, "spender")
			if !web3.IsAddress(spender) {
				return nil, fmt.Errorf("spender is required for approve")
			}
			m, err = erc20ABI.Method("approve", 2)
			callArgs = []any{spender, amount}
		}
		if err != nil {
			return nil, err
		}
		return queueContractSend(ctx, d, "web3_erc20", rc, m, callArgs,
			new(big.Int), from, strArg(args, "reason"))
	default:
		return nil, fmt.Errorf("unknown action %q — info|balance|allowance|transfer|approve", action)
	}
}

// erc20Amount resolves amount|amount_units into base units.
func erc20Amount(ctx context.Context, d *SigningDeps, abi *web3.ABI,
	token string, args map[string]any,
) (*big.Int, error) {
	if raw := strArg(args, "amount_units"); raw != "" {
		return parseWeiArg(raw)
	}
	amount := strArg(args, "amount")
	if strings.EqualFold(amount, "unlimited") {
		return new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)), nil
	}
	if amount == "" {
		return nil, fmt.Errorf("amount (token units) or amount_units (base units) is required")
	}
	return tokenAmount(ctx, d, abi, token, amount)
}

func runERC721(ctx context.Context, d *SigningDeps, args map[string]any) (any, error) {
	action := strings.ToLower(strArg(args, "action"))
	rc, err := resolveContract(ctx, d, args, "token")
	if err != nil {
		return nil, err
	}
	call := func(name string, callArgs []any) ([]any, error) {
		m, err := erc721ABI.Method(name, len(callArgs))
		if err != nil {
			return nil, err
		}
		return contractCall(ctx, d, rc.Address, m, callArgs, "latest")
	}
	tokenID := func() (*big.Int, error) {
		v := strArg(args, "token_id")
		n, ok := new(big.Int).SetString(v, 10)
		if !ok || n.Sign() < 0 {
			return nil, fmt.Errorf("token_id must be a decimal integer")
		}
		return n, nil
	}
	switch action {
	case "name", "symbol":
		out, err := call(action, nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"token": rc.Address, action: firstOf(out)}, nil
	case "ownerof":
		id, err := tokenID()
		if err != nil {
			return nil, err
		}
		out, err := call("ownerOf", []any{id})
		if err != nil {
			return nil, err
		}
		return map[string]any{"token": rc.Address, "token_id": id.String(), "owner": firstOf(out)}, nil
	case "balanceof":
		owner := strArg(args, "owner")
		if owner == "" {
			owner, _ = d.Stack.Wallets.Default()
		}
		out, err := call("balanceOf", []any{owner})
		if err != nil {
			return nil, err
		}
		return map[string]any{"token": rc.Address, "owner": owner, "balance": firstOf(out)}, nil
	case "tokenuri":
		id, err := tokenID()
		if err != nil {
			return nil, err
		}
		out, err := call("tokenURI", []any{id})
		if err != nil {
			return nil, err
		}
		return map[string]any{"token": rc.Address, "token_id": id.String(), "uri": firstOf(out)}, nil
	case "transferfrom", "safetransferfrom":
		if err := requireSigning(d, "web3_erc721"); err != nil {
			return nil, err
		}
		id, err := tokenID()
		if err != nil {
			return nil, err
		}
		to := strArg(args, "to")
		if !web3.IsAddress(to) {
			return nil, fmt.Errorf("to is required for %s", action)
		}
		from, err := resolveFrom(d, args)
		if err != nil {
			return nil, err
		}
		method := "transferFrom"
		if action == "safetransferfrom" {
			method = "safeTransferFrom"
		}
		m, err := erc721ABI.Method(method, 3)
		if err != nil {
			return nil, err
		}
		return queueContractSend(ctx, d, "web3_erc721", rc, m,
			[]any{from, to, id}, new(big.Int), from, strArg(args, "reason"))
	default:
		return nil, fmt.Errorf("unknown action %q", action)
	}
}

func runENS(ctx context.Context, d *SigningDeps, args map[string]any) (any, error) {
	if d.Provider == nil {
		return nil, fmt.Errorf("no web3 endpoint configured")
	}
	if name := strArg(args, "name"); name != "" {
		addr, err := web3.ENSResolve(ctx, d.Provider, name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"name": name, "address": addr}, nil
	}
	if addr := strArg(args, "address"); addr != "" {
		name, err := web3.ENSReverse(ctx, d.Provider, addr)
		if err != nil {
			return nil, err
		}
		return map[string]any{"address": addr, "name": name}, nil
	}
	return nil, fmt.Errorf("pass name (forward) or address (reverse)")
}

func firstOf(out []any) any {
	if len(out) == 0 {
		return nil
	}
	return out[0]
}
