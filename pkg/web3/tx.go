// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// TxRequest describes an unsigned transaction. Fee model: set GasFeeCap +
// GasTipCap for EIP-1559 (type 0x02), or GasPrice for legacy (type 0x00).
// FillTxDefaults populates everything the caller did not set.
type TxRequest struct {
	ChainID   uint64
	Nonce     uint64
	To        string   // 0x address; "" = contract creation
	ValueWei  *big.Int // nil → 0
	Data      []byte   // nil → empty
	GasLimit  uint64
	GasPrice  *big.Int // legacy gas price (wei)
	GasTipCap *big.Int // EIP-1559 maxPriorityFeePerGas
	GasFeeCap *big.Int // EIP-1559 maxFeePerGas
}

// Is1559 reports whether the request uses EIP-1559 fee fields.
func (t *TxRequest) Is1559() bool {
	return t.GasFeeCap != nil || t.GasTipCap != nil
}

// gasEstimateMargin pads eth_estimateGas results — estimates can
// undershoot for calls whose state changes between estimate and mining.
const gasEstimateMarginPct = 125 // +25%

// ChainID queries eth_chainId once.
func ChainID(ctx context.Context, p *Provider) (uint64, error) {
	raw, err := p.Call(ctx, "eth_chainId", nil)
	if err != nil {
		return 0, err
	}
	var hexID string
	if err := json.Unmarshal(raw, &hexID); err != nil {
		return 0, fmt.Errorf("decode eth_chainId: %w", err)
	}
	return QuantityUint64(hexID)
}

// PendingNonce returns eth_getTransactionCount(addr, "pending").
func PendingNonce(ctx context.Context, p *Provider, addr string) (uint64, error) {
	raw, err := p.Call(ctx, "eth_getTransactionCount", []any{addr, "pending"})
	if err != nil {
		return 0, err
	}
	var hexN string
	if err := json.Unmarshal(raw, &hexN); err != nil {
		return 0, fmt.Errorf("decode eth_getTransactionCount: %w", err)
	}
	return QuantityUint64(hexN)
}

// EstimateGas runs eth_estimateGas for the request and applies the safety
// margin.
func EstimateGas(ctx context.Context, p *Provider, from string, req *TxRequest) (uint64, error) {
	call := map[string]string{"from": from}
	if req.To != "" {
		call["to"] = req.To
	}
	if req.ValueWei != nil && req.ValueWei.Sign() > 0 {
		call["value"] = QuantityString(req.ValueWei)
	}
	if len(req.Data) > 0 {
		call["data"] = "0x" + hex.EncodeToString(req.Data)
	}
	raw, err := p.Call(ctx, "eth_estimateGas", []any{call})
	if err != nil {
		return 0, err
	}
	var hexG string
	if err := json.Unmarshal(raw, &hexG); err != nil {
		return 0, fmt.Errorf("decode eth_estimateGas: %w", err)
	}
	g, err := QuantityUint64(hexG)
	if err != nil {
		return 0, err
	}
	return g * gasEstimateMarginPct / 100, nil
}

// FillTxDefaults populates ChainID, Nonce, GasLimit, and the fee fields on
// req. Fee strategy: prefer EIP-1559 when the endpoint reports a base fee
// (post-London chain); fall back to legacy gasPrice otherwise.
func FillTxDefaults(ctx context.Context, p *Provider, from string, req *TxRequest) error {
	if req.ChainID == 0 {
		id, err := ChainID(ctx, p)
		if err != nil {
			return fmt.Errorf("chain id: %w", err)
		}
		req.ChainID = id
	}
	if req.Nonce == 0 {
		// Nonce 0 is a legitimate value for a fresh account, so zero is
		// not a reliable "unset" sentinel — callers who need an explicit
		// nonce set it and also set GasLimit to mark the request filled.
		n, err := PendingNonce(ctx, p, from)
		if err != nil {
			return fmt.Errorf("nonce: %w", err)
		}
		req.Nonce = n
	}
	if req.GasLimit == 0 {
		g, err := EstimateGas(ctx, p, from, req)
		if err != nil {
			return fmt.Errorf("estimate gas: %w", err)
		}
		req.GasLimit = g
	}

	if req.GasFeeCap == nil && req.GasPrice == nil {
		base, baseErr := latestBaseFee(ctx, p)
		tip, tipErr := maxPriorityFee(ctx, p)
		if baseErr == nil && tipErr == nil {
			if req.GasTipCap == nil {
				req.GasTipCap = tip
			}
			// feeCap = baseFee*2 + tip — headroom for the next block.
			req.GasFeeCap = new(big.Int).Add(new(big.Int).Mul(base, big.NewInt(2)), req.GasTipCap)
		} else {
			// Legacy chain (or a light client without fee endpoints).
			raw, err := p.Call(ctx, "eth_gasPrice", nil)
			if err != nil {
				return fmt.Errorf("gas price: %w", err)
			}
			var hexP string
			if err := json.Unmarshal(raw, &hexP); err != nil {
				return fmt.Errorf("decode eth_gasPrice: %w", err)
			}
			price, err := ParseQuantity(hexP)
			if err != nil {
				return fmt.Errorf("gas price: %w", err)
			}
			req.GasPrice = price
		}
	}
	return nil
}

