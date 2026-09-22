// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Minimal Solidity ABI codec covering the surface ERC-20/ERC-721 need:
// address, bool, string, bytes (dynamic), bytes1..32, uint<M>/int<M>,
// and T[]/T[N] arrays thereof. Tuples are rejected explicitly — they are
// not needed for the token helpers and a partial implementation is worse
// than a clear error.

// ABIType is a parsed Solidity type.
type ABIType struct {
	Kind string   // "address"|"bool"|"string"|"bytes"|"bytesN"|"uint"|"int"|"array"
	Bits int      // uint<M>/int<M> width in bits, or bytesN byte count
	Elem *ABIType // array element type
	Len  int      // fixed array length; -1 = dynamic array
}

// ParseABIType parses a Solidity type string ("uint256", "address[]",
// "bytes32[4]", "uint8[][3]").
func ParseABIType(s string) (*ABIType, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty ABI type")
	}
	// Array suffixes bind right-to-left: "uint8[2][]" = dynamic array of
	// fixed arrays of uint8.
	if i := strings.LastIndex(s, "["); i >= 0 {
		if !strings.HasSuffix(s, "]") {
			return nil, fmt.Errorf("malformed array type %q", s)
		}
		elem, err := ParseABIType(s[:i])
		if err != nil {
			return nil, err
		}
		n := -1
		if raw := s[i+1 : len(s)-1]; raw != "" {
			v, err := parseDecimalQuantity(raw)
			if err != nil || !v.IsUint64() || v.Uint64() == 0 || v.Uint64() > 1<<20 {
				return nil, fmt.Errorf("bad fixed-array length in %q", s)
			}
			//nolint:gosec // G115: bounded ≤1<<20 above.
			n = int(v.Uint64())
		}
		return &ABIType{Kind: "array", Elem: elem, Len: n}, nil
	}
	switch {
	case s == "address":
		return &ABIType{Kind: "address"}, nil
	case s == "bool":
		return &ABIType{Kind: "bool"}, nil
	case s == "string":
		return &ABIType{Kind: "string"}, nil
	case s == "bytes":
		return &ABIType{Kind: "bytes"}, nil
	case s == "function":
		// function-typed params are ABI-encoded as bytes24.
		return &ABIType{Kind: "bytesN", Bits: 24}, nil
	case strings.HasPrefix(s, "bytes"):
		n, err := parseDecimalQuantity(s[len("bytes"):])
		if err != nil || !n.IsUint64() || n.Uint64() == 0 || n.Uint64() > 32 {
			return nil, fmt.Errorf("bad fixed-bytes type %q (want bytes1..bytes32)", s)
		}
		//nolint:gosec // G115: bounded ≤32 above.
		return &ABIType{Kind: "bytesN", Bits: int(n.Uint64())}, nil
	case s == "uint" || s == "int":
		return &ABIType{Kind: s, Bits: 256}, nil
	case strings.HasPrefix(s, "uint") || strings.HasPrefix(s, "int"):
		kind := "uint"
		rest := s[len("uint"):]
		if strings.HasPrefix(s, "int") {
			kind = "int"
			rest = s[len("int"):]
		}
		n, err := parseDecimalQuantity(rest)
		if err != nil || !n.IsUint64() || n.Uint64() == 0 ||
			n.Uint64() > 256 || n.Uint64()%8 != 0 {
			return nil, fmt.Errorf("bad integer type %q (want uint8..256/int8..256, M divisible by 8)", s)
		}
		//nolint:gosec // G115: bounded ≤256 above.
		return &ABIType{Kind: kind, Bits: int(n.Uint64())}, nil
	case s == "tuple" || strings.HasPrefix(s, "tuple"):
		return nil, fmt.Errorf("tuple types are not supported by the minimal ABI codec")
	default:
		return nil, fmt.Errorf("unsupported ABI type %q", s)
	}
}

// Canonical renders the type in canonical signature form.
func (t *ABIType) Canonical() string {
	switch t.Kind {
	case "address", "bool", "string":
		return t.Kind
	case "bytes":
		return "bytes"
	case "bytesN":
		return fmt.Sprintf("bytes%d", t.Bits)
	case "uint", "int":
		return fmt.Sprintf("%s%d", t.Kind, t.Bits)
	case "array":
		if t.Len < 0 {
			return t.Elem.Canonical() + "[]"
		}
		return fmt.Sprintf("%s[%d]", t.Elem.Canonical(), t.Len)
	default:
		return t.Kind
	}
}

