// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// Package web3tools provides the web3_* agent tools: read-only Ethereum
// JSON-RPC access backed by pkg/web3. All endpoints are operator-configured
// (tools.web3.endpoint, the ethereum-rpc module, or a running
// nimbus-verified-proxy) — tools never accept URLs at call time.
package web3tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/tools"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// maxFullBlockTxs caps full transaction decoding in web3_block.
const maxFullBlockTxs = 50

// maxLogResults caps decoded logs returned by web3_logs.
const maxLogResults = 100

// knownNetworks maps common chain IDs to display names.
var knownNetworks = map[uint64]string{
	1: "ethereum-mainnet", 5: "goerli", 10: "optimism", 56: "bsc",
	137: "polygon", 8453: "base", 42161: "arbitrum-one",
	11155111: "sepolia", 17000: "holesky", 560048: "hoodi",
	31337: "local-devnet", 1337: "local-devnet",
}

// Tool is a single web3_* tool bound to a shared endpoint Provider.
type Tool struct {
	provider *web3.Provider
	cfg      *config.Web3ToolsConfig
	name     string
	desc     string
	params   map[string]any
	run      func(ctx context.Context, p *web3.Provider, cfg *config.Web3ToolsConfig, args map[string]any) (any, error)
}

func (t *Tool) Name() string               { return t.name }
func (t *Tool) Description() string        { return t.desc }
func (t *Tool) Parameters() map[string]any { return t.params }

func (t *Tool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	out, err := t.run(ctx, t.provider, t.cfg, args)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s failed: %v", t.name, err))
	}
	data, err := json.Marshal(out)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: marshal result: %v", t.name, err))
	}
	return tools.SilentResult(string(data))
}

func strArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func boolArg(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

// blockArg normalizes an optional block argument (default "latest").
func blockArg(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return "latest", nil
	}
	return web3.NormalizeBlockTag(v)
}

// callRaw invokes a method and returns the raw JSON result.
func callRaw(ctx context.Context, p *web3.Provider, method string, params []any) (json.RawMessage, error) {
	return p.Call(ctx, method, params)
}

func quantityArg(ctx context.Context, p *web3.Provider, method string, params []any) (string, error) {
	raw, err := callRaw(ctx, p, method, params)
	if err != nil {
		return "", err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%s: expected quantity string, got %s", method, raw)
	}
	return s, nil
}

