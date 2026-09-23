// Rhizome - Ultra-lightweight personal agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"aead.dev/minisign"
	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// verifyUpstreamSignature enforces a declared release signature (catalog
// schema v3): the signature artifact is fetched over HTTPS (loopback
// allowed for tests) and verified against the downloaded archive and the
// catalog-pinned key. The digest check has already passed at this point —
// the signature adds upstream provenance on top of the digest floor.
// Declared-but-unverifiable is always fatal.
func (m *Manager) verifyUpstreamSignature(
	ctx context.Context, spec ModuleSpec, release ReleasePin, artifactPath string,
) error {
	sig := release.Signature
	if sig == nil {
		return nil
	}
	sigURL := spec.SignatureURL(release)
	if !strings.HasPrefix(sigURL, "https://") && !isLoopbackURL(sigURL) {
		return fmt.Errorf("signature URL is not HTTPS: %s", redactURL(sigURL))
	}
	sigBytes, err := fetchBounded(ctx, sigURL)
	if err != nil {
		return fmt.Errorf("signature fetch failed: %w", err)
	}
	if err := verifyArtifactSignature(sig, artifactPath, sigBytes); err != nil {
		return fmt.Errorf("upstream signature verification failed: %w", err)
	}
	return nil
}

// verifyArtifactSignature dispatches on the declared signature kind.
func verifyArtifactSignature(sig *ReleaseSignature, artifactPath string, sigBytes []byte) error {
	switch sig.Kind {
	case "minisign":
		return verifyMinisign(sig.Key, artifactPath, sigBytes)
	case "cosign-blob":
		return verifyCosignBlob(sig.Key, artifactPath, sigBytes)
	case "gpg":
		return verifyGPG(sig.Key, artifactPath, sigBytes)
	default:
		return fmt.Errorf("unsupported signature kind %q", sig.Kind)
	}
}

// verifyMinisign verifies a .minisig file against the artifact. Both
// minisign algorithms are supported: plain EdDSA ("Ed", signature over the
// content — needs the whole file) and HashEdDSA ("ED", signature over the
// BLAKE2b-512 digest — streamed via minisign.Reader).
func verifyMinisign(keyText, artifactPath string, sigBytes []byte) error {
	var pub minisign.PublicKey
	if err := pub.UnmarshalText([]byte(keyText)); err != nil {
		return fmt.Errorf("minisign public key: %w", err)
	}
	var sig minisign.Signature
	if err := sig.UnmarshalText(sigBytes); err != nil {
		return fmt.Errorf("minisig file: %w", err)
	}
	//nolint:gosec // G304: artifactPath is the module download temp file.
	f, err := os.Open(artifactPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var ok bool
	if sig.Algorithm == minisign.HashEdDSA {
		r := minisign.NewReader(f)
		if _, err := io.Copy(io.Discard, r); err != nil {
			return err
		}
		ok = r.Verify(pub, sigBytes)
	} else {
		msg, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		ok = minisign.Verify(pub, msg, sigBytes)
	}
	if !ok {
		return errors.New("minisign signature does not verify")
	}
	return nil
}

// verifyCosignBlob verifies a `cosign sign-blob`-style raw signature. The
// pinned key is a PEM public key: ECDSA keys verify SHA256(artifact) with
// ASN.1 signatures (cosign's default); Ed25519 keys verify the artifact
// directly. Signature files are base64 text; raw binary is also accepted.
func verifyCosignBlob(keyPEM, artifactPath string, sigBytes []byte) error {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return errors.New("cosign-blob key: not PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("cosign-blob key: %w", err)
	}
	sig := decodeSignatureBytes(sigBytes)

	//nolint:gosec // G304: artifactPath is the module download temp file.
	f, err := os.Open(artifactPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		digest := sha256.New()
		if _, err := io.Copy(digest, f); err != nil {
			return err
		}
		if !ecdsa.VerifyASN1(k, digest.Sum(nil), sig) {
			return errors.New("cosign-blob signature does not verify")
		}
		return nil
	case ed25519.PublicKey:
		msg, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		if !ed25519.Verify(k, msg, sig) {
			return errors.New("cosign-blob signature does not verify")
		}
		return nil
	default:
		return fmt.Errorf("cosign-blob: unsupported public key type %T", pub)
	}
}

// verifyGPG verifies a detached OpenPGP signature (.sig or armored .asc)
// against the artifact with the pinned public key (armored or binary).
func verifyGPG(keyText, artifactPath string, sigBytes []byte) error {
	keyring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(keyText))
	if err != nil {
		keyring, err = openpgp.ReadKeyRing(strings.NewReader(keyText))
		if err != nil {
			return fmt.Errorf("gpg public key: %w", err)
		}
	}

	var sigReader io.Reader = bytes.NewReader(sigBytes)
	if bytes.HasPrefix(bytes.TrimSpace(sigBytes), []byte("-----BEGIN PGP")) {
		block, err := armor.Decode(bytes.NewReader(sigBytes))
		if err != nil {
			return fmt.Errorf("gpg signature armor: %w", err)
		}
		sigReader = block.Body
	}

	//nolint:gosec // G304: artifactPath is the module download temp file.
	f, err := os.Open(artifactPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, err := openpgp.CheckDetachedSignature(keyring, f, sigReader, nil); err != nil {
		return fmt.Errorf("gpg signature does not verify: %w", err)
	}
	return nil
}

// decodeSignatureBytes returns the signature payload: base64-decoded when
// the file is base64 text, the raw bytes otherwise.
func decodeSignatureBytes(b []byte) []byte {
	if decoded, err := base64.StdEncoding.DecodeString(
		strings.TrimSpace(string(b))); err == nil && len(decoded) > 0 {
		return decoded
	}
	return b
}