// IsDynamic reports whether the type uses head/tail encoding.
func (t *ABIType) IsDynamic() bool {
	switch t.Kind {
	case "string", "bytes":
		return true
	case "array":
		return t.Len < 0 || t.Elem.IsDynamic()
	default:
		return false
	}
}

// ABIOParam is one named, typed parameter.
type ABIOParam struct {
	Name string
	Type *ABIType
}

// ABIMethod is one function entry from a contract ABI.
type ABIMethod struct {
	Name            string
	Inputs          []ABIOParam
	Outputs         []ABIOParam
	StateMutability string // "view"|"pure"|"nonpayable"|"payable"|""
}

// ABI is a parsed contract ABI (function entries only — constructor,
// event, fallback, receive, and error entries are parsed but not exposed
// as methods).
type ABI struct {
	Methods []*ABIMethod
}

type abiJSONParam struct {
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Components json.RawMessage `json:"components,omitempty"`
}

type abiJSONEntry struct {
	Type            string         `json:"type"`
	Name            string         `json:"name"`
	Inputs          []abiJSONParam `json:"inputs"`
	Outputs         []abiJSONParam `json:"outputs"`
	StateMutability string         `json:"stateMutability"`
	Constant        bool           `json:"constant"` // legacy
	Payable         bool           `json:"payable"`  // legacy
}

// ParseABIJSON parses a contract ABI JSON array. Entries with tuple types
// fail loudly — the codec cannot encode them.
func ParseABIJSON(data []byte) (*ABI, error) {
	var entries []abiJSONEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse ABI JSON: %w", err)
	}
	abi := &ABI{}
	for _, e := range entries {
		if e.Type != "function" {
			continue
		}
		m := &ABIMethod{Name: e.Name, StateMutability: e.StateMutability}
		if m.StateMutability == "" {
			switch {
			case e.Constant:
				m.StateMutability = "view"
			case e.Payable:
				m.StateMutability = "payable"
			default:
				m.StateMutability = "nonpayable"
			}
		}
		for _, p := range e.Inputs {
			if len(p.Components) > 0 {
				return nil, fmt.Errorf("method %q input %q uses tuple components — unsupported", e.Name, p.Name)
			}
			t, err := ParseABIType(p.Type)
			if err != nil {
				return nil, fmt.Errorf("method %q input %q: %w", e.Name, p.Name, err)
			}
			m.Inputs = append(m.Inputs, ABIOParam{Name: p.Name, Type: t})
		}
		for _, p := range e.Outputs {
			if len(p.Components) > 0 {
				return nil, fmt.Errorf("method %q output %q uses tuple components — unsupported", e.Name, p.Name)
			}
			t, err := ParseABIType(p.Type)
			if err != nil {
				return nil, fmt.Errorf("method %q output %q: %w", e.Name, p.Name, err)
			}
			m.Outputs = append(m.Outputs, ABIOParam{Name: p.Name, Type: t})
		}
		abi.Methods = append(abi.Methods, m)
	}
	return abi, nil
}

// Method resolves a method by name and argument count. Overloads with
// matching arity produce an ambiguity error listing the candidates.
func (a *ABI) Method(name string, arity int) (*ABIMethod, error) {
	var byName []*ABIMethod
	for _, m := range a.Methods {
		if m.Name == name {
			byName = append(byName, m)
		}
	}
	if len(byName) == 0 {
		return nil, fmt.Errorf("contract has no method %q", name)
	}
	var matches []*ABIMethod
	for _, m := range byName {
		if len(m.Inputs) == arity {
			matches = append(matches, m)
		}
	}
	switch len(matches) {
	case 0:
		arities := make([]string, 0, len(byName))
		for _, m := range byName {
			arities = append(arities, m.Signature())
		}
		return nil, fmt.Errorf("method %q takes %v args, not %d (candidates: %s)",
			name, "different arity", arity, strings.Join(arities, ", "))
	case 1:
		return matches[0], nil
	default:
		sigs := make([]string, 0, len(matches))
		for _, m := range matches {
			sigs = append(sigs, m.Signature())
		}
		return nil, fmt.Errorf("method %q is overloaded at arity %d — ambiguous: %s",
			name, arity, strings.Join(sigs, ", "))
	}
}

