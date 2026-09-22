// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testERC20ishABI = `[
	{"type":"function","name":"transfer","stateMutability":"nonpayable",
	 "inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],
	 "outputs":[{"type":"bool"}]},
	{"type":"function","name":"balanceOf","stateMutability":"view",
	 "inputs":[{"name":"account","type":"address"}],"outputs":[{"type":"uint256"}]}
]`

func TestABILabelValidation(t *testing.T) {
	for _, ok := range []string{"usdc", "a.b-c_d", "A1", strings.Repeat("x", 64)} {
		if !ValidABILabel(ok) {
			t.Fatalf("label %q should be valid", ok)
		}
	}
	for _, bad := range []string{
		"", ".dot", "-dash", "has space", "0x1234abcd" + strings.Repeat("0", 60),
		strings.Repeat("x", 65), "slash/name", "..", "../x",
	} {
		if ValidABILabel(bad) {
			t.Fatalf("label %q should be invalid", bad)
		}
	}
}

func TestABIRegistryAddGetRemove(t *testing.T) {
	reg := OpenABIRegistry(t.TempDir())
	e, err := reg.Add("token", testERC20ishABI,
		"0xA0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", []uint64{1, 11155111})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if e.Address != "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48" {
		t.Fatalf("address not checksummed: %s", e.Address)
	}
	got, err := reg.Get("token")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.ABI.Methods) != 2 || len(got.ChainIDs) != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	list, err := reg.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", len(list), err)
	}
	if dup, err := reg.Add("token", testERC20ishABI, "", nil); err == nil || dup != nil {
		t.Fatal("duplicate label should fail")
	}
	if err := reg.Remove("token"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := reg.Get("token"); err == nil {
		t.Fatal("get after remove should fail")
	}
}

func TestABIRegistryRejectsBadInput(t *testing.T) {
	reg := OpenABIRegistry(t.TempDir())
	if _, err := reg.Add("bad label!", testERC20ishABI, "", nil); err == nil {
		t.Fatal("invalid label accepted")
	}
	if _, err := reg.Add("badabi", `[{"type":"bogus"}]`, "", nil); err == nil {
		t.Fatal("malformed ABI accepted")
	}
	if _, err := reg.Add("empty", `[]`, "", nil); err == nil {
		t.Fatal("function-less ABI accepted")
	}
	if _, err := reg.Add("badaddr", testERC20ishABI, "not-an-address", nil); err == nil {
		t.Fatal("invalid address accepted")
	}
	// Oversize ABI: a valid ABI padded past 512 KiB.
	big := `[{"type":"function","name":"` + strings.Repeat("n", 600*1024) +
		`","inputs":[],"outputs":[]}]`
	if _, err := reg.Add("toobig", big, "", nil); err == nil {
		t.Fatal("oversize ABI accepted")
	}
}

func TestABIRegistryMaxEntries(t *testing.T) {
	reg := OpenABIRegistry(t.TempDir())
	dir := filepath.Join(reg.dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Pre-seed the directory to the cap rather than Add-ing 200 entries.
	for i := range abiMaxEntries {
		body := fmt.Sprintf(
			`{"label":"l%03d","abi":%s,"added_at":"2026-01-01T00:00:00Z"}`,
			i, testERC20ishABI)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("l%03d.json", i)),
			[]byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.Add("onetoomany", testERC20ishABI, "", nil); err == nil {
		t.Fatal("201st entry accepted")
	}
}

func TestABIRegistryByAddress(t *testing.T) {
	reg := OpenABIRegistry(t.TempDir())
	if _, err := reg.Add("token", testERC20ishABI,
		"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", nil); err != nil {
		t.Fatal(err)
	}
	e, err := reg.ByAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	if err != nil || e == nil || e.Label != "token" {
		t.Fatalf("by address: %v %v", e, err)
	}
	if e, err := reg.ByAddress("0x0000000000000000000000000000000000000001"); e != nil || err != nil {
		t.Fatalf("unknown address should miss: %v %v", e, err)
	}
}

func TestABIRegistryDirPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX perms don't apply on Windows")
	}
	root := t.TempDir()
	reg := OpenABIRegistry(root)
	if _, err := reg.Add("token", testERC20ishABI, "", nil); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(reg.dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("abi dir perms %o, want 0700", st.Mode().Perm())
	}
}
