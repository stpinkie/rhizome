// Rhizome - Ultra-lightweight personal agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// Package sigverify holds the shared release-signing trust root: a single
// baked-in Ed25519 public key that verifies detached signatures over
// first-party published documents (the remote module catalog and the
// curated skills index). The matching private key lives only in the
// MODULE_CATALOG_SIGNING_KEY GitHub secret — see
// docs/operations/module-catalog-signing.md and
// docs/operations/skill-index-signing.md.
package sigverify

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// ReleasePubKeyB64 is the baked-in Ed25519 public key (base64) that signs
// release documents. It is a variable so tests can substitute their own
// keypair.
var ReleasePubKeyB64 = "Ww3Kz/J38L0ColSCrcOjq6I/3WCzsvZMvecGyyrDQzI="

// ReleasePubKey decodes the baked-in release public key.
func ReleasePubKey() (ed25519.PublicKey, error) {
	if ReleasePubKeyB64 == "" {
		return nil, errors.New("no release signing key baked into this build")
	}
	raw, err := base64.StdEncoding.DecodeString(ReleasePubKeyB64)
	if err != nil {
		return nil, fmt.Errorf("release pubkey: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("release pubkey: %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// VerifyReleaseSignature checks sigB64 (base64 Ed25519 signature) over the
// exact document bytes as published.
func VerifyReleaseSignature(data []byte, sigB64 string) error {
	pub, err := ReleasePubKey()
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return fmt.Errorf("release signature: %w", err)
	}
	if !ed25519.Verify(pub, data, sig) {
		return errors.New("release signature does not verify")
	}
	return nil
}

// SignRelease signs document bytes with a base64-encoded Ed25519 seed and
// returns the base64 signature. Used by the `module catalog` and
// `skills index` emit commands during release, and by tests.
func SignRelease(data []byte, seedB64 string) (string, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seedB64))
	if err != nil {
		return "", fmt.Errorf("release signing seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return "", fmt.Errorf("release signing seed: %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), data)
	return base64.StdEncoding.EncodeToString(sig), nil
}

// GenerateKeypair creates a fresh Ed25519 release signing keypair.
// Returns (base64 public key, base64 seed). The seed belongs in the
// MODULE_CATALOG_SIGNING_KEY GitHub secret, the pubkey in ReleasePubKeyB64.
func GenerateKeypair() (pubB64, seedB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub),
		base64.StdEncoding.EncodeToString(priv.Seed()), nil
}