// Signature returns "name(type1,type2,…)" — the canonical selector input.
func (m *ABIMethod) Signature() string {
	var b strings.Builder
	b.WriteString(m.Name)
	b.WriteByte('(')
	for i, p := range m.Inputs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.Type.Canonical())
	}
	b.WriteByte(')')
	return b.String()
}

// Selector is the 4-byte method id: keccak256(signature)[:4].
func (m *ABIMethod) Selector() []byte {
	return MethodSelector(m.Signature())
}

// MethodSelector computes the 4-byte selector for a canonical signature.
func MethodSelector(sig string) []byte {
	h := Keccak256([]byte(sig))
	return h[:4]
}

// PackArgs ABI-encodes args and prepends the method selector.
func (m *ABIMethod) PackArgs(args []any) ([]byte, error) {
	if len(args) != len(m.Inputs) {
		return nil, fmt.Errorf("%s takes %d args, got %d", m.Signature(), len(m.Inputs), len(args))
	}
	types := make([]*ABIType, len(m.Inputs))
	for i, p := range m.Inputs {
		types[i] = p.Type
	}
	enc, err := EncodeABIArguments(types, args)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", m.Signature(), err)
	}
	return append(m.Selector(), enc...), nil
}

// UnpackOutputs decodes an eth_call result against the method's outputs.
// Integer values decode to decimal strings (uint256 overflows float64 and
// tool output is JSON); addresses are EIP-55 checksummed.
func (m *ABIMethod) UnpackOutputs(data []byte) ([]any, error) {
	types := make([]*ABIType, len(m.Outputs))
	for i, p := range m.Outputs {
		types[i] = p.Type
	}
	return DecodeABIArguments(types, data)
}

// IsReadOnly reports whether the method cannot mutate state.
func (m *ABIMethod) IsReadOnly() bool {
	return m.StateMutability == "view" || m.StateMutability == "pure"
}

// EncodeABIArguments applies Solidity head/tail encoding.
func EncodeABIArguments(types []*ABIType, args []any) ([]byte, error) {
	if len(types) != len(args) {
		return nil, fmt.Errorf("want %d args, got %d", len(types), len(args))
	}
	// Head slots: 32 bytes for dynamic types (offset word), staticSize for
	// static types (a fixed array of statics occupies multiple words).
	headSize := 0
	for _, t := range types {
		if t.IsDynamic() {
			headSize += 32
		} else {
			n, err := staticSize(t)
			if err != nil {
				return nil, err
			}
			headSize += n
		}
	}
	var head, tail []byte
	for i, t := range types {
		v, err := coerceArg(t, args[i])
		if err != nil {
			return nil, fmt.Errorf("arg %d (%s): %w", i, t.Canonical(), err)
		}
		if t.IsDynamic() {
			head = append(head, wordUint(uint64(headSize+len(tail)))...)
			enc, err := encodeValue(t, v)
			if err != nil {
				return nil, fmt.Errorf("arg %d (%s): %w", i, t.Canonical(), err)
			}
			tail = append(tail, enc...)
		} else {
			enc, err := encodeValue(t, v)
			if err != nil {
				return nil, fmt.Errorf("arg %d (%s): %w", i, t.Canonical(), err)
			}
			if len(enc)%32 != 0 {
				return nil, fmt.Errorf("arg %d: static encoding not word-aligned", i)
			}
			head = append(head, enc...)
		}
	}
	return append(head, tail...), nil
}

// wordUint renders a uint64 as a 32-byte big-endian word.
func wordUint(n uint64) []byte {
	w := make([]byte, 32)
	for i := 31; i >= 0 && n > 0; i-- {
		w[i] = byte(n)
		n >>= 8
	}
	return w
}

// wordBig renders a non-negative big.Int as a 32-byte big-endian word.
func wordBig(n *big.Int) []byte {
	w := make([]byte, 32)
	b := n.Bytes()
	copy(w[32-len(b):], b)
	return w
}

