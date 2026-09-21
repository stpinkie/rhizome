// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
)

// stubCatalogKey swaps in a throwaway Ed25519 keypair as the catalog trust
// root for the duration of a test. Returns the base64 seed for signing.
func stubCatalogKey(t *testing.T) string {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := releasePubKeyB64
	releasePubKeyB64 = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { releasePubKeyB64 = old })
	return base64.StdEncoding.EncodeToString(priv.Seed())
}

// catalogServer serves a signed (or tampered) remote catalog over a
// loopback httptest server and counts catalog.json fetches.
func catalogServer(t *testing.T, mods []ModuleSpec, seedB64 string, tamper bool) (*httptest.Server, *int) {
	t.Helper()
	fetches := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog.json":
			*fetches++
			env := CatalogEnvelope{CatalogVersion: 1, Modules: mods}
			data, _ := json.MarshalIndent(env, "", "  ")
			if tamper {
				// Flip an entry after signing bytes are produced — content
				// no longer matches the signature.
				data = append(data, ' ')
			}
			_, _ = w.Write(data)
		case "/catalog.json.sig":
			env := CatalogEnvelope{CatalogVersion: 1, Modules: mods}
			data, _ := json.MarshalIndent(env, "", "  ")
			sig, err := SignCatalog(data, seedB64)
			if err != nil {
				t.Errorf("SignCatalog: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(sig))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, fetches
}

func remoteTestSpec(id string) ModuleSpec {
	return ModuleSpec{
		ID: id, Name: id + " remote", Kind: KindConfig, License: "MIT",
		Description: "remote test module",
		Install:     InstallSpec{Method: "config"},
		ConfigFields: []ConfigField{
			{Key: "endpoint", Label: "Endpoint", Required: true},
		},
	}
}

func indexCfg(url string) *config.Config {
	return &config.Config{ModuleIndex: config.ModuleIndexConfig{Enabled: true, URL: url}}
}

func TestCatalogSignVerifyRoundTrip(t *testing.T) {
	seed := stubCatalogKey(t)
	data, err := MarshalCatalog()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := SignCatalog(data, seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCatalogSignature(data, sig); err != nil {
		t.Fatalf("VerifyCatalogSignature: %v", err)
	}
	// Bit-flipped content must not verify.
	if err := VerifyCatalogSignature(append(data, ' '), sig); err == nil {
		t.Fatal("tampered catalog verified")
	}
	// Nor a signature from a different key.
	_, otherSeed, err := GenerateCatalogKeypair()
	if err != nil {
		t.Fatal(err)
	}
	bad, _ := SignCatalog(data, otherSeed)
	if err := VerifyCatalogSignature(data, bad); err == nil {
		t.Fatal("foreign signature verified")
	}
}

func TestRemoteCatalogMerge(t *testing.T) {
	seed := stubCatalogKey(t)
	registerTestSpec(t, testSpec("embedded-mod"))

	remote := []ModuleSpec{
		remoteTestSpec("remote-mod"),
		// A remote entry colliding with an embedded id must be shadowed.
		remoteTestSpec("embedded-mod"),
	}
	srv, _ := catalogServer(t, remote, seed, false)

	mgr, _, _ := newTestManager(t, indexCfg(srv.URL))
	specs := mgr.specs()
	ids := make([]string, 0, len(specs))
	for _, s := range specs {
		ids = append(ids, s.ID)
	}
	if len(ids) != 2 || ids[0] != "embedded-mod" || ids[1] != "remote-mod" {
		t.Fatalf("merged ids = %v, want [embedded-mod remote-mod]", ids)
	}
	// The embedded entry wins the collision — name proves it.
	spec, src, ok := mgr.lookupSpec("embedded-mod")
	if !ok || src != "embedded" || spec.Name != "embedded-mod" {
		t.Fatalf("embedded-mod resolved to (%q, %q)", spec.Name, src)
	}
	spec, src, ok = mgr.lookupSpec("remote-mod")
	if !ok || src != "remote" || spec.Name != "remote-mod remote" {
		t.Fatalf("remote-mod resolved to (%q, %q)", spec.Name, src)
	}
	info, err := mgr.Info("remote-mod")
	if err != nil {
		t.Fatal(err)
	}
	if info.Source != "remote" {
		t.Fatalf("Info.Source = %q, want remote", info.Source)
	}
}

func TestRemoteCatalogBadSignatureRefused(t *testing.T) {
	seed := stubCatalogKey(t)
	registerTestSpec(t, testSpec("embedded-mod"))

	// Server signs a DIFFERENT body than it serves (tamper=true appends a
	// byte to the served catalog after signing).
	srv, _ := catalogServer(t, []ModuleSpec{remoteTestSpec("remote-mod")}, seed, true)

	mgr, _, _ := newTestManager(t, indexCfg(srv.URL))
	if specs := mgr.specs(); len(specs) != 1 || specs[0].ID != "embedded-mod" {
		t.Fatalf("bad-sig remote was served: %+v", specs)
	}
}

func TestRemoteCatalogUnsignedRefused(t *testing.T) {
	stubCatalogKey(t)
	registerTestSpec(t, testSpec("embedded-mod"))

	// .sig endpoint 404s → no signature → refuse.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/catalog.json" {
			data, _ := json.Marshal(CatalogEnvelope{CatalogVersion: 1, Modules: []ModuleSpec{remoteTestSpec("r")}})
			_, _ = w.Write(data)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	mgr, _, _ := newTestManager(t, indexCfg(srv.URL))
	if specs := mgr.specs(); len(specs) != 1 || specs[0].ID != "embedded-mod" {
		t.Fatalf("unsigned remote was served: %+v", specs)
	}
}

func TestRemoteCatalogSchemeRefused(t *testing.T) {
	stubCatalogKey(t)
	registerTestSpec(t, testSpec("embedded-mod"))
	// http on a non-loopback host must refuse before any fetch.
	mgr, _, _ := newTestManager(t, indexCfg("http://modules.example.com"))
	if specs := mgr.specs(); len(specs) != 1 {
		t.Fatalf("plain-HTTP index served remote specs: %+v", specs)
	}
}

func TestRemoteCatalogCacheTTL(t *testing.T) {
	seed := stubCatalogKey(t)
	registerTestSpec(t, testSpec("embedded-mod"))
	srv, fetches := catalogServer(t, []ModuleSpec{remoteTestSpec("remote-mod")}, seed, false)

	mgr, _, _ := newTestManager(t, indexCfg(srv.URL))
	mgr.specs() // populate cache
	if *fetches != 1 {
		t.Fatalf("fetches = %d, want 1", *fetches)
	}
	mgr.specs() // fresh cache → no refetch
	if *fetches != 1 {
		t.Fatalf("fetches = %d, want 1 (cache hit)", *fetches)
	}

	// Expire the cache artificially → next read refetches.
	old := remoteCatalogTTL
	remoteCatalogTTL = time.Millisecond
	t.Cleanup(func() { remoteCatalogTTL = old })
	time.Sleep(5 * time.Millisecond)
	mgr.specs()
	if *fetches != 2 {
		t.Fatalf("fetches = %d, want 2 after TTL expiry", *fetches)
	}
}

func TestRemoteCatalogStaleServeOnFetchFailure(t *testing.T) {
	seed := stubCatalogKey(t)
	registerTestSpec(t, testSpec("embedded-mod"))

	srv, _ := catalogServer(t, []ModuleSpec{remoteTestSpec("remote-mod")}, seed, false)
	mgr, _, _ := newTestManager(t, indexCfg(srv.URL))
	mgr.specs() // populate cache
	srv.Close()

	// Fetch fails but a verified cache exists → stale cache still serves.
	old := remoteCatalogTTL
	remoteCatalogTTL = time.Millisecond
	t.Cleanup(func() { remoteCatalogTTL = old })
	time.Sleep(5 * time.Millisecond)
	specs := mgr.specs()
	found := false
	for _, s := range specs {
		if s.ID == "remote-mod" {
			found = true
		}
	}
	if !found {
		t.Fatal("stale verified cache was not served on fetch failure")
	}
}

func TestRemoteCatalogNoCacheOnFetchFailure(t *testing.T) {
	seed := stubCatalogKey(t)
	registerTestSpec(t, testSpec("embedded-mod"))

	srv, _ := catalogServer(t, []ModuleSpec{remoteTestSpec("remote-mod")}, seed, false)
	url := srv.URL
	srv.Close() // dead before first fetch → no cache exists

	mgr, _, _ := newTestManager(t, indexCfg(url))
	if specs := mgr.specs(); len(specs) != 1 {
		t.Fatalf("expected embedded-only on fetch failure, got %+v", specs)
	}
}

func TestRemoteCatalogDisabledByDefault(t *testing.T) {
	seed := stubCatalogKey(t)
	registerTestSpec(t, testSpec("embedded-mod"))
	srv, fetches := catalogServer(t, []ModuleSpec{remoteTestSpec("r")}, seed, false)

	// module_index absent → no fetch at all.
	mgr, _, _ := newTestManager(t, &config.Config{})
	_ = mgr.specs()
	if *fetches != 0 {
		t.Fatal("remote fetch attempted with module_index disabled")
	}
	// enabled but no URL → still nothing.
	mgr, _, _ = newTestManager(t, &config.Config{
		ModuleIndex: config.ModuleIndexConfig{Enabled: true},
	})
	_ = mgr.specs()
	if *fetches != 0 {
		t.Fatal("remote fetch attempted with empty module_index.url")
	}
	_ = srv
}

func TestVerifyDetectsDrift(t *testing.T) {
	spec := testSpec("m1")
	registerTestSpec(t, spec)

	asset := buildTarGz(t, map[string]string{"testbin": "#!/bin/sh\n"})
	digest := sha256.Sum256(asset)
	spec.Install.Releases[0].SHA256[Platform()] = hex.EncodeToString(digest[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(asset)
	}))
	defer srv.Close()
	old := downloadBaseURL
	downloadBaseURL = srv.URL
	t.Cleanup(func() { downloadBaseURL = old })

	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)
	if err := mgr.Install(t.Context(), "m1", ""); err != nil {
		t.Fatalf("Install: %v", err)
	}

	res, err := mgr.Verify("m1")
	if err != nil {
		t.Fatalf("Verify clean install: %v", err)
	}
	if !res.Match || res.Version != "1.0.0" || res.Algorithm != "sha256" {
		t.Fatalf("unexpected VerifyResult: %+v", res)
	}

	// Tamper the installed binary → Verify must report the mismatch.
	bin := mgr.binaryPath(spec)
	if err := os.WriteFile(bin, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	res, err = mgr.Verify("m1")
	if err == nil || res.Match {
		t.Fatalf("tampered binary verified: res=%+v err=%v", res, err)
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("error = %v, want mismatch", err)
	}
}

func TestVerifyUninstalledAndConfigKind(t *testing.T) {
	spec := testSpec("m1")
	registerTestSpec(t, spec)
	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)

	if _, err := mgr.Verify("m1"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("verify uninstalled: %v", err)
	}
	if _, err := mgr.Verify("nope"); err == nil || !strings.Contains(err.Error(), "unknown module") {
		t.Fatalf("verify unknown id: %v", err)
	}
}

func TestModuleIndexConfigDecodes(t *testing.T) {
	var cfg config.Config
	err := json.Unmarshal([]byte(`{"module_index":{"enabled":true,"url":"https://idx.example.com"}}`), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ModuleIndex.Enabled || cfg.ModuleIndex.URL != "https://idx.example.com" {
		t.Fatalf("module_index decoded as %+v", cfg.ModuleIndex)
	}
}

func TestMarshalCatalogDeterministic(t *testing.T) {
	a, err := MarshalCatalog()
	if err != nil {
		t.Fatal(err)
	}
	b, err := MarshalCatalog()
	if err != nil {
		t.Fatal(err)
	}
	// generated_at differs across calls — compare structure, not bytes.
	var ea, eb CatalogEnvelope
	if err := json.Unmarshal(a, &ea); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &eb); err != nil {
		t.Fatal(err)
	}
	if len(ea.Modules) != len(eb.Modules) || len(ea.Modules) != len(catalog) {
		t.Fatalf("envelope module count = %d vs %d vs catalog %d",
			len(ea.Modules), len(eb.Modules), len(catalog))
	}
	if ea.CatalogVersion != catalogVersionSupported {
		t.Fatalf("catalog_version = %d", ea.CatalogVersion)
	}
	// Entries must parse through the schema gate.
	if _, err := parseCatalogEnvelope(a); err != nil {
		t.Fatalf("own catalog fails schema check: %v", err)
	}
}

func TestParseCatalogEnvelopeSchemaGate(t *testing.T) {
	// Newer schema than this binary understands → refused.
	future, _ := json.Marshal(CatalogEnvelope{CatalogVersion: 99})
	if _, err := parseCatalogEnvelope(future); err == nil ||
		!strings.Contains(err.Error(), "catalog_version") {
		t.Fatalf("future schema accepted: %v", err)
	}
	// Zero/negative version → refused.
	if _, err := parseCatalogEnvelope([]byte(`{"catalog_version":0,"modules":[]}`)); err == nil {
		t.Fatal("version-0 catalog accepted")
	}
	// Duplicate ids → refused.
	dup, _ := json.Marshal(CatalogEnvelope{CatalogVersion: 1, Modules: []ModuleSpec{
		remoteTestSpec("x"), remoteTestSpec("x"),
	}})
	if _, err := parseCatalogEnvelope(dup); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate ids accepted: %v", err)
	}
}
