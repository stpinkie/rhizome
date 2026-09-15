// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// installRelease downloads the pinned release for the current platform,
// verifies its sha256, and extracts it under <modules>/<id>/<version>/.
func (m *Manager) installRelease(ctx context.Context, spec ModuleSpec, version string) error {
	release, ok := spec.Release(version)
	if !ok {
		return fmt.Errorf("module %q: no pinned release for version %q", spec.ID, version)
	}
	platform := Platform()
	want, ok := release.SHA256[platform]
	if !ok || want == "" {
		return fmt.Errorf(
			"module %q v%s has no sha256 for %s — refusing to install",
			spec.ID, release.Version, platform,
		)
	}
	url := spec.DownloadURL(release)
	if !strings.HasPrefix(url, "https://") && !isLoopbackURL(url) {
		return fmt.Errorf("module %q: download URL is not HTTPS: %s", spec.ID, url)
	}

	tmp, err := os.CreateTemp("", "rhizome-module-*.tar.gz")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	sum := sha256.New()
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
			"module %q v%s: sha256 mismatch — got %s, want %s (refusing to install)",
			spec.ID, release.Version, got, want,
		)
	}

	dest := filepath.Join(m.Dir(spec.ID), release.Version)
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	if err := extractTarGz(tmpPath, dest, spec.Install.Binary); err != nil {
		_ = os.RemoveAll(dest)
		return fmt.Errorf("module %q: extract failed: %w", spec.ID, err)
	}
	return m.markInstalled(spec.ID, release.Version)
}

// isLoopbackURL reports whether a URL targets loopback — the one case where
// plain HTTP is acceptable (local test servers, development registries).
func isLoopbackURL(url string) bool {
	return strings.HasPrefix(url, "http://127.0.0.1") ||
		strings.HasPrefix(url, "http://localhost") ||
		strings.HasPrefix(url, "http://[::1]")
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

// extractTarGz unpacks a .tar.gz into dest. It finds the module binary by
// basename (allowing archives that wrap contents in a top-level directory),
// marks it executable, and refuses path-traversal entries.
func extractTarGz(archivePath, dest, binaryName string) error {
	//nolint:gosec // G304: archivePath is the download temp file just written.
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()

	wantBase := binaryName
	tr := tar.NewReader(gz)
	foundBinary := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Reject anything that would escape dest.
		name := filepath.Clean(hdr.Name)
		if name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) ||
			filepath.IsAbs(name) {
			return fmt.Errorf("archive contains unsafe path %q", hdr.Name)
		}
		target := filepath.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if hdr.Size > maxMemberBytes {
				return fmt.Errorf("archive member %q exceeds %d-byte cap", hdr.Name, maxMemberBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			mode := hdr.FileInfo().Mode().Perm()
			if mode == 0 {
				mode = 0o600
			}
			//nolint:gosec // G304: target is validated to stay under dest above.
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			n, err := io.CopyN(out, tr, maxMemberBytes+1)
			if err != nil && err != io.EOF {
				_ = out.Close()
				return err
			}
			if n > maxMemberBytes {
				_ = out.Close()
				return fmt.Errorf("archive member %q exceeds %d-byte cap", hdr.Name, maxMemberBytes)
			}
			if err := out.Close(); err != nil {
				return err
			}
			base := filepath.Base(name)
			if base == wantBase || base == wantBase+".exe" {
				//nolint:gosec // G302: the module binary must be executable.
				_ = os.Chmod(target, 0o755)
				foundBinary = true
			}
		}
	}
	if wantBase != "" && !foundBinary {
		return fmt.Errorf("archive does not contain binary %q", wantBase)
	}
	return nil
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
