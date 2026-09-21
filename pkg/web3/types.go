// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// Package web3 provides a minimal Ethereum JSON-RPC layer: a stdlib
// net/http client, an operator-config endpoint resolver, the hex
// quantity / block / transaction / receipt / log codecs the web3_* read
// tools are built on, and — gated behind tools.web3.signing — the local
// wallet (keys.go), ABI codec (abi.go), transaction signing (tx.go),
// policy engine (policy.go), and pending-approval queue (pending.go).
// Private keys are AES-256-GCM sealed under a master key in the OS
// keyring (scrypt passphrase fallback) and never leave the local box.
package web3

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Quantity is an Ethereum hex-quantity ("0x0", "0x1a2b"). The zero value
// renders as "0x0".
type Quantity = big.Int

// ParseQuantity parses a 0x-prefixed hex quantity per the JSON-RPC spec:
// required "0x" prefix, at least one digit, no leading zeros except "0x0".
func ParseQuantity(s string) (*big.Int, error) {
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return nil, fmt.Errorf("quantity %q missing 0x prefix", s)
	}
	digits := s[2:]
	if digits == "" {
		return nil, fmt.Errorf("quantity %q has no digits", s)
	}
	if len(digits) > 1 && digits[0] == '0' {
		return nil, fmt.Errorf("quantity %q has leading zeros", s)
	}
	// Validate digits ourselves: big.Int.SetString accepts sign prefixes
	// and, in base 16, a "0x" prefix — quantities are unsigned.
	if !isHex(digits) {
		return nil, fmt.Errorf("invalid quantity %q", s)
	}
	n, _ := new(big.Int).SetString(digits, 16)
	return n, nil
}

// QuantityString renders n as a canonical 0x-prefixed hex quantity.
func QuantityString(n *big.Int) string {
	if n == nil {
		return "0x0"
	}
	return "0x" + n.Text(16)
}

// parseDecimalQuantity parses a bare decimal string into a non-negative
// quantity value. Sign prefixes are rejected — Ethereum quantities are
// unsigned, and big.Int.SetString would otherwise accept "+1"/"-1".
func parseDecimalQuantity(s string) (*big.Int, error) {
	if s != "" && (s[0] == '-' || s[0] == '+') {
		return nil, fmt.Errorf("invalid quantity %q", s)
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("invalid quantity %q", s)
	}
	return n, nil
}

// NormalizeQuantity accepts a 0x-prefixed hex or bare decimal string and
// returns the canonical hex quantity form.
func NormalizeQuantity(v string) (string, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "0x") || strings.HasPrefix(v, "0X") {
		n, err := ParseQuantity(v)
		if err != nil {
			return "", err
		}
		return QuantityString(n), nil
	}
	n, err := parseDecimalQuantity(v)
	if err != nil {
		return "", err
	}
	return QuantityString(n), nil
}

// QuantityUint64 parses a quantity into uint64 (block numbers, counts).
func QuantityUint64(s string) (uint64, error) {
	n, err := ParseQuantity(s)
	if err != nil {
		return 0, err
	}
	if !n.IsUint64() {
		return 0, fmt.Errorf("quantity %q overflows uint64", s)
	}
	return n.Uint64(), nil
}

// Block tags accepted by every method taking a block parameter.
var blockTags = map[string]bool{
	"latest": true, "earliest": true, "pending": true,
	"safe": true, "finalized": true,
}

// NormalizeBlockTag converts a user-supplied block argument into a valid
// JSON-RPC block parameter: a named tag ("latest", "safe", …) or a hex
// quantity. Decimal integers are converted to hex for convenience.
func NormalizeBlockTag(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "latest", nil
	case string:
		s := strings.TrimSpace(strings.ToLower(t))
		if s == "" {
			return "latest", nil
		}
		if blockTags[s] {
			return s, nil
		}
		if strings.HasPrefix(s, "0x") {
			if _, err := ParseQuantity(s); err != nil {
				return "", fmt.Errorf("invalid block quantity: %w", err)
			}
			return s, nil
		}
		// Bare decimal block numbers are accepted for convenience.
		if n, err := parseDecimalQuantity(s); err == nil {
			return QuantityString(n), nil
		}
		return "", fmt.Errorf(
			"invalid block tag %q (expected latest/earliest/pending/safe/finalized or a block number)", t)
	case float64:
		if t < 0 || t != float64(uint64(t)) {
			return "", fmt.Errorf("invalid block number %v", t)
		}
		return QuantityString(new(big.Int).SetUint64(uint64(t))), nil
	case json.Number:
		n, err := parseDecimalQuantity(t.String())
		if err != nil {
			return "", fmt.Errorf("invalid block number %q", t.String())
		}
		return QuantityString(n), nil
	default:
		return "", fmt.Errorf("invalid block argument of type %T", v)
	}
}

