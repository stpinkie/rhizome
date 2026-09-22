// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"encoding/hex"
	"math/big"
	"testing"
)

// Vectors from the Ethereum RLP spec (ethereum.org developers docs).
func TestRLP_SpecVectors(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"empty string", []byte{}, "80"},
		{"single byte <0x80", []byte{0x00}, "00"},
		{"single byte 0x7f", []byte{0x7f}, "7f"},
		{"dog", []byte("dog"), "83646f67"},
		{"empty list", []any{}, "c0"},
		{"uint 15", uint64(15), "0f"},
		{"uint 1024", uint64(1024), "820400"},
		{"big zero → empty", new(big.Int), "80"},
		{"big 1024", big.NewInt(1024), "820400"},
		{
			"56-byte string",
			[]byte("Lorem ipsum dolor sit amet, consectetur adipisicing elit"),
			"b8384c6f72656d20697073756d20646f6c6f722073697420616d65742c20636f6e7365637465747572206164697069736963696e6720656c6974",
		},
		{
			"nested lists [[],[[]],[[],[[]]]]",
			[]any{[]any{}, []any{[]any{}}, []any{[]any{}, []any{[]any{}}}},
			"c7c0c1c0c3c0c1c0",
		},
		{
			"list of strings",
			[]any{[]byte("cat"), []byte("dog")},
			"c88363617483646f67",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rlpEncode(tc.in)
			if err != nil {
				t.Fatalf("rlpEncode: %v", err)
			}
			if hex.EncodeToString(got) != tc.want {
				t.Fatalf("got %s, want %s", hex.EncodeToString(got), tc.want)
			}
		})
	}
}

func TestRLP_1024ByteString(t *testing.T) {
	in := make([]byte, 1024)
	for i := range in {
		in[i] = byte(i)
	}
	got, err := rlpEncode(in)
	if err != nil {
		t.Fatal(err)
	}
	// 0xb7 + lenlen(2) → 0xb9, then 0x0400
	if got[0] != 0xb9 || got[1] != 0x04 || got[2] != 0x00 {
		t.Fatalf("bad long-string prefix %x", got[:3])
	}
	if len(got) != 1027 {
		t.Fatalf("len %d, want 1027", len(got))
	}
}

func TestRLP_UnsupportedType(t *testing.T) {
	if _, err := rlpEncode("string-not-supported"); err == nil {
		t.Fatal("want error for Go string (callers pass []byte)")
	}
}
