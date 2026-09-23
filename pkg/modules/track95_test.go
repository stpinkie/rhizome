package modules

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aead.dev/minisign"
	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"

	"github.com/stpinkie/rhizome/pkg/config"
)

// Track 95 — catalog schema v3 release signatures. No catalog tenant
// publishes upstream signatures today (surveyed nimbus-eth1 v0.4.1, helios
// 0.11.1, kubo v0.43.1 — all ship bare archives + checksums only), so the
// mechanism is pinned here with signed fixtures per supported kind.

func installWithSignature(t *testing.T, sig *ReleaseSignature, asset, sigBytes []byte) (*Manager, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sig") {
			w.Write(sigBytes)
			return
		}
		w.Write(asset)
	}))
	t.Cleanup(srv.Close)
	old := downloadBaseURL
	downloadBaseURL = srv.URL
	t.Cleanup(func() { downloadBaseURL = old })

	// The signature URL template resolves {asset} to the fake asset name.
	sig.URL = srv.URL + "/{asset}.sig"
	spec := testSpec("m1")
	sum := sha256.Sum256(asset)
	spec.Install.Releases[0].SHA256[Platform()] = hex.EncodeToString(sum[:])
	spec.Install.Releases[0].Signature = sig
	registerTestSpec(t, spec)

	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)
	return mgr, mgr.Install(t.Context(), "m1", "")
}

func TestInstallMinisignSignature(t *testing.T) {
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubText, err := pub.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	asset := buildTarGz(t, map[string]string{"testbin": "#!/bin/sh\n"})

	// Plain EdDSA signature over the artifact bytes.
	sigText := minisign.SignWithComments(priv, asset, "release test", "fixture")
	sig := &ReleaseSignature{Kind: "minisign", Key: string(pubText)}
	if _, err := installWithSignature(t, sig, asset, sigText); err != nil {
		t.Fatalf("install with valid minisign sig: %v", err)
	}

	// Tampered signature bytes → fatal.
	bad := append([]byte(nil), sigText...)
	bad[len(bad)-20] ^= 0xFF
	if _, err := installWithSignature(t, sig, asset, bad); err == nil ||
		!strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature failure, got %v", err)
	}

	// Wrong key → fatal.
	pub2, _, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub2Text, _ := pub2.MarshalText()
	if _, err := installWithSignature(t,
		&ReleaseSignature{Kind: "minisign", Key: string(pub2Text)}, asset, sigText); err == nil {
		t.Fatal("expected wrong-key signature failure")
	}
}

func TestInstallMinisignHashedSignature(t *testing.T) {
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubText, err := pub.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	asset := buildTarGz(t, map[string]string{"testbin": "#!/bin/sh\n"})

	// HashEdDSA (prehashed) — the streamed verify path.
	r := minisign.NewReader(bytes.NewReader(asset))
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal(err)
	}
	sigText := r.Sign(priv)
	sig := &ReleaseSignature{Kind: "minisign", Key: string(pubText)}
	if _, err := installWithSignature(t, sig, asset, sigText); err != nil {
		t.Fatalf("install with valid prehashed minisign sig: %v", err)
	}
}

func TestInstallCosignBlobSignature(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	asset := buildTarGz(t, map[string]string{"testbin": "#!/bin/sh\n"})

	digest := sha256.Sum256(asset)
	sigDER, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(sigDER)

	sig := &ReleaseSignature{Kind: "cosign-blob", Key: string(pubPEM)}
	if _, err := installWithSignature(t, sig, asset, []byte(sigB64)); err != nil {
		t.Fatalf("install with valid cosign-blob sig: %v", err)
	}

	// Signature over different content → fatal.
	other := sha256.Sum256([]byte("other"))
	sig2, _ := ecdsa.SignASN1(rand.Reader, priv, other[:])
	if _, err := installWithSignature(t, sig, asset,
		[]byte(base64.StdEncoding.EncodeToString(sig2))); err == nil {
		t.Fatal("expected cosign-blob signature failure")
	}
}

func TestInstallEd25519BlobSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	asset := buildTarGz(t, map[string]string{"testbin": "#!/bin/sh\n"})

	raw := ed25519.Sign(priv, asset)
	sig := &ReleaseSignature{Kind: "cosign-blob", Key: string(pubPEM)}
	if _, err := installWithSignature(t, sig, asset,
		[]byte(base64.StdEncoding.EncodeToString(raw))); err != nil {
		t.Fatalf("install with valid ed25519 blob sig: %v", err)
	}
}

