// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stpinkie/rhizome/pkg/utils"
)

// installRelease downloads the pinned release for the current platform,
// verifies its pinned digest (sha256 or sha512, per the release pin), and
// extracts it under <modules>/<id>/<version>/.
func (m *Manager) installRelease(ctx context.Context, spec ModuleSpec, version string) error {
	release, ok := spec.Release(version)
	if !ok {
		return fmt.Errorf("module %q: no pinned release for version %q", spec.ID, version)
	}
	platform := Platform()
	want, algo := release.Digest(platform)
	if want == "" {
		return fmt.Errorf(
			"module %q v%s has no digest for %s — refusing to install",
			spec.ID, release.Version, platform,
		)
	}
	var sum hash.Hash
	switch algo {
	case "sha256":
		sum = sha256.New()
	case "sha512":
		sum = sha512.New()
	default:
		return fmt.Errorf("module %q: unsupported digest algorithm %q", spec.ID, algo)
	}
	url := spec.DownloadURL(release)
	if !strings.HasPrefix(url, "https://") && !isLoopbackURL(url) {
		return fmt.Errorf("module %q: download URL is not HTTPS: %s", spec.ID, url)
	}

	tmp, err := os.CreateTemp("", "rhizome-module-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := m.download(ctx, url, io.MultiWriter(tmp, sum)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("module %q: download failed: %w", spec.ID, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	got := hex.EncodeToString(sum.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf(
			"module %q v%s: %s mismatch — got %s, want %s (refusing to install)",
			spec.ID, release.Version, algo, got, want,
		)
	}

	// Digest is the floor; a declared upstream signature adds provenance.
	// Absent is fine — declared-but-unverifiable is fatal before extraction.
	if release.Signature != nil {
		if err := m.verifyUpstreamSignature(ctx, spec, release, tmpPath); err != nil {
			return fmt.Errorf("module %q v%s: %w", spec.ID, release.Version, err)
		}
	}

	dest := filepath.Join(m.Dir(spec.ID), release.Version)
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	binPath, err := extractArchive(tmpPath, dest, spec.Install.Binary, spec.Asset(release))
	if err != nil {
		_ = os.RemoveAll(dest)
		return fmt.Errorf("module %q: extract failed: %w", spec.ID, err)
	}
	// Record the extracted binary's digest so `module verify` can detect
	// post-install drift offline. The artifact digest was verified above, so
	// this records what a trusted install produced.
	if binPath != "" {
		if err := recordBinaryDigest(binPath, dest); err != nil {
			_ = os.RemoveAll(dest)
			return fmt.Errorf("module %q: %w", spec.ID, err)
		}
	}
	return m.markInstalled(spec.ID, release.Version)
}

// binaryDigestFile is the per-version record of the installed binary's
// sha256, written at install time for `module verify` drift detection.
const binaryDigestFile = ".binary-digest"

// recordBinaryDigest hashes binPath (sha256) and writes the hex digest to
// <dest>/.binary-digest.
func recordBinaryDigest(binPath, dest string) error {
	f, err := os.Open(binPath) //nolint:gosec // G304: path is the just-extracted module binary.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return err
	}
	digest := hex.EncodeToString(sum.Sum(nil)) + "\n"
	return os.WriteFile(filepath.Join(dest, binaryDigestFile), []byte(digest), 0o600)
}

// isLoopbackURL reports whether a URL targets loopback — the one case where
// plain HTTP is acceptable (local test servers, development registries).
func isLoopbackURL(url string) bool {
	return utils.IsLoopbackURL(url)
}

