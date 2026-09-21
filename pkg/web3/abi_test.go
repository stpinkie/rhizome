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
)

// Published method-selector vectors (ERC-20 ABI spec).
func TestABI_MethodSelectors(t *testing.T) {
	cases := map[string]string{
		"transfer(address,uint256)":             "a9059cbb",
		"approve(address,uint256)":              "095ea7b3",
		"balanceOf(address)":                    "70a08231",
		"decimals()":                            "313ce567",
		"symbol()":                              "95d89b41",
		"name()":                                "06fdde03",
		"allowance(address,address)":            "dd62ed3e",
		"ownerOf(uint256)":                      "6352211e",
		"tokenURI(uint256)":                     "c87b56dd",
		"sam(bytes,bool,uint256[])":             "a5643bf2",
		"transferFrom(address,address,uint256)": "23b872dd",
	}
	for sig, want := range cases {
		if got := hex.EncodeToString(MethodSelector(sig)); got != want {
			t.Fatalf("selector %s = %s, want %s", sig, got, want)
		}
	}
}

// The canonical Solidity-docs dynamic-args example:
// sam("dave", true, [1,2,3]).
func TestABI_EncodeDynamicSpecVector(t *testing.T) {
	types := []*ABIType{
		{Kind: "bytes"},
		{Kind: "bool"},
		{Kind: "array", Elem: &ABIType{Kind: "uint", Bits: 256}, Len: -1},
	}
	got, err := EncodeABIArguments(types, []any{"0x64617665", true, []any{
		big.NewInt(1), big.NewInt(2), big.NewInt(3),
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "" +
		"0000000000000000000000000000000000000000000000000000000000000060" +
		"0000000000000000000000000000000000000000000000000000000000000001" +
		"00000000000000000000000000000000000000000000000000000000000000a0" +
		"0000000000000000000000000000000000000000000000000000000000000004" +
		"6461766500000000000000000000000000000000000000000000000000000000" +
		"0000000000000000000000000000000000000000000000000000000000000003" +
		"0000000000000000000000000000000000000000000000000000000000000001" +
		"0000000000000000000000000000000000000000000000000000000000000002" +
		"0000000000000000000000000000000000000000000000000000000000000003"
	if hex.EncodeToString(got) != want {
		t.Fatalf("got %s", hex.EncodeToString(got))
	}
}

// ERC-20 transfer encoding: selector + padded address + padded amount.
func TestABI_EncodeTransfer(t *testing.T) {
	abi, err := ParseABIJSON([]byte(`[{
		"type":"function","name":"transfer","stateMutability":"nonpayable",
		"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],
		"outputs":[{"name":"","type":"bool"}]
	}]`))
	if err != nil {
		t.Fatal(err)
	}
	m, err := abi.Method("transfer", 2)
	if err != nil {
		t.Fatal(err)
	}
	data, err := m.PackArgs([]any{"0x00000000000000000000000000000000000000ff", "1000"})
	if err != nil {
		t.Fatal(err)
	}
	want := "a9059cbb" +
		"00000000000000000000000000000000000000000000000000000000000000ff" +
		"00000000000000000000000000000000000000000000000000000000000003e8"
	if hex.EncodeToString(data) != want {
		t.Fatalf("got %s, want %s", hex.EncodeToString(data), want)
	}
	if m.IsReadOnly() {
		t.Fatal("transfer must not be read-only")
	}
}

func TestABI_DecodeOutputs(t *testing.T) {
	abi, err := ParseABIJSON([]byte(`[{
		"type":"function","name":"balanceOf","stateMutability":"view",
		"inputs":[{"name":"owner","type":"address"}],
		"outputs":[{"name":"","type":"uint256"}]
	},{
		"type":"function","name":"name","stateMutability":"view",
		"inputs":[],"outputs":[{"name":"","type":"string"}]
	}]`))
	if err != nil {
		t.Fatal(err)
	}

	// balanceOf → uint256 1000
	bal, _ := abi.Method("balanceOf", 1)
	word := "00000000000000000000000000000000000000000000000000000000000003e8"
	out, err := bal.UnpackOutputs(mustHex(t, word))
	if err != nil {
		t.Fatal(err)
	}
	if out[0] != "1000" {
		t.Fatalf("balanceOf decoded %v, want 1000", out[0])
	}
	if !bal.IsReadOnly() {
		t.Fatal("balanceOf should be read-only")
	}

	// name() → dynamic string "USD Coin"
	name, _ := abi.Method("name", 0)
	enc := "0000000000000000000000000000000000000000000000000000000000000020" +
		"0000000000000000000000000000000000000000000000000000000000000008" +
		"55534420436f696e000000000000000000000000000000000000000000000000"
	out, err = name.UnpackOutputs(mustHex(t, enc))
	if err != nil {
		t.Fatal(err)
	}
	if out[0] != "USD Coin" {
		t.Fatalf("name decoded %q, want USD Coin", out[0])
	}
}

func TestABI_MethodLookupErrors(t *testing.T) {
	abi, err := ParseABIJSON([]byte(`[
		{"type":"function","name":"x","inputs":[],"outputs":[]},
		{"type":"function","name":"x","inputs":[{"name":"a","type":"uint256"}],"outputs":[]},
		{"type":"function","name":"x","inputs":[{"name":"a","type":"address"}],"outputs":[]},
		{"type":"event","name":"Transfer","inputs":[]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := abi.Method("nope", 0); err == nil {
		t.Fatal("want error for unknown method")
	}
	if _, err := abi.Method("x", 1); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("want ambiguity error listing overloads, got %v", err)
	}
	if m, err := abi.Method("x", 0); err != nil || len(m.Inputs) != 0 {
		t.Fatalf("arity disambiguation failed: %v", err)
	}
}

func TestABI_TypeParseErrors(t *testing.T) {
	for _, bad := range []string{
		"", "tuple", "uint7", "uint257", "bytes33",
		"int0", "bogus", "uint256[abc]", "uint256[0]",
	} {
		if _, err := ParseABIType(bad); err == nil {
			t.Fatalf("type %q should fail to parse", bad)
		}
	}
	for _, good := range []string{
		"address", "uint", "uint256", "int8",
		"bytes", "bytes32", "string", "bool", "uint256[]", "address[3]",
		"uint8[][2]", "function",
	} {
		if _, err := ParseABIType(good); err != nil {
			t.Fatalf("type %q should parse: %v", good, err)
		}
	}
}

func TestABI_CoercionGuards(t *testing.T) {
	addr := &ABIType{Kind: "address"}
	if _, err := coerceArg(addr, "not-an-address"); err == nil {
		t.Fatal("bad address should fail")
	}
	u8 := &ABIType{Kind: "uint", Bits: 8}
	if _, err := coerceArg(u8, "256"); err == nil {
		t.Fatal("256 overflows uint8")
	}
	i8 := &ABIType{Kind: "int", Bits: 8}
	if _, err := coerceArg(i8, "-129"); err == nil {
		t.Fatal("-129 overflows int8")
	}
	if v, err := coerceArg(i8, "-128"); err != nil || v.(*big.Int).Int64() != -128 {
		t.Fatal("int8 min should coerce")
	}
	// Negative int encodes as two's complement.
	enc, err := encodeValue(&ABIType{Kind: "int", Bits: 256}, big.NewInt(-1))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(enc) != strings.Repeat("ff", 32) {
		t.Fatalf("int(-1) encoded %s", hex.EncodeToString(enc))
	}
}

func TestABI_StaticFixedArray(t *testing.T) {
	types := []*ABIType{
		{Kind: "uint", Bits: 256},
		{Kind: "array", Elem: &ABIType{Kind: "uint", Bits: 256}, Len: 3},
	}
	got, err := EncodeABIArguments(types, []any{"7", []any{"1", "2", "3"}})
	if err != nil {
		t.Fatal(err)
	}
	// Head = 4 words (uint + 3 array elems inline), no tail.
	if len(got) != 4*32 {
		t.Fatalf("len %d, want 128", len(got))
	}
	vals, err := DecodeABIArguments(types, got)
	if err != nil {
		t.Fatal(err)
	}
	arr := vals[1].([]any)
	if vals[0] != "7" || arr[0] != "1" || arr[2] != "3" {
		t.Fatalf("decoded %v", vals)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