// IsAddress reports whether s is a 0x-prefixed 20-byte hex address.
func IsAddress(s string) bool {
	return len(s) == 42 && strings.HasPrefix(s, "0x") && isHex(s[2:])
}

// IsHash32 reports whether s is a 0x-prefixed 32-byte hex value.
func IsHash32(s string) bool {
	return len(s) == 66 && strings.HasPrefix(s, "0x") && isHex(s[2:])
}

// IsHexData reports whether s is 0x-prefixed even-length hex (call data).
func IsHexData(s string) bool {
	if !strings.HasPrefix(s, "0x") || len(s)%2 != 0 {
		return false
	}
	return isHex(s[2:])
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// EtherString renders a wei amount as a decimal ether string (18 dp,
// trimmed). Uses big.Rat — no float rounding.
func EtherString(wei *big.Int) string {
	if wei == nil {
		return "0"
	}
	r := new(big.Rat).SetFrac(wei, bigPow10(18))
	s := r.FloatString(18)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return "0"
	}
	return s
}

func bigPow10(exp int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil)
}

// Block is the decoded eth_getBlockBy* result relevant to agents.
type Block struct {
	Number           uint64        `json:"number"`
	Hash             string        `json:"hash"`
	ParentHash       string        `json:"parent_hash"`
	Timestamp        uint64        `json:"timestamp"`
	Miner            string        `json:"miner,omitempty"`
	GasUsed          uint64        `json:"gas_used"`
	GasLimit         uint64        `json:"gas_limit"`
	BaseFeeWei       string        `json:"base_fee_wei,omitempty"`
	TransactionCount int           `json:"transaction_count"`
	Transactions     []string      `json:"transactions,omitempty"` // hashes, unless full requested
	FullTransactions []Transaction `json:"full_transactions,omitempty"`
}

type rawBlock struct {
	Number       string            `json:"number"`
	Hash         string            `json:"hash"`
	ParentHash   string            `json:"parentHash"`
	Timestamp    string            `json:"timestamp"`
	Miner        string            `json:"miner"`
	GasUsed      string            `json:"gasUsed"`
	GasLimit     string            `json:"gasLimit"`
	BaseFee      string            `json:"baseFeePerGas"`
	Transactions []json.RawMessage `json:"transactions"`
}

// DecodeBlock decodes an eth_getBlockBy* result. When full is true the
// transactions array is decoded into typed transactions (capped by
// maxFull); otherwise only hashes are kept.
func DecodeBlock(raw json.RawMessage, full bool, maxFull int) (*Block, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	var rb rawBlock
	if err := json.Unmarshal(raw, &rb); err != nil {
		return nil, false, fmt.Errorf("decode block: %w", err)
	}
	b := &Block{
		Hash:       rb.Hash,
		ParentHash: rb.ParentHash,
		Miner:      rb.Miner,
	}
	var err error
	if b.Number, err = QuantityUint64(rb.Number); err != nil {
		return nil, false, fmt.Errorf("block number: %w", err)
	}
	if b.Timestamp, err = QuantityUint64(rb.Timestamp); err != nil {
		return nil, false, fmt.Errorf("block timestamp: %w", err)
	}
	b.GasUsed, _ = QuantityUint64(rb.GasUsed)
	b.GasLimit, _ = QuantityUint64(rb.GasLimit)
	b.BaseFeeWei = rb.BaseFee
	b.TransactionCount = len(rb.Transactions)
	truncated := false
	if full {
		limit := len(rb.Transactions)
		if maxFull > 0 && limit > maxFull {
			limit = maxFull
			truncated = true
		}
		for i := 0; i < limit; i++ {
			tx, err := DecodeTransaction(rb.Transactions[i])
			if err != nil || tx == nil {
				continue
			}
			b.FullTransactions = append(b.FullTransactions, *tx)
		}
	} else {
		for _, rtx := range rb.Transactions {
			var h string
			if json.Unmarshal(rtx, &h) == nil {
				b.Transactions = append(b.Transactions, h)
			}
		}
	}
	return b, truncated, nil
}

// Transaction is the decoded eth_getTransaction* result.
type Transaction struct {
	Hash        string `json:"hash"`
	From        string `json:"from"`
	To          string `json:"to,omitempty"` // empty for contract creation
	ValueWei    string `json:"value_wei"`
	ValueEther  string `json:"value_ether"`
	Nonce       uint64 `json:"nonce"`
	Gas         uint64 `json:"gas"`
	GasPriceWei string `json:"gas_price_wei,omitempty"`
	BlockNumber uint64 `json:"block_number,omitempty"`
	Input       string `json:"input,omitempty"` // truncated to 138 chars
}