// Tools constructs the web3_* tool set bound to provider.
func Tools(provider *web3.Provider, cfg *config.Web3ToolsConfig) []tools.Tool {
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
	blockProp := strProp(
		"Block: \"latest\" (default), \"earliest\", \"pending\", \"safe\", \"finalized\", or a block number")

	chain := &Tool{
		provider: provider, cfg: cfg, name: "web3_chain",
		desc: "Read the connected Ethereum network: chain ID, network name, latest block number, " +
			"gas price, and sync status. The endpoint is resolved from operator configuration " +
			"(tools.web3.endpoint → ethereum-rpc module → running nimbus-verified-proxy).",
		params: obj(map[string]any{}),
		run:    runChain,
	}
	balance := &Tool{
		provider: provider, cfg: cfg, name: "web3_balance",
		desc: "Get the ETH balance of an address (eth_getBalance). Returns wei and ether.",
		params: obj(map[string]any{
			"address": strProp("0x-prefixed 20-byte Ethereum address"),
			"block":   blockProp,
		}, "address"),
		run: runBalance,
	}
	call := &Tool{
		provider: provider, cfg: cfg, name: "web3_call",
		desc: "Execute a read-only contract call (eth_call). Returns raw ABI-encoded data. " +
			"Does not broadcast or alter state.",
		params: obj(map[string]any{
			"to":    strProp("Contract address (0x…)"),
			"data":  strProp("ABI-encoded call data (0x…)"),
			"from":  strProp("Optional sender address (0x…)"),
			"value": strProp("Optional wei value (hex or decimal)"),
			"block": blockProp,
		}, "to", "data"),
		run: runCall,
	}
	block := &Tool{
		provider: provider, cfg: cfg, name: "web3_block",
		desc: "Get a block by number or tag (eth_getBlockByNumber). Returns header fields and " +
			"transaction hashes; full_transactions=true returns decoded transactions (capped at 50).",
		params: obj(map[string]any{
			"block": blockProp,
			"full_transactions": map[string]any{
				"type":        "boolean",
				"description": "Return decoded transactions instead of hashes (default false, max 50)",
			},
		}),
		run: runBlock,
	}
	transaction := &Tool{
		provider: provider, cfg: cfg, name: "web3_transaction",
		desc: "Get a transaction and its receipt by hash. Receipt status is success/reverted; " +
			"a missing receipt means the transaction is still pending.",
		params: obj(map[string]any{
			"hash": strProp("0x-prefixed 32-byte transaction hash"),
		}, "hash"),
		run: runTransaction,
	}
	logs := &Tool{
		provider: provider, cfg: cfg, name: "web3_logs",
		desc: "Query event logs (eth_getLogs) by address and/or topics over a bounded block " +
			"range (tools.web3.max_log_range, default 10000). Returns at most 100 entries.",
		params: obj(map[string]any{
			"address":    strProp("Contract address to filter (0x…)"),
			"from_block": strProp("Start block: tag or number (default \"latest\")"),
			"to_block":   strProp("End block: tag or number (default \"latest\")"),
			"topics": map[string]any{
				"type":        "array",
				"description": "Topic filters (0x32-byte values; null matches any)",
				"items":       map[string]any{"type": "string"},
			},
		}),
		run: runLogs,
	}
	rpc := &Tool{
		provider: provider, cfg: cfg, name: "web3_rpc",
		desc: "Call any read-only Ethereum JSON-RPC method not covered by the other web3_* tools. " +
			"Restricted to a read-only allowlist (eth_call, eth_get*, net_*, web3_* — " +
			"send/sign/subscribe/admin methods are denied).",
		params: obj(map[string]any{
			"method": strProp("JSON-RPC method name (must be on the read-only allowlist)"),
			"params": map[string]any{
				"type":        "array",
				"description": "Positional params array (default [])",
			},
		}, "method"),
		run: runRPC,
	}

	return []tools.Tool{chain, balance, call, block, transaction, logs, rpc}
}

// Register registers all web3_* tools into reg.
func Register(reg *tools.ToolRegistry, provider *web3.Provider, cfg *config.Web3ToolsConfig) {
	if reg == nil || provider == nil {
		return
	}
	if cfg == nil {
		cfg = &config.Web3ToolsConfig{}
	}
	for _, t := range Tools(provider, cfg) {
		reg.Register(t)
	}
}

// --- tool implementations ---

type chainResult struct {
	ChainID           string `json:"chain_id"`
	ChainIDDec        uint64 `json:"chain_id_dec"`
	Network           string `json:"network"`
	BlockNumber       uint64 `json:"block_number"`
	GasPriceWei       string `json:"gas_price_wei"`
	MaxPriorityFeeWei string `json:"max_priority_fee_wei,omitempty"`
	Syncing           any    `json:"syncing"`
	EndpointSource    string `json:"endpoint_source"`
}

func runChain(ctx context.Context, p *web3.Provider, _ *config.Web3ToolsConfig, _ map[string]any) (any, error) {
	idHex, err := quantityArg(ctx, p, "eth_chainId", nil)
	if err != nil {
		return nil, err
	}
	id, err := web3.QuantityUint64(idHex)
	if err != nil {
		return nil, err
	}
	out := chainResult{ChainID: idHex, ChainIDDec: id, EndpointSource: p.Source()}
	if name, ok := knownNetworks[id]; ok {
		out.Network = name
	} else {
		out.Network = fmt.Sprintf("chain-%d", id)
	}
	if bn, err := quantityArg(ctx, p, "eth_blockNumber", nil); err == nil {
		out.BlockNumber, _ = web3.QuantityUint64(bn)
	}
	if gp, err := quantityArg(ctx, p, "eth_gasPrice", nil); err == nil {
		out.GasPriceWei = gp
	}
	if mp, err := quantityArg(ctx, p, "eth_maxPriorityFeePerGas", nil); err == nil {
		out.MaxPriorityFeeWei = mp
	}
	// eth_syncing returns false or a progress object — pass through raw.
	if raw, err := callRaw(ctx, p, "eth_syncing", nil); err == nil {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			out.Syncing = v
		}
	}
	return out, nil
}

