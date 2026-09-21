// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"encoding/json"
	"math/big"
	"testing"
)

func TestParseQuantity(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int64
		fail bool
	}{
		{"0x0", 0, false},
		{"0x1", 1, false},
		{"0x18d077c", 26019708, false},
		{"0XFF", 255, false}, // uppercase prefix accepted, digits case-insensitive
		{"0x", 0, true},
		{"0x01", 0, true},   // leading zero rejected per spec
		{"42", 0, true},     // missing prefix
		{"0xgg", 0, true},   // non-hex
		{"0x-1", 0, true},   // sign rejected — quantities are unsigned
		{"0x+1", 0, true},   // SetString would otherwise accept the sign
		{"0x0x10", 0, true}, // second 0x prefix rejected
		{"", 0, true},
	} {
		n, err := ParseQuantity(tt.in)
		if tt.fail {
			if err == nil {
				t.Fatalf("ParseQuantity(%q) should fail", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseQuantity(%q): %v", tt.in, err)
		}
		if n.Int64() != tt.want {
			t.Fatalf("ParseQuantity(%q) = %d, want %d", tt.in, n.Int64(), tt.want)
		}
	}
}

func TestQuantityString(t *testing.T) {
	if got := QuantityString(big.NewInt(0)); got != "0x0" {
		t.Fatalf("QuantityString(0) = %q", got)
	}
	if got := QuantityString(big.NewInt(255)); got != "0xff" {
		t.Fatalf("QuantityString(255) = %q", got)
	}
	if got := QuantityString(nil); got != "0x0" {
		t.Fatalf("QuantityString(nil) = %q", got)
	}
}

func TestNormalizeBlockTag(t *testing.T) {
	for _, tt := range []struct {
		in   any
		want string
		fail bool
	}{
		{nil, "latest", false},
		{"latest", "latest", false},
		{"FINALIZED", "finalized", false},
		{"", "latest", false},
		{"0x10", "0x10", false},
		{"12345", "0x3039", false}, // bare decimal → hex
		{float64(42), "0x2a", false},
		{"bogus", "", true},
		{"0xzz", "", true},
		{"-1", "", true},              // negative decimal rejected
		{json.Number("-1"), "", true}, // negative json.Number rejected
		{json.Number("42"), "0x2a", false},
		{-1.5, "", true},
	} {
		got, err := NormalizeBlockTag(tt.in)
		if tt.fail {
			if err == nil {
				t.Fatalf("NormalizeBlockTag(%v) should fail", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("NormalizeBlockTag(%v): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("NormalizeBlockTag(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeQuantity(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
		fail bool
	}{
		{"10", "0xa", false},
		{" 255 ", "0xff", false}, // trimmed
		{"0x10", "0x10", false},
		{"0XFF", "0xff", false}, // canonical lowercase form
		{"0x0", "0x0", false},
		{"-1", "", true},
		{"+1", "", true},
		{"0x-1", "", true},
		{"0x0x10", "", true},
		{"0x01", "", true}, // leading zeros still rejected
		{"abc", "", true},
		{"", "", true},
		{"0x", "", true},
	} {
		got, err := NormalizeQuantity(tt.in)
		if tt.fail {
			if err == nil {
				t.Fatalf("NormalizeQuantity(%q) should fail", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("NormalizeQuantity(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("NormalizeQuantity(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestValidators(t *testing.T) {
	addr := "0xde0B295669a9FD93d5F28D9Ec85E40f4cb697BAe"
	if !IsAddress(addr) {
		t.Fatalf("IsAddress(%q) = false", addr)
	}
	for _, bad := range []string{"", "0x", addr + "ff", "0xZZ56789a9FD93d5F28D9Ec85E40f4cb697BAe"} {
		if IsAddress(bad) {
			t.Fatalf("IsAddress(%q) = true", bad)
		}
	}
	hash := "0x881de7d386f6e90111b157d513b81b496d5297aaa6e70771b2c44d4fbebfa61a"
	if !IsHash32(hash) {
		t.Fatalf("IsHash32(%q) = false", hash)
	}
	if IsHash32(addr) {
		t.Fatal("IsHash32 accepted an address")
	}
	if !IsHexData("0x") || !IsHexData("0xa9059cbb") {
		t.Fatal("IsHexData rejected valid hex")
	}
	if IsHexData("0x0") || IsHexData("abc") {
		t.Fatal("IsHexData accepted invalid hex")
	}
}

func TestEtherString(t *testing.T) {
	one := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	if got := EtherString(one); got != "1" {
		t.Fatalf("EtherString(1e18) = %q", got)
	}
	half := new(big.Int).Div(one, big.NewInt(2))
	if got := EtherString(half); got != "0.5" {
		t.Fatalf("EtherString(0.5e18) = %q", got)
	}
	if got := EtherString(big.NewInt(0)); got != "0" {
		t.Fatalf("EtherString(0) = %q", got)
	}
	// 1.2345 ether
	v := new(big.Int).Mul(big.NewInt(12345), new(big.Int).Exp(big.NewInt(10), big.NewInt(14), nil))
	if got := EtherString(v); got != "1.2345" {
		t.Fatalf("EtherString(1.2345e18) = %q", got)
	}
}

func TestDecodeBlock(t *testing.T) {
	raw := json.RawMessage(`{
		"number": "0x10", "hash": "0xabc", "parentHash": "0xdef",
		"timestamp": "0x5f5e100", "miner": "0x0000000000000000000000000000000000000001",
		"gasUsed": "0x5208", "gasLimit": "0x1c9c380", "baseFeePerGas": "0x3b9aca00",
		"transactions": ["0x111", "0x222"]
	}`)
	b, trunc, err := DecodeBlock(raw, false, 50)
	if err != nil {
		t.Fatalf("DecodeBlock: %v", err)
	}
	if trunc || b == nil {
		t.Fatal("unexpected truncation/nil")
	}
	if b.Number != 16 || b.TransactionCount != 2 || len(b.Transactions) != 2 {
		t.Fatalf("block = %+v", b)
	}
	if b.BaseFeeWei != "0x3b9aca00" {
		t.Fatalf("baseFee = %q", b.BaseFeeWei)
	}

	// Full transactions, capped.
	rawFull := json.RawMessage(`{
		"number": "0x10", "hash": "0xabc", "parentHash": "0xdef",
		"timestamp": "0x5f5e100", "gasUsed": "0x5208", "gasLimit": "0x1c9c380",
		"transactions": [
			{"hash":"0x1","from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0xde0b6b3a7640000","nonce":"0x0","gas":"0x5208"},
			{"hash":"0x2","from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0x0","nonce":"0x1","gas":"0x5208"}
		]
	}`)
	b2, trunc, err := DecodeBlock(rawFull, true, 1)
	if err != nil {
		t.Fatalf("DecodeBlock full: %v", err)
	}
	if !trunc || len(b2.FullTransactions) != 1 {
		t.Fatalf("full decode: trunc=%v n=%d", trunc, len(b2.FullTransactions))
	}
	if b2.FullTransactions[0].ValueEther != "1" {
		t.Fatalf("value ether = %q", b2.FullTransactions[0].ValueEther)
	}
}

func TestDecodeReceipt(t *testing.T) {
	r, err := DecodeReceipt(json.RawMessage(`{
		"status":"0x1","gasUsed":"0x5208","blockNumber":"0x10",
		"effectiveGasPrice":"0x3b9aca00","logs":[{},{}]
	}`))
	if err != nil || r == nil {
		t.Fatalf("DecodeReceipt: %v", err)
	}
	if r.Status != "success" || r.GasUsed != 21000 || r.LogCount != 2 {
		t.Fatalf("receipt = %+v", r)
	}
	// Pending tx → null receipt.
	r2, err := DecodeReceipt(json.RawMessage(`null`))
	if err != nil || r2 != nil {
		t.Fatalf("null receipt: %+v %v", r2, err)
	}
	r3, err := DecodeReceipt(json.RawMessage(`{"status":"0x0","gasUsed":"0x1","blockNumber":"0x2","logs":[]}`))
	if err != nil || r3.Status != "reverted" {
		t.Fatalf("reverted receipt = %+v", r3)
	}
}

func TestDecodeLogs(t *testing.T) {
	raw := json.RawMessage(`[
		{"address":"0x0000000000000000000000000000000000000001","topics":["0xaa"],"data":"0x",
		 "blockNumber":"0x10","transactionHash":"0xbb","logIndex":"0x0"},
		{"address":"0x0000000000000000000000000000000000000002","topics":[],"data":"0x1234",
		 "blockNumber":"0x11","transactionHash":"0xcc","logIndex":"0x3"}
	]`)
	logs, trunc, err := DecodeLogs(raw, 100)
	if err != nil || trunc {
		t.Fatalf("DecodeLogs: %v trunc=%v", err, trunc)
	}
	if len(logs) != 2 || logs[0].BlockNumber != 16 || logs[1].LogIndex != 3 {
		t.Fatalf("logs = %+v", logs)
	}
	_, trunc, err = DecodeLogs(raw, 1)
	if err != nil || !trunc {
		t.Fatalf("expected truncation at max=1")
	}
}