func TestInstallGPGSignature(t *testing.T) {
	entity, err := openpgp.NewEntity("Rhizome Test", "", "modules@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	var keyBuf bytes.Buffer
	aw, err := armor.Encode(&keyBuf, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.Serialize(aw); err != nil {
		t.Fatal(err)
	}
	if err := aw.Close(); err != nil {
		t.Fatal(err)
	}

	asset := buildTarGz(t, map[string]string{"testbin": "#!/bin/sh\n"})
	var sigBuf bytes.Buffer
	if err := openpgp.DetachSign(&sigBuf, entity, bytes.NewReader(asset), nil); err != nil {
		t.Fatal(err)
	}

	sig := &ReleaseSignature{Kind: "gpg", Key: keyBuf.String()}
	if _, err := installWithSignature(t, sig, asset, sigBuf.Bytes()); err != nil {
		t.Fatalf("install with valid gpg sig: %v", err)
	}

	// Armored signature also verifies.
	var ascBuf bytes.Buffer
	saw, err := armor.Encode(&ascBuf, openpgp.SignatureType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saw.Write(sigBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := saw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := installWithSignature(t, sig, asset, ascBuf.Bytes()); err != nil {
		t.Fatalf("install with armored gpg sig: %v", err)
	}

	// Sig over different bytes → fatal.
	var otherBuf bytes.Buffer
	if err := openpgp.DetachSign(&otherBuf, entity, bytes.NewReader([]byte("other")), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := installWithSignature(t, sig, asset, otherBuf.Bytes()); err == nil {
		t.Fatal("expected gpg signature failure")
	}
}

func TestInstallSignatureFetchFailureFatal(t *testing.T) {
	asset := buildTarGz(t, map[string]string{"testbin": "#!/bin/sh\n"})
	spec := testSpec("m1")
	sum := sha256.Sum256(asset)
	spec.Install.Releases[0].SHA256[Platform()] = hex.EncodeToString(sum[:])
	registerTestSpec(t, spec)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sig") {
			http.NotFound(w, r)
			return
		}
		w.Write(asset)
	}))
	t.Cleanup(srv.Close)
	old := downloadBaseURL
	downloadBaseURL = srv.URL
	t.Cleanup(func() { downloadBaseURL = old })

	spec.Install.Releases[0].Signature = &ReleaseSignature{
		Kind: "minisign",
		URL:  srv.URL + "/{asset}.sig",
		Key:  "unused",
	}
	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)
	err := mgr.Install(t.Context(), "m1", "")
	if err == nil || !strings.Contains(err.Error(), "signature fetch") {
		t.Fatalf("expected signature fetch failure, got %v", err)
	}
	if mgr.installedVersion("m1") != "" {
		t.Fatal("module marked installed after signature fetch failure")
	}
}

func TestSignatureURLExpansion(t *testing.T) {
	spec := testSpec("m1")
	r := spec.Install.Releases[0]
	r.Signature = &ReleaseSignature{
		Kind: "minisign",
		URL:  "https://example.com/dl/{tag}/{asset}.minisig?v={version}",
		Key:  "x",
	}
	got := spec.SignatureURL(r)
	asset := spec.Asset(r)
	if !strings.Contains(got, asset+".minisig") ||
		!strings.Contains(got, "/v1.0.0/") ||
		!strings.HasSuffix(got, "?v=1.0.0") {
		t.Fatalf("SignatureURL = %q", got)
	}
	noSig := r
	noSig.Signature = nil
	if spec.SignatureURL(noSig) != "" {
		t.Fatal("SignatureURL non-empty without signature")
	}
}

func TestCatalogV3EmittedWhenSignaturePresent(t *testing.T) {
	spec := testSpec("m1")
	spec.Install.Releases[0].Signature = &ReleaseSignature{
		Kind: "minisign", URL: "https://example.com/{asset}.minisig", Key: "x",
	}
	registerTestSpec(t, spec)
	if got := catalogVersionRequired(catalog); got != 3 {
		t.Fatalf("catalogVersionRequired = %d, want 3", got)
	}
	data, err := MarshalCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var env CatalogEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.CatalogVersion != 3 {
		t.Fatalf("emitted catalog_version = %d, want 3", env.CatalogVersion)
	}
}

func TestSchemaGateRejectsMalformedSignatures(t *testing.T) {
	bad := func(sig *ReleaseSignature) []byte {
		spec := testSpec("m1")
		spec.Install.Releases[0].Signature = sig
		data, _ := json.Marshal(CatalogEnvelope{CatalogVersion: 3, Modules: []ModuleSpec{spec}})
		return data
	}
	for name, sig := range map[string]*ReleaseSignature{
		"no kind":  {Kind: "", URL: "https://x/sig", Key: "k"},
		"bad kind": {Kind: "pgp2", URL: "https://x/sig", Key: "k"},
		"no url":   {Kind: "minisign", URL: "", Key: "k"},
		"no key":   {Kind: "minisign", URL: "https://x/sig", Key: ""},
	} {
		if _, err := parseCatalogEnvelope(bad(sig)); err == nil {
			t.Fatalf("%s signature accepted", name)
		}
	}
}
