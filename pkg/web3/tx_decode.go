// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// DecodedTx is a signed transaction parsed back from its raw wire bytes —
// the sender is recovered from the signature, not trusted from a declared
// field. Tests and dev chains use it to apply wallet-signed transactions.
type DecodedTx struct {
	Type     byte   // 0x00 legacy | 0x02 EIP-1559
	ChainID  uint64 // 0 on unprotected (pre-EIP-155) legacy txs
	Nonce    uint64
	To       string   // checksummed; "" = contract creation
	ValueWei *big.Int // nil-safe: never nil on return
	Data     []byte
	GasLimit uint64
	From     string // checksummed, recovered via ecrecover
}

// DecodeSignedTx parses a raw signed transaction (legacy or EIP-1559) and
// recovers the signer. It verifies the signature against the unsigned
// payload — a tx that doesn't recover cleanly is rejected, never
// best-effort applied.
func DecodeSignedTx(raw []byte) (*DecodedTx, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty transaction")
	}
	if raw[0] == 0x02 {
		return decode1559(raw)
	}
	if raw[0] >= 0xc0 {
		return decodeLegacy(raw)
	}
	return nil, fmt.Errorf("unsupported transaction type 0x%02x", raw[0])
}

// decode1559 parses a type-0x02 (EIP-1559) signed transaction:
// 0x02 || rlp([chainID, nonce, tipCap, feeCap, gas, to, value, data,
// accessList, yParity, r, s]).
func decode1559(raw []byte) (*DecodedTx, error) {
	items, rest, err := rlpDecode(raw[1:])
	if err != nil {
		return nil, fmt.Errorf("1559 payload: %w", err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("1559 payload: %d trailing bytes", len(rest))
	}
	fields, ok := items.([]any)
	if !ok || len(fields) != 12 {
		return nil, fmt.Errorf("1559 payload: expected 12 fields, got %v", fieldCount(items))
	}
	strs := make([][]byte, 12)
	for i := 0; i < 12; i++ {
		if i == 8 {
			continue // field 8 is the access list — not a string
		}
		s, ok := fields[i].([]byte)
		if !ok {
			return nil, fmt.Errorf("1559 field %d is not a string", i)
		}
		strs[i] = s
	}
	unsigned := fields[:9]
	enc, err := rlpEncode(unsigned)
	if err != nil {
		return nil, fmt.Errorf("1559 sighash encode: %w", err)
	}
	sighash := Keccak256(append([]byte{0x02}, enc...))
	yParity := new(big.Int).SetBytes(strs[9]).Uint64()
	if yParity > 1 {
		return nil, fmt.Errorf("1559 yParity %d > 1", yParity)
	}
	from, err := recoverSender(sighash, int(yParity), strs[10], strs[11])
	if err != nil {
		return nil, err
	}
	return &DecodedTx{
		Type:     0x02,
		ChainID:  new(big.Int).SetBytes(strs[0]).Uint64(),
		Nonce:    new(big.Int).SetBytes(strs[1]).Uint64(),
		To:       addrField(strs[5]),
		ValueWei: new(big.Int).SetBytes(strs[6]),
		Data:     strs[7],
		GasLimit: new(big.Int).SetBytes(strs[4]).Uint64(),
		From:     from,
	}, nil
}

// decodeLegacy parses an EIP-155 or unprotected legacy transaction:
// rlp([nonce, gasPrice, gas, to, value, data, v, r, s]).
func decodeLegacy(raw []byte) (*DecodedTx, error) {
	items, rest, err := rlpDecode(raw)
	if err != nil {
		return nil, fmt.Errorf("legacy payload: %w", err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("legacy payload: %d trailing bytes", len(rest))
	}
	fields, ok := items.([]any)
	if !ok || len(fields) != 9 {
		return nil, fmt.Errorf("legacy payload: expected 9 fields, got %v", fieldCount(items))
	}
	strs := make([][]byte, 9)
	for i := range fields {
		s, ok := fields[i].([]byte)
		if !ok {
			return nil, fmt.Errorf("legacy field %d is not a string", i)
		}
		strs[i] = s
	}
	v := new(big.Int).SetBytes(strs[6])
	var chainID uint64
	var recid int64
	var sighash []byte
	switch {
	case v.Cmp(big.NewInt(27)) == 0 || v.Cmp(big.NewInt(28)) == 0:
		// Unprotected (pre-EIP-155): v = 27 + recid, no chain id.
		recid = v.Int64() - 27
		enc, err := rlpEncode(fields[:6])
		if err != nil {
			return nil, fmt.Errorf("legacy sighash encode: %w", err)
		}
		sighash = Keccak256(enc)
	case v.Cmp(big.NewInt(35)) >= 0:
		// EIP-155: v = chainID*2 + 35 + recid.
		cid := new(big.Int).Sub(v, big.NewInt(35))
		recid = new(big.Int).Mod(cid, big.NewInt(2)).Int64()
		chainID = cid.Div(cid, big.NewInt(2)).Uint64()
		payload := append(append([]any{}, fields[:6]...), chainID, uint64(0), uint64(0))
		enc, err := rlpEncode(payload)
		if err != nil {
			return nil, fmt.Errorf("eip155 sighash encode: %w", err)
		}
		sighash = Keccak256(enc)
	default:
		return nil, fmt.Errorf("legacy v %s is not 27/28 or ≥35", v)
	}
	from, err := recoverSender(sighash, int(recid), strs[7], strs[8])
	if err != nil {
		return nil, err
	}
	return &DecodedTx{
		Type:     0x00,
		ChainID:  chainID,
		Nonce:    new(big.Int).SetBytes(strs[0]).Uint64(),
		To:       addrField(strs[3]),
		ValueWei: new(big.Int).SetBytes(strs[4]),
		Data:     strs[5],
		GasLimit: new(big.Int).SetBytes(strs[2]).Uint64(),
		From:     from,
	}, nil
}

// recoverSender applies ecrecover and derives the sender address.
func recoverSender(hash []byte, recid int, r, s []byte) (string, error) {
	if recid < 0 || recid > 1 {
		return "", fmt.Errorf("recovery id %d out of range", recid)
	}
	if len(r) > 32 || len(s) > 32 {
		return "", fmt.Errorf("signature component longer than 32 bytes")
	}
	sig := make([]byte, 65)
	sig[0] = byte(recid) + 27 // compact recovery header, uncompressed pubkey
	copy(sig[33-len(r):33], r)
	copy(sig[65-len(s):65], s)
	pub, _, err := ecdsa.RecoverCompact(sig, hash)
	if err != nil {
		return "", fmt.Errorf("sender recovery failed: %w", err)
	}
	uncomp := pub.SerializeUncompressed() // 0x04 || X || Y
	h := Keccak256(uncomp[1:])
	return ChecksumAddress(h[12:]), nil
}

// addrField renders a 20-byte RLP string as a checksummed address; empty
// means contract creation.
func addrField(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) != 20 {
		return "0x" + hex.EncodeToString(b) // malformed — surface, don't guess
	}
	return ChecksumAddress(b)
}

func fieldCount(items any) any {
	if l, ok := items.([]any); ok {
		return len(l)
	}
	return "non-list"
}

// rlpDecode decodes one RLP item at the head of b and returns the
// remaining bytes. Strings yield []byte, lists []any. Long-form lengths
// beyond 8 bytes are rejected (no counter overflows).
func rlpDecode(b []byte) (any, []byte, error) {
	if len(b) == 0 {
		return nil, nil, fmt.Errorf("rlp: empty input")
	}
	switch c := b[0]; {
	case c <= 0x7f: // single byte < 0x80 is itself
		return b[:1], b[1:], nil
	case c <= 0xb7: // short string
		n := int(c) - 0x80
		if len(b) < 1+n {
			return nil, nil, fmt.Errorf("rlp: short string truncated")
		}
		return b[1 : 1+n], b[1+n:], nil
	case c <= 0xbf: // long string
		llen := int(c) - 0xb7
		if llen > 8 || len(b) < 1+llen {
			return nil, nil, fmt.Errorf("rlp: bad long-string length")
		}
		n := int(new(big.Int).SetBytes(b[1 : 1+llen]).Int64())
		if len(b) < 1+llen+n {
			return nil, nil, fmt.Errorf("rlp: long string truncated")
		}
		return b[1+llen : 1+llen+n], b[1+llen+n:], nil
	case c <= 0xf7: // short list
		n := int(c) - 0xc0
		return rlpList(b[1:], n, b)
	default: // long list
		llen := int(c) - 0xf7
		if llen > 8 || len(b) < 1+llen {
			return nil, nil, fmt.Errorf("rlp: bad long-list length")
		}
		n := int(new(big.Int).SetBytes(b[1 : 1+llen]).Int64())
		return rlpList(b[1+llen:], n, b)
	}
}

// rlpList decodes n payload bytes as a list of items.
func rlpList(payload []byte, n int, orig []byte) (any, []byte, error) {
	if len(payload) < n {
		return nil, nil, fmt.Errorf("rlp: list payload truncated")
	}
	var out []any
	rest := payload[:n]
	for len(rest) > 0 {
		item, r, err := rlpDecode(rest)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, item)
		rest = r
	}
	return out, orig[len(orig)-len(payload)+n:], nil
}

// ParseSignedTxHex is a convenience wrapper for 0x-hex-encoded raw txs.
func ParseSignedTxHex(s string) (*DecodedTx, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return nil, fmt.Errorf("bad raw tx hex: %w", err)
	}
	return DecodeSignedTx(raw)
}
