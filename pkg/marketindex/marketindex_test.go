// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package marketindex

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

func validDoc(t *testing.T) []byte {
	t.Helper()
	adv, _ := json.Marshal(map[string]any{"v": 1, "module": "rhizome-market"})
	idx := Index{
		V:         1,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Seq:       7,
		Providers: []IndexProvider{{
			PeerID: testutil.NewIdentity(t).PeerID,
			Addrs:  []string{"/ip4/127.0.0.1/tcp/1"},
			Advert: adv,
			Attestations: []json.RawMessage{
				json.RawMessage(`{"kind":"runtime","issuer":"curator"}`),
			},
		}},
	}
	doc, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestParseValid(t *testing.T) {
	idx, err := Parse(validDoc(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if idx.Seq != 7 || len(idx.Providers) != 1 {
		t.Fatalf("idx = %+v", idx)
	}
	if idx.Expired(time.Now()) {
		t.Fatal("unexpired index reported expired")
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(doc map[string]any)
	}{
		{"bad version", func(d map[string]any) { d["v"] = 2 }},
		{"missing updated_at", func(d map[string]any) { delete(d, "updated_at") }},
		{"bad updated_at", func(d map[string]any) { d["updated_at"] = "yesterday" }},
		{"bad expires_at", func(d map[string]any) { d["expires_at"] = "soon" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(validDoc(t), &doc); err != nil {
				t.Fatal(err)
			}
			tc.mutate(doc)
			raw, _ := json.Marshal(doc)
			if _, err := Parse(raw); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func TestParseRejectsBadPeerID(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(validDoc(t), &doc); err != nil {
		t.Fatal(err)
	}
	providers := doc["providers"].([]any)
	providers[0].(map[string]any)["peer_id"] = "not-a-peer"
	raw, _ := json.Marshal(doc)
	if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "peer_id") {
		t.Fatalf("want peer_id refusal, got %v", err)
	}
}

func TestParseRejectsDuplicatePeer(t *testing.T) {
	idx, err := Parse(validDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	dup := idx.Providers[0]
	idx.Providers = append(idx.Providers, dup)
	if err := idx.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate refusal, got %v", err)
	}
}

func TestParseRejectsOversize(t *testing.T) {
	if _, err := Parse(make([]byte, MaxBytes+1)); err == nil {
		t.Fatal("oversize index accepted")
	}
	idx := &Index{V: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	for i := 0; i < MaxEntries+1; i++ {
		idx.Providers = append(idx.Providers, IndexProvider{
			PeerID: fmt.Sprintf("p%d", i), Advert: json.RawMessage(`{}`),
		})
	}
	if err := idx.Validate(); err == nil || !strings.Contains(err.Error(), "providers") {
		t.Fatalf("want entry-count refusal, got %v", err)
	}
}

func TestExpired(t *testing.T) {
	idx := &Index{V: 1}
	if idx.Expired(time.Now()) {
		t.Fatal("no expires_at should never be expired")
	}
	idx.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if !idx.Expired(time.Now()) {
		t.Fatal("past expires_at not expired")
	}
	idx.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if idx.Expired(time.Now()) {
		t.Fatal("future expires_at reported expired")
	}
}

func TestAddrsOrMultiaddr(t *testing.T) {
	p := &IndexProvider{}
	if len(p.AddrsOrMultiaddr()) != 0 {
		t.Fatal("empty provider has addrs")
	}
	p.Multiaddr = "/ip4/1.2.3.4/tcp/9"
	if got := p.AddrsOrMultiaddr(); len(got) != 1 || got[0] != p.Multiaddr {
		t.Fatalf("multiaddr fallback = %v", got)
	}
	p.Addrs = []string{"/dns4/x", "/ip4/y"}
	if got := p.AddrsOrMultiaddr(); len(got) != 2 || got[0] != "/dns4/x" {
		t.Fatalf("addrs preferred = %v", got)
	}
}

func TestAttestationKinds(t *testing.T) {
	p := &IndexProvider{Attestations: []json.RawMessage{
		json.RawMessage(`{"kind":"runtime","issuer":"curator"}`),
		json.RawMessage(`{"kind":"uptime"}`),
		json.RawMessage(`"garbage"`),
	}}
	got := p.AttestationKinds()
	want := []string{"runtime@curator", "uptime", "unknown"}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
