// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// roundTrip: SignTx → DecodeSignedTx must recover the signer, chain, and
// payload verbatim — the guarantee FakeChain's eth_sendRawTransaction
// relies on.
func TestDecodeSignedTx_LegacyRoundTrip(t *testing.T) {
	key := secp256k1.PrivKeyFromBytes([]byte("test-key-32-bytes-padded-exactly!"))
	from := DeriveAddress(key)
	req := &TxRequest{
		ChainID: 11155111, Nonce: 7, To: "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
		ValueWei: big.NewInt(42), Data: []byte{0xde, 0xad, 0xbe, 0xef},
		GasLimit: 21000, GasPrice: big.NewInt(3_000_000_000),
	}
	raw, _, err := SignTx(req, key)
	if err != nil {
		t.Fatalf("SignTx: %v", err)
	}
	dt, err := DecodeSignedTx(raw)
	if err != nil {
		t.Fatalf("DecodeSignedTx: %v", err)
	}
	if dt.Type != 0x00 || dt.ChainID != 11155111 || dt.Nonce != 7 {
		t.Fatalf("header mismatch: %+v", dt)
	}
	if dt.From != from {
		t.Fatalf("recovered %s, want %s", dt.From, from)
	}
	if !strings.EqualFold(dt.To, req.To) || dt.ValueWei.Cmp(big.NewInt(42)) != 0 ||
		hex.EncodeToString(dt.Data) != "deadbeef" || dt.GasLimit != 21000 {
		t.Fatalf("payload mismatch: %+v", dt)
	}
}

func TestDecodeSignedTx_1559RoundTrip(t *testing.T) {
	key := secp256k1.PrivKeyFromBytes([]byte("another-key-32-bytes-padded-ok!?"))
	from := DeriveAddress(key)
	req := &TxRequest{
		ChainID: 137, Nonce: 0, To: "0x000000000000000000000000000000000000dEaD",
		ValueWei: new(big.Int), Data: nil,
		GasLimit: 100000, GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(50_000_000_000),
	}
	raw, _, err := SignTx(req, key)
	if err != nil {
		t.Fatalf("SignTx: %v", err)
	}
	if raw[0] != 0x02 {
		t.Fatalf("expected typed tx, got 0x%02x", raw[0])
	}
	dt, err := DecodeSignedTx(raw)
	if err != nil {
		t.Fatalf("DecodeSignedTx: %v", err)
	}
	if dt.Type != 0x02 || dt.ChainID != 137 || dt.From != from {
		t.Fatalf("mismatch: %+v want from %s", dt, from)
	}
}

func TestDecodeSignedTx_RejectsGarbage(t *testing.T) {
	for _, raw := range [][]byte{
		nil,
		{0x01, 0xaa}, // unknown type byte
		{0x02, 0xc0}, // 1559 empty list
		{0xf8, 0x00}, // legacy empty list
		append([]byte{0x02, 0xf8, 0x0a}, // 1559 list w/ 10 fields
			make([]byte, 10)...),
	} {
		if _, err := DecodeSignedTx(raw); err == nil {
			t.Fatalf("raw %x should fail", raw)
		}
	}
}