const maxInputDisplay = 138 // 0x + 4-byte selector + 64 bytes

// DecodeTransaction decodes one tx object from a JSON-RPC result.
func DecodeTransaction(raw json.RawMessage) (*Transaction, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var rt struct {
		Hash        string `json:"hash"`
		From        string `json:"from"`
		To          string `json:"to"`
		Value       string `json:"value"`
		Nonce       string `json:"nonce"`
		Gas         string `json:"gas"`
		GasPrice    string `json:"gasPrice"`
		BlockNumber string `json:"blockNumber"`
		Input       string `json:"input"`
	}
	if err := json.Unmarshal(raw, &rt); err != nil {
		return nil, fmt.Errorf("decode transaction: %w", err)
	}
	tx := &Transaction{
		Hash:        rt.Hash,
		From:        rt.From,
		To:          rt.To,
		ValueWei:    rt.Value,
		GasPriceWei: rt.GasPrice,
	}
	if v, err := ParseQuantity(rt.Value); err == nil {
		tx.ValueEther = EtherString(v)
	}
	tx.Nonce, _ = QuantityUint64(rt.Nonce)
	tx.Gas, _ = QuantityUint64(rt.Gas)
	tx.BlockNumber, _ = QuantityUint64(rt.BlockNumber)
	if len(rt.Input) > maxInputDisplay {
		tx.Input = rt.Input[:maxInputDisplay] + "…"
	} else {
		tx.Input = rt.Input
	}
	return tx, nil
}

// Receipt is the decoded eth_getTransactionReceipt result.
type Receipt struct {
	Status          string `json:"status"` // "success" | "reverted"
	GasUsed         uint64 `json:"gas_used"`
	BlockNumber     uint64 `json:"block_number"`
	ContractAddress string `json:"contract_address,omitempty"`
	LogCount        int    `json:"log_count"`
	EffectiveGasWei string `json:"effective_gas_price_wei,omitempty"`
}

// DecodeReceipt decodes an eth_getTransactionReceipt result; nil for
// pending transactions (null receipt).
func DecodeReceipt(raw json.RawMessage) (*Receipt, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var rr struct {
		Status            string          `json:"status"`
		GasUsed           string          `json:"gasUsed"`
		BlockNumber       string          `json:"blockNumber"`
		ContractAddress   string          `json:"contractAddress"`
		Logs              json.RawMessage `json:"logs"`
		EffectiveGasPrice string          `json:"effectiveGasPrice"`
	}
	if err := json.Unmarshal(raw, &rr); err != nil {
		return nil, fmt.Errorf("decode receipt: %w", err)
	}
	r := &Receipt{
		ContractAddress: rr.ContractAddress,
		EffectiveGasWei: rr.EffectiveGasPrice,
	}
	switch rr.Status {
	case "0x1":
		r.Status = "success"
	case "0x0":
		r.Status = "reverted"
	default:
		r.Status = rr.Status
	}
	r.GasUsed, _ = QuantityUint64(rr.GasUsed)
	r.BlockNumber, _ = QuantityUint64(rr.BlockNumber)
	if len(rr.Logs) > 0 {
		var logs []json.RawMessage
		if json.Unmarshal(rr.Logs, &logs) == nil {
			r.LogCount = len(logs)
		}
	}
	return r, nil
}

// Log is one decoded eth_getLogs entry.
type Log struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber uint64   `json:"block_number"`
	TxHash      string   `json:"transaction_hash"`
	LogIndex    uint64   `json:"log_index"`
}

// DecodeLogs decodes an eth_getLogs result into a bounded slice; the
// second return value reports truncation at max.
func DecodeLogs(raw json.RawMessage, limit int) ([]Log, bool, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil, false, fmt.Errorf("decode logs: %w", err)
	}
	truncated := false
	if limit > 0 && len(raws) > limit {
		raws = raws[:limit]
		truncated = true
	}
	logs := make([]Log, 0, len(raws))
	for _, rl := range raws {
		var l struct {
			Address     string   `json:"address"`
			Topics      []string `json:"topics"`
			Data        string   `json:"data"`
			BlockNumber string   `json:"blockNumber"`
			TxHash      string   `json:"transactionHash"`
			LogIndex    string   `json:"logIndex"`
		}
		if err := json.Unmarshal(rl, &l); err != nil {
			continue
		}
		entry := Log{
			Address: l.Address,
			Topics:  l.Topics,
			Data:    l.Data,
			TxHash:  l.TxHash,
		}
		entry.BlockNumber, _ = QuantityUint64(l.BlockNumber)
		entry.LogIndex, _ = QuantityUint64(l.LogIndex)
		logs = append(logs, entry)
	}
	return logs, truncated, nil
}
