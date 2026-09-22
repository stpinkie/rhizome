// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"fmt"
	"math/big"
)

// Minimal RLP encoding (Ethereum wire format) — strings (byte slices) and
// lists only, which is all transaction signing needs. Items are encoded as:
//
//	single byte < 0x80            → the byte itself
//	string 0–55 bytes             → 0x80+len || string
//	string > 55 bytes             → 0xb7+lenlen || len || string
//	list payload 0–55 bytes       → 0xc0+len || payload
//	list payload > 55 bytes       → 0xf7+lenlen || len || payload

// rlpEncode encodes one item. Supported values: []byte (string),
// *big.Int / uint64 (quantity — empty string when zero), and []any (list).
func rlpEncode(v any) ([]byte, error) {
	switch t := v.(type) {
	case []byte:
		return rlpEncodeString(t), nil
	case *big.Int:
		return rlpEncodeString(rlpBigInt(t)), nil
	case uint64:
		return rlpEncodeString(rlpUint64(t)), nil
	case []any:
		var payload []byte
		for _, item := range t {
			enc, err := rlpEncode(item)
			if err != nil {
				return nil, err
			}
			payload = append(payload, enc...)
		}
		return rlpWrapList(payload), nil
	default:
		return nil, fmt.Errorf("rlp: unsupported type %T", v)
	}
}

// rlpBigInt renders a quantity as the minimal big-endian byte string; zero
// encodes as the empty string per the RLP spec.
func rlpBigInt(n *big.Int) []byte {
	if n == nil || n.Sign() == 0 {
		return nil
	}
	return n.Bytes()
}

func rlpUint64(n uint64) []byte {
	if n == 0 {
		return nil
	}
	b := make([]byte, 8)
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte(n)
		n >>= 8
	}
	return b[i:]
}

// rlpEncodeString applies the short/long string rules to raw bytes.
func rlpEncodeString(b []byte) []byte {
	if len(b) == 1 && b[0] < 0x80 {
		return b
	}
	return append(rlpLengthPrefix(len(b), 0x80), b...)
}

// rlpWrapList wraps an already-encoded payload with the list prefix.
func rlpWrapList(payload []byte) []byte {
	return append(rlpLengthPrefix(len(payload), 0xc0), payload...)
}

// rlpLengthPrefix renders the length prefix for a payload of size n at the
// given offset (0x80 for strings, 0xc0 for lists).
func rlpLengthPrefix(n int, offset byte) []byte {
	if n < 56 {
		//nolint:gosec // G115: n < 56 above.
		return []byte{offset + byte(n)}
	}
	// Long form: offset+55+lenlen || len big-endian.
	lb := rlpUint64(uint64(n))
	out := make([]byte, 0, 1+len(lb))
	//nolint:gosec // G115: len(lb) ≤ 8 for realistic payload sizes.
	out = append(out, offset+55+byte(len(lb)))
	out = append(out, lb...)
	return out
}