func runBalance(ctx context.Context, p *web3.Provider, _ *config.Web3ToolsConfig, args map[string]any) (any, error) {
	addr := strArg(args, "address")
	if !web3.IsAddress(addr) {
		return nil, fmt.Errorf("address %q is not a valid 0x-prefixed 20-byte address", addr)
	}
	tag, err := blockArg(args, "block")
	if err != nil {
		return nil, err
	}
	hexBal, err := quantityArg(ctx, p, "eth_getBalance", []any{addr, tag})
	if err != nil {
		return nil, err
	}
	wei, err := web3.ParseQuantity(hexBal)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"address": addr, "block": tag,
		"wei": hexBal, "ether": web3.EtherString(wei),
	}, nil
}

func runCall(ctx context.Context, p *web3.Provider, _ *config.Web3ToolsConfig, args map[string]any) (any, error) {
	to := strArg(args, "to")
	if !web3.IsAddress(to) {
		return nil, fmt.Errorf("to %q is not a valid 0x-prefixed address", to)
	}
	data := strArg(args, "data")
	if !web3.IsHexData(data) {
		return nil, fmt.Errorf("data %q is not valid 0x-prefixed hex", data)
	}
	tx := map[string]any{"to": to, "data": data}
	if from := strArg(args, "from"); from != "" {
		if !web3.IsAddress(from) {
			return nil, fmt.Errorf("from %q is not a valid 0x-prefixed address", from)
		}
		tx["from"] = from
	}
	if v := strArg(args, "value"); v != "" {
		q, err := web3.NormalizeQuantity(v)
		if err != nil {
			return nil, fmt.Errorf("value: %w", err)
		}
		tx["value"] = q
	}
	tag, err := blockArg(args, "block")
	if err != nil {
		return nil, err
	}
	raw, err := callRaw(ctx, p, "eth_call", []any{tx, tag})
	if err != nil {
		return nil, err
	}
	var ret string
	if err := json.Unmarshal(raw, &ret); err != nil {
		return nil, fmt.Errorf("eth_call: unexpected result %s", raw)
	}
	return map[string]any{"return_data": ret, "to": to, "block": tag}, nil
}

func runBlock(ctx context.Context, p *web3.Provider, _ *config.Web3ToolsConfig, args map[string]any) (any, error) {
	tag, err := blockArg(args, "block")
	if err != nil {
		return nil, err
	}
	full := boolArg(args, "full_transactions")
	raw, err := callRaw(ctx, p, "eth_getBlockByNumber", []any{tag, full})
	if err != nil {
		return nil, err
	}
	b, truncated, err := web3.DecodeBlock(raw, full, maxFullBlockTxs)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, fmt.Errorf("block %q not found", tag)
	}
	return map[string]any{"block": b, "transactions_truncated": truncated}, nil
}

func runTransaction(
	ctx context.Context, p *web3.Provider, _ *config.Web3ToolsConfig, args map[string]any,
) (any, error) {
	hash := strArg(args, "hash")
	if !web3.IsHash32(hash) {
		return nil, fmt.Errorf("hash %q is not a valid 0x-prefixed 32-byte hash", hash)
	}
	rawTx, err := callRaw(ctx, p, "eth_getTransactionByHash", []any{hash})
	if err != nil {
		return nil, err
	}
	tx, err := web3.DecodeTransaction(rawTx)
	if err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, fmt.Errorf("transaction %s not found", hash)
	}
	rawRcpt, err := callRaw(ctx, p, "eth_getTransactionReceipt", []any{hash})
	if err != nil {
		return nil, err
	}
	rcpt, err := web3.DecodeReceipt(rawRcpt)
	if err != nil {
		return nil, err
	}
	status := "pending"
	if rcpt != nil {
		status = rcpt.Status
	}
	return map[string]any{
		"transaction": tx, "receipt": rcpt, "status": status,
	}, nil
}