// latestBaseFee returns baseFeePerGas of the latest block, or an error on
// pre-London chains and light clients that omit the field.
func latestBaseFee(ctx context.Context, p *Provider) (*big.Int, error) {
	raw, err := p.Call(ctx, "eth_getBlockByNumber", []any{"latest", false})
	if err != nil {
		return nil, err
	}
	b, _, err := DecodeBlock(raw, false, 0)
	if err != nil {
		return nil, err
	}
	if b == nil || b.BaseFeeWei == "" {
		return nil, fmt.Errorf("endpoint did not report baseFeePerGas")
	}
	return ParseQuantity(b.BaseFeeWei)
}

func maxPriorityFee(ctx context.Context, p *Provider) (*big.Int, error) {
	raw, err := p.Call(ctx, "eth_maxPriorityFeePerGas", nil)
	if err != nil {
		return nil, err
	}
	var hexP string
	if err := json.Unmarshal(raw, &hexP); err != nil {
		return nil, fmt.Errorf("decode eth_maxPriorityFeePerGas: %w", err)
	}
	return ParseQuantity(hexP)
}

// txToBytes renders the To field: 20 raw bytes, or empty for creation.
func txToBytes(to string) ([]byte, error) {
	if to == "" {
		return nil, nil
	}
	if !IsAddress(to) {
		return nil, fmt.Errorf("invalid to address %q", to)
	}
	return hex.DecodeString(to[2:])
}

// signHash signs a 32-byte digest and returns (recid 0–3, r, s).
// decred's signer produces low-S canonical signatures (EIP-2 safe).
func signHash(key *secp256k1.PrivateKey, hash []byte) (int, *big.Int, *big.Int) {
	sig := ecdsa.SignCompact(key, hash, false) // sig[0] = 27 + recid
	recid := int(sig[0]) - 27
	r := new(big.Int).SetBytes(sig[1:33])
	s := new(big.Int).SetBytes(sig[33:65])
	return recid, r, s
}

// SignTx signs req and returns the RLP-encoded raw transaction plus its
// hash (keccak of the raw bytes — what eth_sendRawTransaction returns).
func SignTx(req *TxRequest, key *secp256k1.PrivateKey) (raw []byte, txHash string, err error) {
	to, err := txToBytes(req.To)
	if err != nil {
		return nil, "", err
	}
	value := req.ValueWei
	if value == nil {
		value = new(big.Int)
	}
	data := req.Data
	if data == nil {
		data = []byte{}
	}

	if req.Is1559() {
		if req.GasFeeCap == nil || req.GasTipCap == nil {
			return nil, "", fmt.Errorf("EIP-1559 tx needs both gas_fee_cap and gas_tip_cap")
		}
		payload := []any{
			req.ChainID, req.Nonce, req.GasTipCap, req.GasFeeCap,
			req.GasLimit, to, value, data,
			[]any{}, // empty access list
		}
		enc, err := rlpEncode(payload)
		if err != nil {
			return nil, "", err
		}
		sighash := Keccak256(append([]byte{0x02}, enc...))
		recid, r, s := signHash(key, sighash)
		signed, err := rlpEncode(append(payload, big.NewInt(int64(recid)), r, s))
		if err != nil {
			return nil, "", err
		}
		raw = append([]byte{0x02}, signed...)
	} else {
		if req.GasPrice == nil {
			return nil, "", fmt.Errorf("legacy tx needs gas_price")
		}
		payload := []any{req.Nonce, req.GasPrice, req.GasLimit, to, value, data}
		sigFields := append(append([]any{}, payload...), req.ChainID, uint64(0), uint64(0))
		enc, err := rlpEncode(sigFields)
		if err != nil {
			return nil, "", err
		}
		recid, r, s := signHash(key, Keccak256(enc))
		// EIP-155: v = chainID*2 + 35 + recid
		v := new(big.Int).Add(
			new(big.Int).Mul(new(big.Int).SetUint64(req.ChainID), big.NewInt(2)),
			big.NewInt(35+int64(recid)))
		raw, err = rlpEncode(append(payload, v, r, s))
		if err != nil {
			return nil, "", err
		}
	}
	return raw, "0x" + hex.EncodeToString(Keccak256(raw)), nil
}

// SendRawTransaction broadcasts signed bytes via eth_sendRawTransaction
// and returns the tx hash.
func SendRawTransaction(ctx context.Context, p *Provider, raw []byte) (string, error) {
	res, err := p.Call(ctx, "eth_sendRawTransaction",
		[]any{"0x" + hex.EncodeToString(raw)})
	if err != nil {
		return "", err
	}
	var h string
	if err := json.Unmarshal(res, &h); err != nil {
		return "", fmt.Errorf("decode eth_sendRawTransaction: %w", err)
	}
	return h, nil
}

// SignMessage produces an EIP-191 personal_sign signature
// (r || s || v with v = 27+recid) over an arbitrary message.
func SignMessage(key *secp256k1.PrivateKey, msg []byte) ([]byte, error) {
	prefix := "\x19Ethereum Signed Message:\n" + strconv.Itoa(len(msg))
	hash := Keccak256(append([]byte(prefix), msg...))
	sig := ecdsa.SignCompact(key, hash, false) // sig[0] = 27 + recid
	// Reorder compact [recid||r||s] → [r||s||v].
	out := make([]byte, 65)
	copy(out[:32], sig[1:33])
	copy(out[32:64], sig[33:65])
	out[64] = sig[0]
	return out, nil
}

// ParseHexBytes decodes a 0x-prefixed even-hex string (call data).
func ParseHexBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0x" {
		return nil, nil
	}
	if !IsHexData(s) {
		return nil, fmt.Errorf("invalid hex data %q", s)
	}
	return hex.DecodeString(s[2:])
}