// wordSigned renders a possibly-negative big.Int as a two's-complement word.
func wordSigned(n *big.Int) []byte {
	if n.Sign() >= 0 {
		return wordBig(n)
	}
	// n + 2^256
	two256 := new(big.Int).Lsh(big.NewInt(1), 256)
	return wordBig(new(big.Int).Add(n, two256))
}

// padRight pads b to a multiple of 32 on the right (bytesN, string data).
func padRight(b []byte) []byte {
	if rem := len(b) % 32; rem != 0 {
		b = append(b, make([]byte, 32-rem)...)
	}
	return b
}

// coerceArg converts a JSON-ish argument (string, float64, bool, []any)
// into the Go value the encoder wants for t.
func coerceArg(t *ABIType, arg any) (any, error) {
	switch t.Kind {
	case "address":
		s, ok := arg.(string)
		if !ok || !IsAddress(s) {
			return nil, fmt.Errorf("expected a 0x address, got %v", arg)
		}
		return hex.DecodeString(s[2:])
	case "bool":
		switch v := arg.(type) {
		case bool:
			return v, nil
		case string:
			if v == "true" {
				return true, nil
			}
			if v == "false" {
				return false, nil
			}
		}
		return nil, fmt.Errorf("expected a bool, got %v", arg)
	case "uint", "int":
		n, err := coerceBigInt(arg)
		if err != nil {
			return nil, err
		}
		bits := uint(t.Bits)
		if t.Kind == "uint" {
			if n.Sign() < 0 || n.BitLen() > int(bits) {
				return nil, fmt.Errorf("%s out of range for uint%d", n, bits)
			}
		} else {
			// int<M>: -2^(M-1) .. 2^(M-1)-1
			half := new(big.Int).Lsh(big.NewInt(1), bits-1)
			if n.Cmp(new(big.Int).Neg(half)) < 0 || n.Cmp(new(big.Int).Sub(half, big.NewInt(1))) > 0 {
				return nil, fmt.Errorf("%s out of range for int%d", n, bits)
			}
		}
		return n, nil
	case "bytesN":
		b, err := coerceBytes(arg)
		if err != nil {
			return nil, err
		}
		if len(b) != t.Bits {
			return nil, fmt.Errorf("expected %d bytes, got %d", t.Bits, len(b))
		}
		return b, nil
	case "bytes":
		return coerceBytes(arg)
	case "string":
		s, ok := arg.(string)
		if !ok {
			return nil, fmt.Errorf("expected a string, got %v", arg)
		}
		return s, nil
	case "array":
		list, ok := arg.([]any)
		if !ok {
			return nil, fmt.Errorf("expected an array, got %v", arg)
		}
		if t.Len >= 0 && len(list) != t.Len {
			return nil, fmt.Errorf("fixed array needs %d elements, got %d", t.Len, len(list))
		}
		out := make([]any, len(list))
		for i, e := range list {
			cv, err := coerceArg(t.Elem, e)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			out[i] = cv
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported type %q", t.Kind)
	}
}

// coerceBigInt accepts decimal strings, 0x hex, float64, json.Number.
func coerceBigInt(arg any) (*big.Int, error) {
	switch v := arg.(type) {
	case *big.Int:
		return v, nil
	case string:
		s := strings.TrimSpace(v)
		if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
			n, ok := new(big.Int).SetString(s[2:], 16)
			if !ok {
				return nil, fmt.Errorf("invalid hex integer %q", s)
			}
			return n, nil
		}
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("invalid integer %q", s)
		}
		return n, nil
	case float64:
		if v != float64(int64(v)) {
			return nil, fmt.Errorf("non-integral number %v", v)
		}
		return big.NewInt(int64(v)), nil
	case json.Number:
		n, ok := new(big.Int).SetString(v.String(), 10)
		if !ok {
			return nil, fmt.Errorf("invalid number %q", v.String())
		}
		return n, nil
	default:
		return nil, fmt.Errorf("expected an integer, got %v (%T)", arg, arg)
	}
}

// coerceBytes accepts 0x-hex strings (decoded) or plain strings (UTF-8).
func coerceBytes(arg any) ([]byte, error) {
	s, ok := arg.(string)
	if !ok {
		return nil, fmt.Errorf("expected hex data or a string, got %v", arg)
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if !IsHexData(s) {
			return nil, fmt.Errorf("invalid hex data %q", s)
		}
		return hex.DecodeString(s[2:])
	}
	return []byte(s), nil
}

