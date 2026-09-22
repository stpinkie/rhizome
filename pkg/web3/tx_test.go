// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

func testKey(t *testing.T) *secp256k1.PrivateKey {
	t.Helper()
	b, err := hex.DecodeString(
		"4646464646464646464646464646464646464646464646464646464646464646")
	if err != nil {
		t.Fatal(err)
	}
	return secp256k1.PrivKeyFromBytes(b)
}

// The canonical EIP-155 spec vector: privkey 0x4646…, nonce 9,
// 20 gwei, 21000 gas, 1 ETH to 0x3535…35, chainID 1.
func TestSignTx_LegacyEIP155Vector(t *testing.T) {
	key := testKey(t)
	defer key.Zero()
	req := &TxRequest{
		ChainID:  1,
		Nonce:    9,
		To:       "0x3535353535353535353535353535353535353535",
		ValueWei: new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil),
		GasLimit: 21000,
		GasPrice: big.NewInt(20_000_000_000),
	}
	raw, hash, err := SignTx(req, key)
	if err != nil {
		t.Fatal(err)
	}
	want := "f86c098504a817c8008252089435353535353535353535353535353535353535358" +
		"80de0b6b3a76400008025a028ef61340bd939bc2195fe537567866003e1a15d3" +
		"c71ff63e1590620aa636276a067cbe9d8997f761aecb703304b3800ccf555c9f" +
		"3dc64214b297fb1966a3b6d83"
	if hex.EncodeToString(raw) != want {
		t.Fatalf("raw tx %s\nwant %s", hex.EncodeToString(raw), want)
	}
	if !IsHash32(hash) {
		t.Fatalf("tx hash %q malformed", hash)
	}
}

func TestSignTx_EIP1559Structure(t *testing.T) {
	key := testKey(t)
	defer key.Zero()
	req := &TxRequest{
		ChainID:   11155111,
		Nonce:     4,
		To:        "0x00000000000000000000000000000000000000ff",
		ValueWei:  big.NewInt(42),
		Data:      mustHex(t, "a9059cbb"),
		GasLimit:  50000,
		GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(20_000_000_000),
	}
	raw, hash, err := SignTx(req, key)
	if err != nil {
		t.Fatal(err)
	}
	if raw[0] != 0x02 {
		t.Fatalf("1559 tx must be typed 0x02, got 0x%02x", raw[0])
	}
	if !IsHash32(hash) {
		t.Fatalf("bad hash %q", hash)
	}
	// Determinism: RFC6979 makes same-key same-tx sign identically.
	raw2, _, err := SignTx(req, key)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(raw) != hex.EncodeToString(raw2) {
		t.Fatal("signing is not deterministic")
	}
}

// Sign a payload and verify ecrecover returns the signing pubkey —
// proves the compact-sig recid handling matches Ethereum semantics.
func TestSignHash_Recovery(t *testing.T) {
	key := testKey(t)
	defer key.Zero()
	hash := Keccak256([]byte("rhizome"))
	sig := ecdsa.SignCompact(key, hash, false)
	pub, compressed, err := ecdsa.RecoverCompact(sig, hash)
	if err != nil {
		t.Fatal(err)
	}
	if compressed {
		t.Fatal("expected uncompressed reference")
	}
	if !pub.IsEqual(key.PubKey()) {
		t.Fatal("recovered pubkey does not match signer")
	}
	// Low-S enforcement: s must be ≤ n/2 (EIP-2). N is the secp256k1
	// group order.
	n, _ := new(big.Int).SetString(
		"FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
	s := new(big.Int).SetBytes(sig[33:65])
	if s.Cmp(new(big.Int).Rsh(n, 1)) > 0 {
		t.Fatal("signature s is not low-S normalized")
	}
}

func TestSignMessage_EIP191(t *testing.T) {
	key := testKey(t)
	defer key.Zero()
	sig, err := SignMessage(key, []byte("Hello World"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 65 {
		t.Fatalf("sig len %d, want 65", len(sig))
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatalf("v = %d, want 27/28", sig[64])
	}
	// Recover and compare to signer.
	want := Keccak256(append(
		[]byte("\x19Ethereum Signed Message:\n11"), []byte("Hello World")...))
	compact := make([]byte, 65)
	compact[0] = sig[64]
	copy(compact[1:33], sig[:32])
	copy(compact[33:65], sig[32:64])
	pub, _, err := ecdsa.RecoverCompact(compact, want)
	if err != nil {
		t.Fatal(err)
	}
	if !pub.IsEqual(key.PubKey()) {
		t.Fatal("EIP-191 recovery mismatch")
	}
}

func TestParseHexBytes(t *testing.T) {
	if b, err := ParseHexBytes("0xa9059cbb"); err != nil || len(b) != 4 {
		t.Fatal("hex parse failed")
	}
	if _, err := ParseHexBytes("0x123"); err == nil {
		t.Fatal("odd-length hex must fail")
	}
	if _, err := ParseHexBytes("zz"); err == nil {
		t.Fatal("non-hex must fail")
	}
}
