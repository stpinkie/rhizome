// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// remoteCatalogTTL bounds how long a verified remote catalog is trusted
// before a refetch is attempted. It is a variable so tests can shorten it.
var remoteCatalogTTL = time.Hour

// maxCatalogBytes caps remote catalog + signature fetches — a catalog is a
// small JSON document; anything bigger is a mistake or an attack.
const maxCatalogBytes = 4 << 20

// remoteClient is a short-timeout client for catalog fetches — m.client's
// 5-minute timeout is sized for binary downloads, not index metadata.
var remoteClient = &http.Client{Timeout: 15 * time.Second}

// catalogCacheDir returns <RHIZOME_HOME>/catalog-cache (m.root is
// <home>/modules).
func (m *Manager) catalogCacheDir() string {
	return filepath.Join(filepath.Dir(m.root), "catalog-cache")
}

// mergedEntry pairs a catalog spec with its provenance.
type mergedEntry struct {
	spec   ModuleSpec
	remote bool
}

// mergedEntries returns the effective catalog: the embedded set first, then
// verified remote entries whose ids do not collide (embedded always wins —
// the remote cannot shadow the trust-rooted set).
func (m *Manager) mergedEntries(ctx context.Context) []mergedEntry {
	entries := make([]mergedEntry, 0, len(catalog)+8)
	seen := make(map[string]bool, len(catalog)+8)
	for _, spec := range catalog {
		entries = append(entries, mergedEntry{spec: spec})
		seen[spec.ID] = true
	}
	remote := m.remoteSpecs(ctx)
	for _, spec := range remote {
		if seen[spec.ID] {
			continue
		}
		seen[spec.ID] = true
		entries = append(entries, mergedEntry{spec: spec, remote: true})
	}
	return entries
}

// specs returns the merged catalog (embedded + verified remote).
func (m *Manager) specs() []ModuleSpec {
	entries := m.mergedEntries(context.Background())
	out := make([]ModuleSpec, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.spec)
	}
	return out
}

// lookupSpec resolves a module id against the merged catalog; the second
// return is the provenance ("embedded"/"remote").
func (m *Manager) lookupSpec(id string) (ModuleSpec, string, bool) {
	for _, e := range m.mergedEntries(context.Background()) {
		if e.spec.ID == id {
			return e.spec, map[bool]string{true: "remote", false: "embedded"}[e.remote], true
		}
	}
	return ModuleSpec{}, "", false
}

// LookupSpec resolves a module id against the merged catalog — the exported
// form for callers outside the package (config validation).
func (m *Manager) LookupSpec(id string) (ModuleSpec, bool) {
	spec, _, ok := m.lookupSpec(id)
	return spec, ok
}

// remoteSpecs returns the verified remote catalog's modules, or nil when the
// index is disabled, unreachable, or untrusted. Trust posture:
//   - unsigned / bad-signature / wrong-schema content is hard-refused (and a
//     previously cached catalog is NOT served — tamper evidence outranks
//     availability);
//   - a plain fetch failure falls back to the last verified cache, whatever
//     its age (a signed catalog does not become unsafe because the network
//     is down), with a module.catalog.stale event.
func (m *Manager) remoteSpecs(ctx context.Context) []ModuleSpec {
	idx := m.cfg.ModuleIndex
	if !idx.Enabled || idx.URL == "" {
		return nil
	}
	if data, sig, ok := m.readCatalogCache(remoteCatalogTTL); ok {
		if specs, err := m.decodeVerifiedCatalog(data, sig); err == nil {
			return specs
		}
	}
	data, sig, err := m.fetchRemoteCatalog(ctx, idx.URL)
	if err != nil {
		m.publish("module.catalog.rejected", map[string]any{"reason": err.Error()})
		// Signature/schema failures refuse outright; transport failures may
		// serve the last verified cache.
		if !isTrustFailure(err) {
			if data, sig, ok := m.readCatalogCache(0); ok {
				if specs, decErr := m.decodeVerifiedCatalog(data, sig); decErr == nil {
					m.publish("module.catalog.stale", nil)
					return specs
				}
			}
		}
		return nil
	}
	specs, err := m.decodeVerifiedCatalog(data, sig)
	if err != nil {
		m.publish("module.catalog.rejected", map[string]any{"reason": err.Error()})
		return nil
	}
	m.writeCatalogCache(data, sig)
	shadowed := 0
	for _, spec := range specs {
		if _, ok := Lookup(spec.ID); ok {
			shadowed++
		}
	}
	m.publish("module.catalog.merged", map[string]any{
		"remote_total": len(specs),
		"merged":       len(specs) - shadowed,
		"shadowed":     shadowed,
	})
	return specs
}

