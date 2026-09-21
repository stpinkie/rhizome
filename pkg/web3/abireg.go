// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/fileutil"
)

// ABI registry: <dir>/abi/<label>.json files letting contract tools resolve
// human labels to addresses and encode calls by method name. ABIs are public
// data, but the directory keeps the web3 dir's uniform 0700 posture.

const (
	abiDirName      = "abi"
	abiMaxEntries   = 200
	abiMaxFileBytes = 512 * 1024
)

var abiLabelRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// ABIEntry is one registered contract ABI.
type ABIEntry struct {
	Label    string    `json:"label"`
	Address  string    `json:"address,omitempty"` // EIP-55 0x address; empty = ABI-only
	ChainIDs []uint64  `json:"chain_ids,omitempty"`
	ABI      *ABI      `json:"abi"`
	AddedAt  time.Time `json:"added_at"`
}

// abiFile is the on-disk envelope — ABI stored as the raw JSON array.
type abiFile struct {
	Label    string          `json:"label"`
	Address  string          `json:"address,omitempty"`
	ChainIDs []uint64        `json:"chain_ids,omitempty"`
	ABI      json.RawMessage `json:"abi"`
	AddedAt  time.Time       `json:"added_at"`
}

// ABIRegistry manages <dir>/abi/*.json.
type ABIRegistry struct {
	dir string
}

// OpenABIRegistry returns the registry rooted at <dir>/abi.
func OpenABIRegistry(dir string) *ABIRegistry {
	return &ABIRegistry{dir: filepath.Join(dir, abiDirName)}
}

// ValidABILabel reports whether label is a usable registry key.
func ValidABILabel(label string) bool {
	return abiLabelRe.MatchString(label)
}

func (r *ABIRegistry) pathFor(label string) (string, error) {
	if !ValidABILabel(label) {
		return "", fmt.Errorf("invalid ABI label %q (want %s)", label, abiLabelRe)
	}
	return filepath.Join(r.dir, label+".json"), nil
}

// Add stores an ABI under label. abiJSON is the contract ABI JSON array;
// address is optional (ABI-only entries decode but can't be sent to).
func (r *ABIRegistry) Add(label, abiJSON, address string, chainIDs []uint64) (*ABIEntry, error) {
	path, err := r.pathFor(label)
	if err != nil {
		return nil, err
	}
	abi, err := ParseABIJSON(json.RawMessage(abiJSON))
	if err != nil {
		return nil, fmt.Errorf("ABI: %w", err)
	}
	if len(abi.Methods) == 0 {
		return nil, fmt.Errorf("ABI for %q declares no functions", label)
	}
	if address != "" {
		address, err = NormalizeAddress(address)
		if err != nil {
			return nil, err
		}
	}
	if len(abiJSON) > abiMaxFileBytes {
		return nil, fmt.Errorf("ABI file exceeds %d bytes", abiMaxFileBytes)
	}
	if _, err := r.Get(label); err == nil {
		return nil, fmt.Errorf("label %q already registered — remove it first", label)
	}
	if entries, err := r.List(); err == nil && len(entries) >= abiMaxEntries {
		return nil, fmt.Errorf("ABI registry is full (%d entries)", abiMaxEntries)
	}
	f := abiFile{
		Label: label, Address: address, ChainIDs: chainIDs,
		ABI: json.RawMessage(abiJSON), AddedAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return nil, err
	}
	if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return nil, err
	}
	return &ABIEntry{
		Label: label, Address: address, ChainIDs: chainIDs,
		ABI: abi, AddedAt: f.AddedAt,
	}, nil
}

// Get returns the entry for label, or nil when unregistered.
func (r *ABIRegistry) Get(label string) (*ABIEntry, error) {
	path, err := r.pathFor(label)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: label is regex-validated.
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no ABI registered for %q", label)
	}
	if err != nil {
		return nil, err
	}
	var f abiFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("corrupt ABI file %s: %w", label, err)
	}
	abi, err := ParseABIJSON(f.ABI)
	if err != nil {
		return nil, fmt.Errorf("ABI %q: %w", label, err)
	}
	return &ABIEntry{
		Label: f.Label, Address: f.Address, ChainIDs: f.ChainIDs,
		ABI: abi, AddedAt: f.AddedAt,
	}, nil
}

// ByAddress returns the registered entry bound to addr, or nil.
func (r *ABIRegistry) ByAddress(addr string) (*ABIEntry, error) {
	entries, err := r.List()
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Address != "" && strings.EqualFold(e.Address, addr) {
			return e, nil
		}
	}
	return nil, nil
}

// List returns all entries sorted by label.
func (r *ABIRegistry) List() ([]*ABIEntry, error) {
	dirEntries, err := os.ReadDir(r.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]*ABIEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		label := strings.TrimSuffix(de.Name(), ".json")
		e, err := r.Get(label)
		if err != nil {
			continue // skip corrupt files rather than failing the listing
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

// Remove deletes label's entry.
func (r *ABIRegistry) Remove(label string) error {
	path, err := r.pathFor(label)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no ABI registered for %q", label)
		}
		return err
	}
	return nil
}