// encodeValue encodes one coerced value per the ABI spec. CoerceArg fixes
// the Go representation per kind; assertions below are checked anyway so a
// caller that skips coercion fails loudly rather than panicking.
func encodeValue(t *ABIType, v any) ([]byte, error) {
	switch t.Kind {
	case "address":
		b, ok := v.([]byte)
		if !ok {
			return nil, fmt.Errorf("address arg has type %T", v)
		}
		return wordBig(new(big.Int).SetBytes(b)), nil
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("bool arg has type %T", v)
		}
		if b {
			return wordUint(1), nil
		}
		return wordUint(0), nil
	case "uint":
		n, ok := v.(*big.Int)
		if !ok {
			return nil, fmt.Errorf("uint arg has type %T", v)
		}
		return wordBig(n), nil
	case "int":
		n, ok := v.(*big.Int)
		if !ok {
			return nil, fmt.Errorf("int arg has type %T", v)
		}
		return wordSigned(n), nil
	case "bytesN":
		b, ok := v.([]byte)
		if !ok {
			return nil, fmt.Errorf("bytes%d arg has type %T", t.Bits, v)
		}
		return padRight(b)[:32], nil
	case "bytes", "string":
		var data []byte
		if t.Kind == "string" {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("string arg has type %T", v)
			}
			data = []byte(s)
		} else {
			b, ok := v.([]byte)
			if !ok {
				return nil, fmt.Errorf("bytes arg has type %T", v)
			}
			data = b
		}
		out := wordUint(uint64(len(data)))
		return append(out, padRight(data)...), nil
	case "array":
		list, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("array arg has type %T", v)
		}
		if t.Len >= 0 && !t.Elem.IsDynamic() {
			// Static fixed array: concat element encodings.
			var out []byte
			for _, e := range list {
				enc, err := encodeValue(t.Elem, e)
				if err != nil {
					return nil, err
				}
				out = append(out, enc...)
			}
			return out, nil
		}
		// Dynamic array (or fixed array of dynamic elems): length prefix
		// (dynamic only), then head/tail over elements.
		var out []byte
		if t.Len < 0 {
			out = wordUint(uint64(len(list)))
		}
		headSize := 32 * len(list)
		var head, tail []byte
		for _, e := range list {
			if t.Elem.IsDynamic() {
				head = append(head, wordUint(uint64(headSize+len(tail)))...)
				enc, err := encodeValue(t.Elem, e)
				if err != nil {
					return nil, err
				}
				tail = append(tail, enc...)
			} else {
				enc, err := encodeValue(t.Elem, e)
				if err != nil {
					return nil, err
				}
				head = append(head, enc...)
			}
		}
		return append(out, append(head, tail...)...), nil
	default:
		return nil, fmt.Errorf("unsupported type %q", t.Kind)
	}
}

// DecodeABIArguments decodes one argument tuple. Values are returned in
// JSON-friendly form (ints as decimal strings, addresses EIP-55, bytes as
// 0x hex).
func DecodeABIArguments(types []*ABIType, data []byte) ([]any, error) {
	if len(types) == 0 {
		return nil, nil
	}
	if len(data) < 32*len(types) {
		return nil, fmt.Errorf("output too short: %d bytes for %d values", len(data), len(types))
	}
	vals := make([]any, len(types))
	headOff := 0
	for i, t := range types {
		v, err := decodeAt(t, data, headOff, data)
		if err != nil {
			return nil, fmt.Errorf("output %d (%s): %w", i, t.Canonical(), err)
		}
		vals[i] = v
		if t.IsDynamic() {
			headOff += 32
		} else {
			n, err := staticSize(t)
			if err != nil {
				return nil, err
			}
			headOff += n
		}
	}
	return vals, nil
}

// decodeAt decodes the value whose head word starts at headOff within head
// region `data`; dynamic payloads resolve via the offset into `data`.
func decodeAt(t *ABIType, data []byte, headOff int, root []byte) (any, error) {
	if t.IsDynamic() {
		if headOff+32 > len(data) {
			return nil, fmt.Errorf("missing offset word")
		}
		off := new(big.Int).SetBytes(data[headOff : headOff+32]).Uint64()
		if off > uint64(len(root)) {
			return nil, fmt.Errorf("dynamic offset %d beyond output length %d", off, len(root))
		}
		//nolint:gosec // G115: bounded by len(root) above.
		return decodeDynamic(t, root, int(off))
	}
	return decodeStatic(t, data, headOff)
}