// download streams a URL into w with the module HTTP client.
func (m *Manager) download(ctx context.Context, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// maxMemberBytes caps a single archive member — bounded so a hostile or
// corrupt archive cannot expand without limit (decompression-bomb guard).
const maxMemberBytes = 512 << 20

// extractArchive dispatches on the resolved asset suffix: .zip → extractZip,
// .tar.gz/.tgz → extractTarGz, anything else refuses.
func extractArchive(archivePath, dest, binaryName, assetName string) (string, error) {
	switch {
	case strings.HasSuffix(assetName, ".zip"):
		return extractZip(archivePath, dest, binaryName)
	case strings.HasSuffix(assetName, ".tar.gz"), strings.HasSuffix(assetName, ".tgz"):
		return extractTarGz(archivePath, dest, binaryName)
	default:
		return "", fmt.Errorf("unsupported archive format: %s", assetName)
	}
}

// extractTarGz unpacks a .tar.gz into dest. It finds the module binary by
// basename (allowing archives that wrap contents in a top-level directory),
// marks it executable, refuses path-traversal entries, and returns the path
// of the extracted binary ("" when binaryName is empty).
func extractTarGz(archivePath, dest, binaryName string) (string, error) {
	//nolint:gosec // G304: archivePath is the download temp file just written.
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer func() { _ = gz.Close() }()

	wantBase := binaryName
	tr := tar.NewReader(gz)
	binaryPath := ""
	foundBinary := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		// Reject anything that would escape dest.
		name := filepath.Clean(hdr.Name)
		if name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) ||
			filepath.IsAbs(name) {
			return "", fmt.Errorf("archive contains unsafe path %q", hdr.Name)
		}
		target := filepath.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if hdr.Size > maxMemberBytes {
				return "", fmt.Errorf("archive member %q exceeds %d-byte cap", hdr.Name, maxMemberBytes)
			}
			base := filepath.Base(name)
			if base == wantBase || base == wantBase+".exe" {
				// Flatten the module binary to the version dir root so
				// binaryPath finds it regardless of archive layout
				// (e.g. nimbus ships it under build/).
				target = filepath.Join(dest, base)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return "", err
			}
			mode := hdr.FileInfo().Mode().Perm()
			if mode == 0 {
				mode = 0o600
			}
			//nolint:gosec // G304: target is validated to stay under dest above.
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return "", err
			}
			n, err := io.CopyN(out, tr, maxMemberBytes+1)
			if err != nil && err != io.EOF {
				_ = out.Close()
				return "", err
			}
			if n > maxMemberBytes {
				_ = out.Close()
				return "", fmt.Errorf("archive member %q exceeds %d-byte cap", hdr.Name, maxMemberBytes)
			}
			if err := out.Close(); err != nil {
				return "", err
			}
			if base == wantBase || base == wantBase+".exe" {
				//nolint:gosec // G302: the module binary must be executable.
				_ = os.Chmod(target, 0o755)
				binaryPath = target
				foundBinary = true
			}
		}
	}
	if wantBase != "" && !foundBinary {
		return "", fmt.Errorf("archive does not contain binary %q", wantBase)
	}
	return binaryPath, nil
}

// extractZip unpacks a .zip into dest with the same guards as
// extractTarGz: path-traversal rejection, a per-member size cap, the module
// binary flattened to the version-dir root and marked executable, and a
// foundBinary error when absent. Windows-built zips may carry
// File.Mode()==0 — members default to 0600, the binary to 0755. Zip-encoded
// symlinks are rejected outright (a symlink member followed by a regular
// member would let content escape dest through the link).
func extractZip(archivePath, dest, binaryName string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("invalid zip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	wantBase := binaryName
	destClean := filepath.Clean(dest)
	binaryPath := ""
	foundBinary := false
	for _, f := range zr.File {
		// Reject anything that would escape dest.
		name := filepath.Clean(f.Name)
		if name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) ||
			filepath.IsAbs(name) {
			return "", fmt.Errorf("archive contains unsafe path %q", f.Name)
		}
		target := filepath.Join(dest, name)
		// Defense-in-depth: the resolved path must stay under dest.
		targetClean := filepath.Clean(target)
		if targetClean != destClean &&
			!strings.HasPrefix(targetClean, destClean+string(filepath.Separator)) {
			return "", fmt.Errorf("archive member %q escapes target dir", f.Name)
		}
		mode := f.FileInfo().Mode()
		if mode&os.ModeSymlink != 0 {
			return "", fmt.Errorf("archive contains symlink %q; symlinks are not allowed", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return "", err
			}
			continue
		}
		if !mode.IsRegular() {
			continue // device/fifo/etc. members carry no extractable payload
		}
		if f.UncompressedSize64 > maxMemberBytes {
			return "", fmt.Errorf("archive member %q exceeds %d-byte cap", f.Name, maxMemberBytes)
		}
		base := filepath.Base(name)
		isBinary := base == wantBase || base == wantBase+".exe"
		if isBinary {
			// Flatten the module binary to the version dir root.
			target = filepath.Join(dest, base)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return "", err
		}
		perm := mode.Perm()
		if perm == 0 {
			perm = 0o600
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		//nolint:gosec // G304: target is validated to stay under dest above.
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
		if err != nil {
			_ = rc.Close()
			return "", err
		}
		n, err := io.CopyN(out, rc, maxMemberBytes+1)
		_ = rc.Close()
		if err != nil && err != io.EOF {
			_ = out.Close()
			return "", err
		}
		if n > maxMemberBytes {
			_ = out.Close()
			return "", fmt.Errorf("archive member %q exceeds %d-byte cap", f.Name, maxMemberBytes)
		}
		if err := out.Close(); err != nil {
			return "", err
		}
		if isBinary {
			//nolint:gosec // G302: the module binary must be executable.
			_ = os.Chmod(target, 0o755)
			binaryPath = target
			foundBinary = true
		}
	}
	if wantBase != "" && !foundBinary {
		return "", fmt.Errorf("archive does not contain binary %q", wantBase)
	}
	return binaryPath, nil
}

// detectBinary resolves a "detect"-method module by finding its binary on
// PATH (or an absolute path when Binary contains a separator).
func (m *Manager) detectBinary(spec ModuleSpec) (string, error) {
	name := spec.Install.Binary
	if name == "" {
		return "", fmt.Errorf("module %q: detect method needs a binary name", spec.ID)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		hint := spec.Install.Hint
		if hint == "" {
			hint = fmt.Sprintf("install %s and ensure it is on PATH", name)
		}
		return "", fmt.Errorf("module %q: %w — %s", spec.ID, err, hint)
	}
	return path, nil
}