func runLogs(ctx context.Context, p *web3.Provider, cfg *config.Web3ToolsConfig, args map[string]any) (any, error) {
	from, err := blockArg(args, "from_block")
	if err != nil {
		return nil, err
	}
	to, err := blockArg(args, "to_block")
	if err != nil {
		return nil, err
	}
	// Enforce the range cap even when bounds are tags — resolve them to
	// concrete numbers first so "earliest..latest" cannot bypass
	// max_log_range. The original tag strings stay in the filter.
	f, err := resolveBlockNum(ctx, p, from)
	if err != nil {
		return nil, fmt.Errorf("from_block %q: %w", from, err)
	}
	t, err := resolveBlockNum(ctx, p, to)
	if err != nil {
		return nil, fmt.Errorf("to_block %q: %w", to, err)
	}
	if t < f {
		return nil, fmt.Errorf("from_block %d exceeds to_block %d", f, t)
	}
	maxRange := cfg.GetMaxLogRange()
	if t-f+1 > maxRange {
		return nil, fmt.Errorf(
			"log range %d blocks exceeds tools.web3.max_log_range (%d) — narrow from_block/to_block",
			t-f+1, maxRange)
	}
	filter := map[string]any{"fromBlock": from, "toBlock": to}
	if addr := strArg(args, "address"); addr != "" {
		if !web3.IsAddress(addr) {
			return nil, fmt.Errorf("address %q is not a valid 0x-prefixed address", addr)
		}
		filter["address"] = addr
	}
	if tv, ok := args["topics"].([]any); ok && len(tv) > 0 {
		topics := make([]any, 0, len(tv))
		for i, t := range tv {
			if t == nil {
				topics = append(topics, nil)
				continue
			}
			s, ok := t.(string)
			if !ok || !web3.IsHash32(s) {
				return nil, fmt.Errorf("topics[%d] must be a 0x-prefixed 32-byte value or null", i)
			}
			topics = append(topics, s)
		}
		filter["topics"] = topics
	}
	raw, err := callRaw(ctx, p, "eth_getLogs", []any{filter})
	if err != nil {
		return nil, err
	}
	logs, truncated, err := web3.DecodeLogs(raw, maxLogResults)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"logs": logs, "count": len(logs), "truncated": truncated,
	}, nil
}

// resolveBlockNum turns a normalized block arg (tag or 0x quantity) into
// a concrete block number. Tags cost one extra RPC call.
func resolveBlockNum(ctx context.Context, p *web3.Provider, tag string) (uint64, error) {
	switch tag {
	case "earliest":
		return 0, nil
	case "latest", "pending":
		hex, err := quantityArg(ctx, p, "eth_blockNumber", nil)
		if err != nil {
			return 0, err
		}
		return web3.QuantityUint64(hex)
	case "safe", "finalized":
		raw, err := callRaw(ctx, p, "eth_getBlockByNumber", []any{tag, false})
		if err != nil {
			return 0, err
		}
		b, _, err := web3.DecodeBlock(raw, false, 0)
		if err != nil {
			return 0, err
		}
		if b == nil {
			return 0, fmt.Errorf("endpoint returned no %q block", tag)
		}
		return b.Number, nil
	default:
		return web3.QuantityUint64(tag)
	}
}

func runRPC(ctx context.Context, p *web3.Provider, _ *config.Web3ToolsConfig, args map[string]any) (any, error) {
	method := strArg(args, "method")
	if method == "" {
		return nil, fmt.Errorf("method is required")
	}
	if !web3.IsReadOnlyMethod(method) {
		return nil, fmt.Errorf(
			"method %q is not on the read-only allowlist — web3 tools cannot send, sign, or administer",
			method)
	}
	params, _ := args["params"].([]any)
	raw, err := callRaw(ctx, p, method, params)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		return map[string]any{"result": v}, nil
	}
	return map[string]any{"result": string(raw)}, nil
}