func decodeStatic(t *ABIType, data []byte, off int) (any, error) {
	if off+32 > len(data) {
		return nil, fmt.Errorf("short static word")
	}
	w := data[off : off+32]
	switch t.Kind {
	case "address":
		return ChecksumAddress(w[12:]), nil
	case "bool":
		return new(big.Int).SetBytes(w).Sign() != 0, nil
	case "uint":
		return new(big.Int).SetBytes(w).String(), nil
	case "int":
		n := new(big.Int).SetBytes(w)
		if w[0]&0x80 != 0 { // negative two's complement
			two256 := new(big.Int).Lsh(big.NewInt(1), 256)
			n.Sub(n, two256)
		}
		return n.String(), nil
	case "bytesN":
		return "0x" + hex.EncodeToString(w[:t.Bits]), nil
	case "array":
		// Static fixed array.
		out := make([]any, t.Len)
		stride, err := staticSize(t.Elem)
		if err != nil {
			return nil, err
		}
		for i := range out {
			v, err := decodeStatic(t.Elem, data, off+i*stride)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported static type %q", t.Kind)
	}
}

// staticSize is the byte width of a static type's encoding.
func staticSize(t *ABIType) (int, error) {
	if t.IsDynamic() {
		return 0, fmt.Errorf("dynamic type %s has no static size", t.Canonical())
	}
	if t.Kind == "array" {
		n, err := staticSize(t.Elem)
		return n * t.Len, err
	}
	return 32, nil
}

// decodeDynamic decodes a dynamic value at absolute offset off in root.
func decodeDynamic(t *ABIType, root []byte, off int) (any, error) {
	switch t.Kind {
	case "string", "bytes":
		if off+32 > len(root) {
			return nil, fmt.Errorf("short length word")
		}
		nBig := new(big.Int).SetBytes(root[off : off+32])
		if !nBig.IsUint64() || nBig.Uint64() > uint64(len(root)) {
			return nil, fmt.Errorf("bad dynamic length %s", nBig)
		}
		//nolint:gosec // G115: bounded by len(root) above.
		n := int(nBig.Uint64())
		start := off + 32
		if start+n > len(root) {
			return nil, fmt.Errorf("data overruns output")
		}
		data := root[start : start+n]
		if t.Kind == "string" {
			return string(data), nil
		}
		return "0x" + hex.EncodeToString(data), nil
	case "array":
		idx := off
		count := t.Len
		if count < 0 {
			if off+32 > len(root) {
				return nil, fmt.Errorf("short array length word")
			}
			cBig := new(big.Int).SetBytes(root[off : off+32])
			if !cBig.IsUint64() || cBig.Uint64() > uint64(len(root)/32) {
				return nil, fmt.Errorf("bad array length %s", cBig)
			}
			//nolint:gosec // G115: bounded by len(root)/32 above.
			count = int(cBig.Uint64())
			idx = off + 32
		}
		out := make([]any, count)
		if t.Elem.IsDynamic() {
			for i := range out {
				if idx+32 > len(root) {
					return nil, fmt.Errorf("short element offset")
				}
				eBig := new(big.Int).SetBytes(root[idx : idx+32])
				if !eBig.IsUint64() || eBig.Uint64() > uint64(len(root)) {
					return nil, fmt.Errorf("bad element offset %s", eBig)
				}
				//nolint:gosec // G115: bounded by len(root) above.
				v, err := decodeDynamic(t.Elem, root, idx+int(eBig.Uint64()))
				if err != nil {
					return nil, err
				}
				out[i] = v
				idx += 32
			}
		} else {
			stride, err := staticSize(t.Elem)
			if err != nil {
				return nil, err
			}
			for i := range out {
				v, err := decodeStatic(t.Elem, root, idx+i*stride)
				if err != nil {
					return nil, err
				}
				out[i] = v
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported dynamic type %q", t.Kind)
	}
}