// catalogTrustError marks catalog errors that mean "content is untrusted" —
// signature, schema, or scheme violations — as opposed to transport
// failures where a stale verified cache may still be served.
type catalogTrustError struct{ err error }

func (e catalogTrustError) Error() string { return e.err.Error() }
func (e catalogTrustError) Unwrap() error { return e.err }

func isTrustFailure(err error) bool {
	var tf catalogTrustError
	return errors.As(err, &tf)
}

// fetchRemoteCatalog downloads catalog.json + catalog.json.sig from the
// configured index URL. The URL must be HTTPS (loopback excepted, for
// tests); the raw catalog bytes are returned for byte-exact signature
// verification.
func (m *Manager) fetchRemoteCatalog(ctx context.Context, base string) (data, sig []byte, err error) {
	if !strings.HasPrefix(base, "https://") && !isLoopbackURL(base) {
		return nil, nil, catalogTrustError{fmt.Errorf("module_index.url must be HTTPS: %s", redactURL(base))}
	}
	base = strings.TrimSuffix(base, "/")
	if data, err = fetchBounded(ctx, base+"/catalog.json"); err != nil {
		return nil, nil, fmt.Errorf("catalog fetch: %w", err)
	}
	if sig, err = fetchBounded(ctx, base+"/catalog.json.sig"); err != nil {
		return nil, nil, fmt.Errorf("catalog signature fetch: %w", err)
	}
	return data, sig, nil
}

// fetchBounded GETs a URL into memory with a hard size cap.
func fetchBounded(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := remoteClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", redactURL(url), resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
}

// decodeVerifiedCatalog verifies sig over data and parses the envelope.
// sig is the base64 *text* of the signature, as carried in catalog.json.sig.
func (m *Manager) decodeVerifiedCatalog(data, sig []byte) ([]ModuleSpec, error) {
	if err := VerifyCatalogSignature(data, strings.TrimSpace(string(sig))); err != nil {
		return nil, catalogTrustError{err}
	}
	specs, err := parseCatalogEnvelope(data)
	if err != nil {
		return nil, catalogTrustError{err}
	}
	return specs, nil
}

// readCatalogCache returns cached catalog + signature bytes when they exist
// and are younger than maxAge (0 = any age — the stale-serve path).
func (m *Manager) readCatalogCache(maxAge time.Duration) (data, sig []byte, ok bool) {
	dir := m.catalogCacheDir()
	st, err := os.Stat(filepath.Join(dir, "catalog.json"))
	if err != nil {
		return nil, nil, false
	}
	if maxAge > 0 && time.Since(st.ModTime()) > maxAge {
		return nil, nil, false
	}
	//nolint:gosec // G304: dir is our own catalog-cache under RHIZOME_HOME.
	if data, err = os.ReadFile(filepath.Join(dir, "catalog.json")); err != nil {
		return nil, nil, false
	}
	//nolint:gosec // G304: dir is our own catalog-cache under RHIZOME_HOME.
	if sig, err = os.ReadFile(filepath.Join(dir, "catalog.json.sig")); err != nil {
		return nil, nil, false
	}
	return data, sig, true
}

// writeCatalogCache persists verified catalog + signature bytes.
func (m *Manager) writeCatalogCache(data, sig []byte) {
	dir := m.catalogCacheDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "catalog.json"), data, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "catalog.json.sig"), sig, 0o600)
}

// redactURL strips any userinfo and query from a URL for error/event text —
// index URLs may embed credentials or key-like path parameters.
func redactURL(raw string) string {
	if i := strings.Index(raw, "?"); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.Index(raw, "@"); i >= 0 {
		if j := strings.LastIndex(raw[:i], "//"); j >= 0 {
			raw = raw[:j+2] + "***@" + raw[i+1:]
		}
	}
	return raw
}
