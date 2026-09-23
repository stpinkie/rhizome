// Rhizome - Ultra-lightweight personal agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/sigverify"
	"github.com/stpinkie/rhizome/pkg/utils"
)

// defaultRhizomeIndexBaseURL is where releases publish the signed curated
// skills index — index.json + index.json.sig attached to each GitHub
// release, signed with the MODULE_CATALOG_SIGNING_KEY secret (see
// docs/operations/skill-index-signing.md).
const defaultRhizomeIndexBaseURL = "https://github.com/stpinkie/rhizome/releases/latest/download"

// skillIndexTTL bounds how long a verified index is trusted before a
// refetch is attempted. It is a variable so tests can shorten it.
var skillIndexTTL = time.Hour

// maxIndexBytes caps index + signature fetches — the index is a small JSON
// document; anything bigger is a mistake or an attack.
const maxIndexBytes = 4 << 20

// maxSkillArchiveBytes caps the GitHub .tar.gz download used by sha256-
// pinned entries — a whole-repository archive can be large.
const maxSkillArchiveBytes = 64 << 20

func init() {
	RegisterRegistryProviderBuilder("rhizome", func(_ string, cfg config.SkillRegistryConfig) RegistryProvider {
		privateCfg := rhizomeRegistryPrivateConfig{}
		if err := cfg.DecodeParam(&privateCfg); err != nil {
			slog.Warn("invalid rhizome registry private config", "error", err)
		}
		return RhizomeRegistryConfig{
			Enabled:         cfg.Enabled,
			BaseURL:         cfg.BaseURL,
			AuthToken:       cfg.AuthToken.String(),
			Proxy:           privateCfg.Proxy,
			CacheTTLSeconds: privateCfg.CacheTTLSeconds,
		}
	})
}

// rhizomeRegistryPrivateConfig carries the rhizome registry's non-secret
// param keys. `proxy` applies to index fetches and GitHub source downloads
// alike; `auth_token` (SecureString on the parent config) is the GitHub
// token used for source resolution.
type rhizomeRegistryPrivateConfig struct {
	Proxy           string `json:"proxy"`
	CacheTTLSeconds int    `json:"cache_ttl_seconds"`
}

// RhizomeRegistryConfig configures the first-party curated registry.
type RhizomeRegistryConfig struct {
	Enabled         bool
	BaseURL         string // serves index.json + index.json.sig
	AuthToken       string // GitHub token for source downloads
	Proxy           string
	CacheTTLSeconds int
}

func (c RhizomeRegistryConfig) IsEnabled() bool { return c.Enabled }

func (c RhizomeRegistryConfig) BuildRegistry() SkillRegistry {
	installer, err := NewSkillInstallerWithBaseURL("", "", c.AuthToken, c.Proxy)
	if err != nil {
		slog.Warn("failed to create rhizome registry installer", "error", err)
		return nil
	}
	client, err := utils.CreateHTTPClient(c.Proxy, 60*time.Second)
	if err != nil {
		slog.Warn("failed to create rhizome registry client", "error", err)
		return nil
	}
	ttl := skillIndexTTL
	if c.CacheTTLSeconds > 0 {
		ttl = time.Duration(c.CacheTTLSeconds) * time.Second
	}
	return &RhizomeRegistry{
		baseURL:   strings.TrimRight(strings.TrimSpace(c.BaseURL), "/"),
		installer: installer,
		client:    client,
		ttl:       ttl,
	}
}

// RhizomeRegistry serves the first-party curated skills index. Trust
// posture mirrors the remote module catalog:
//   - unsigned / bad-signature / malformed content is hard-refused (and a
//     previously cached index is NOT served — tamper evidence outranks
//     availability);
//   - a plain fetch failure falls back to the last verified cache, whatever
//     its age, with a skill.index.stale event.
type RhizomeRegistry struct {
	baseURL   string
	installer *SkillInstaller
	client    *http.Client
	ttl       time.Duration
	cacheDir  string // "" → <RHIZOME_HOME>/skills-index-cache
	bus       events.Bus

	mu        sync.Mutex
	index     *skillIndex
	indexTime time.Time
}

// SetEventBus attaches the daemon's runtime event bus for skill.index.*
// events. Nil-safe: daemonless callers leave the bus unset and warnings go
// to the log only.
func (r *RhizomeRegistry) SetEventBus(bus events.Bus) { r.bus = bus }

func (r *RhizomeRegistry) publish(kind string, attrs map[string]any) {
	if r.bus == nil {
		return
	}
	r.bus.PublishNonBlocking(events.Event{
		Kind:     events.Kind(kind),
		Source:   events.Source{Component: "skills"},
		Severity: events.SeverityInfo,
		Attrs:    attrs,
	})
}

func (r *RhizomeRegistry) reject(err error) {
	slog.Warn("curated skills index rejected", "error", err)
	r.publish("skill.index.rejected", map[string]any{"reason": err.Error()})
}

func (r *RhizomeRegistry) Name() string { return "rhizome" }

func (r *RhizomeRegistry) ResolveInstallDirName(target string) (string, error) {
	if err := utils.ValidateSkillIdentifier(target); err != nil {
		return "", err
	}
	return target, nil
}

func (r *RhizomeRegistry) SkillURL(slug, _ string) string {
	idx := r.cachedIndex()
	if idx == nil {
		return ""
	}
	e, ok := idx.bySlug[slug]
	if !ok {
		return ""
	}
	target, ref, err := parseCuratedGitHubSource(e.Source.GitHub)
	if err != nil {
		return ""
	}
	version := ref
	if version == "" {
		version = e.Version
	}
	gh := &GitHubRegistry{webBase: r.installer.githubBaseURL}
	return gh.SkillURL(target, version)
}

func (r *RhizomeRegistry) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	idx, err := r.loadIndex(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 5
	}
	results := make([]SearchResult, 0, len(idx.entries))
	for _, e := range idx.entries {
		score, ok := curatedMatchScore(query, e)
		if !ok {
			continue
		}
		results = append(results, SearchResult{
			Score:        score,
			Slug:         e.Slug,
			DisplayName:  e.DisplayName,
			Summary:      e.Summary,
			Version:      e.Version,
			RegistryName: r.Name(),
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Slug < results[j].Slug
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (r *RhizomeRegistry) GetSkillMeta(ctx context.Context, slug string) (*SkillMeta, error) {
	e, err := r.lookupEntry(ctx, slug)
	if err != nil {
		return nil, err
	}
	return &SkillMeta{
		Slug:          e.Slug,
		DisplayName:   e.DisplayName,
		Summary:       e.Summary,
		LatestVersion: e.Version,
		RegistryName:  r.Name(),
	}, nil
}

// DownloadAndInstall resolves the entry's source.github through the
// existing GitHub installer path — content moderation (malware/suspicious
// flags), skill-directory validation, and provenance writing all remain the
// caller's job, exactly as for other registries. A caller-supplied version
// that disagrees with the index-pinned version is refused: the curated
// index's contract is "install what the signed index declares".
func (r *RhizomeRegistry) DownloadAndInstall(
	ctx context.Context,
	slug, version, targetDir string,
) (*InstallResult, error) {
	e, err := r.lookupEntry(ctx, slug)
	if err != nil {
		return nil, err
	}
	if version != "" && e.Version != "" && version != e.Version {
		return nil, fmt.Errorf(
			"curated index pins %s at version %s (requested %s)", slug, e.Version, version)
	}
	target, ref, err := parseCuratedGitHubSource(e.Source.GitHub)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		ref = e.Version
	}

	var res *InstallResult
	if e.SHA256 != "" {
		res, err = r.installPinned(ctx, target, ref, e.SHA256, targetDir)
	} else {
		res, err = r.installer.InstallFromGitHubToDir(ctx, target, ref, targetDir)
	}
	if err != nil {
		return nil, err
	}
	res.Summary = e.Summary
	if res.Version == "" {
		res.Version = e.Version
	}
	return res, nil
}

func (r *RhizomeRegistry) lookupEntry(ctx context.Context, slug string) (skillIndexEntry, error) {
	if err := utils.ValidateSkillIdentifier(slug); err != nil {
		return skillIndexEntry{}, fmt.Errorf("invalid slug %q: %w", slug, err)
	}
	idx, err := r.loadIndex(ctx)
	if err != nil {
		return skillIndexEntry{}, err
	}
	e, ok := idx.bySlug[slug]
	if !ok {
		return skillIndexEntry{}, fmt.Errorf("skill %q is not in the curated index", slug)
	}
	return e, nil
}

// curatedMatchScore scores an entry against a free-text query. Every query
// token must hit somewhere; slug hits outrank display-name hits, which
// outrank summary hits. An empty query returns every entry (browse mode).
func curatedMatchScore(query string, e skillIndexEntry) (float64, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 1.0, true
	}
	slug := strings.ToLower(e.Slug)
	name := strings.ToLower(e.DisplayName)
	summary := strings.ToLower(e.Summary)
	score := 0.0
	for _, tok := range strings.Fields(query) {
		switch {
		case strings.Contains(slug, tok):
			score += 1.0
		case strings.Contains(name, tok):
			score += 0.6
		case strings.Contains(summary, tok):
			score += 0.3
		default:
			return 0, false
		}
	}
	return score, true
}

// ── Index fetch / verify / cache ─────────────────────────────────────────

// indexTrustError marks index errors that mean "content is untrusted" —
// signature, schema, or scheme violations — as opposed to transport
// failures where a stale verified cache may still be served.
type indexTrustError struct{ err error }

func (e indexTrustError) Error() string { return e.err.Error() }
func (e indexTrustError) Unwrap() error { return e.err }

func isIndexTrustFailure(err error) bool {
	var tf indexTrustError
	return errors.As(err, &tf)
}

// loadIndex returns the verified index, consulting in-memory and disk
// caches before fetching. Fetch failures may serve the stale verified
// cache; signature/schema failures never do.
func (r *RhizomeRegistry) loadIndex(ctx context.Context) (*skillIndex, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.index != nil && time.Since(r.indexTime) < r.ttl {
		return r.index, nil
	}
	if data, sig, ok := r.readIndexCache(r.ttl); ok {
		if idx, err := r.decodeVerifiedIndex(data, sig); err == nil {
			r.index, r.indexTime = idx, time.Now()
			return idx, nil
		}
	}
	data, sig, err := r.fetchIndex(ctx)
	if err != nil {
		r.reject(err)
		if !isIndexTrustFailure(err) {
			if data, sig, ok := r.readIndexCache(0); ok {
				if idx, decErr := r.decodeVerifiedIndex(data, sig); decErr == nil {
					r.publish("skill.index.stale", nil)
					r.index, r.indexTime = idx, time.Now()
					return idx, nil
				}
			}
		}
		return nil, err
	}
	idx, err := r.decodeVerifiedIndex(data, sig)
	if err != nil {
		r.reject(err)
		return nil, err
	}
	r.writeIndexCache(data, sig)
	r.publish("skill.index.merged", map[string]any{"skills": len(idx.entries)})
	r.index, r.indexTime = idx, time.Now()
	return idx, nil
}

// cachedIndex returns the last verified index without touching the network
// — for SkillURL, which runs after install when the index is already warm.
func (r *RhizomeRegistry) cachedIndex() *skillIndex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.index != nil {
		return r.index
	}
	if data, sig, ok := r.readIndexCache(r.ttl); ok {
		if idx, err := r.decodeVerifiedIndex(data, sig); err == nil {
			r.index, r.indexTime = idx, time.Now()
			return idx
		}
	}
	return nil
}

// indexBaseURL resolves the configured or default index base.
func (r *RhizomeRegistry) indexBaseURL() string {
	if r.baseURL != "" {
		return r.baseURL
	}
	return defaultRhizomeIndexBaseURL
}

// fetchIndex downloads index.json + index.json.sig. The base URL must be
// HTTPS (loopback excepted, for tests); raw index bytes are returned for
// byte-exact signature verification.
func (r *RhizomeRegistry) fetchIndex(ctx context.Context) (data, sig []byte, err error) {
	base := r.indexBaseURL()
	if !strings.HasPrefix(base, "https://") && !utils.IsLoopbackURL(base) {
		return nil, nil, indexTrustError{
			fmt.Errorf("skills index url must be HTTPS: %s", redactIndexURL(base)),
		}
	}
	base = strings.TrimSuffix(base, "/")
	if data, err = r.fetchBounded(ctx, base+"/index.json", maxIndexBytes); err != nil {
		return nil, nil, fmt.Errorf("index fetch: %w", err)
	}
	if sig, err = r.fetchBounded(ctx, base+"/index.json.sig", maxIndexBytes); err != nil {
		return nil, nil, fmt.Errorf("index signature fetch: %w", err)
	}
	return data, sig, nil
}

func (r *RhizomeRegistry) fetchBounded(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", redactIndexURL(rawURL), resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%s: exceeds %d-byte cap", redactIndexURL(rawURL), maxBytes)
	}
	return body, nil
}

// decodeVerifiedIndex verifies sig over data and parses the envelope.
func (r *RhizomeRegistry) decodeVerifiedIndex(data, sig []byte) (*skillIndex, error) {
	if err := sigverify.VerifyReleaseSignature(data, strings.TrimSpace(string(sig))); err != nil {
		return nil, indexTrustError{err}
	}
	idx, err := parseSkillIndex(data, r.installer.githubBaseURL)
	if err != nil {
		return nil, indexTrustError{err}
	}
	return idx, nil
}

func (r *RhizomeRegistry) indexCacheDir() string {
	if r.cacheDir != "" {
		return r.cacheDir
	}
	return filepath.Join(config.GetHome(), "skills-index-cache")
}

// readIndexCache returns cached index + signature bytes when they exist
// and are younger than maxAge (0 = any age — the stale-serve path).
func (r *RhizomeRegistry) readIndexCache(maxAge time.Duration) (data, sig []byte, ok bool) {
	dir := r.indexCacheDir()
	st, err := os.Stat(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil, nil, false
	}
	if maxAge > 0 && time.Since(st.ModTime()) > maxAge {
		return nil, nil, false
	}
	//nolint:gosec // G304: dir is our own skills-index-cache under RHIZOME_HOME.
	if data, err = os.ReadFile(filepath.Join(dir, "index.json")); err != nil {
		return nil, nil, false
	}
	//nolint:gosec // G304: dir is our own skills-index-cache under RHIZOME_HOME.
	if sig, err = os.ReadFile(filepath.Join(dir, "index.json.sig")); err != nil {
		return nil, nil, false
	}
	return data, sig, true
}

// writeIndexCache persists verified index + signature bytes.
func (r *RhizomeRegistry) writeIndexCache(data, sig []byte) {
	dir := r.indexCacheDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "index.json"), data, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "index.json.sig"), sig, 0o600)
}

// redactIndexURL strips userinfo and query from a URL for error/event text —
// index URLs may embed credentials or key-like path parameters.
func redactIndexURL(raw string) string {
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

// ── sha256-pinned install ────────────────────────────────────────────────

// installPinned downloads the GitHub auto-generated .tar.gz archive for the
// resolved ref, verifies it against the index-pinned sha256, and extracts
// the declared subpath into targetDir. A digest mismatch refuses the
// install outright — the pin is the curated entry's integrity contract.
func (r *RhizomeRegistry) installPinned(
	ctx context.Context,
	target, ref, wantSHA, targetDir string,
) (*InstallResult, error) {
	resolved, err := r.installer.resolveGitHubTarget(ctx, target, ref)
	if err != nil {
		return nil, err
	}
	// Refs may contain slashes (feature/foo); GitHub resolves the archive
	// endpoint by longest ref match, so the raw ref goes in unescaped.
	archiveURL := fmt.Sprintf("%s/%s/%s/archive/%s.tar.gz",
		strings.TrimRight(resolved.Endpoints.WebBaseURL, "/"),
		resolved.Ref.Owner, resolved.Ref.RepoName, resolved.Ref.Ref)
	body, err := r.fetchBounded(ctx, archiveURL, maxSkillArchiveBytes)
	if err != nil {
		return nil, fmt.Errorf("pinned archive fetch: %w", err)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != normalizeSHA256Pin(wantSHA) {
		return nil, fmt.Errorf(
			"curated bundle sha256 mismatch for %s@%s", target, resolved.Ref.Ref)
	}
	subPath := strings.Trim(resolved.Ref.SubPath, "/")
	if isSkillMarkdownPath(subPath) {
		if dir := path.Dir(subPath); dir == "." {
			subPath = ""
		} else {
			subPath = dir
		}
	}
	if err := extractSkillTarGz(body, subPath, targetDir); err != nil {
		return nil, err
	}
	return &InstallResult{Version: resolved.Ref.Ref}, nil
}

// extractSkillTarGz unpacks a GitHub repo tarball: the archive's top-level
// directory is stripped, only members under subPath are kept, and SKILL.md
// must land at the skill root. Path traversal, symlink members, oversized
// files, and over-large file counts are refused — same guards as
// UnpackSkillBundle.
func extractSkillTarGz(archive []byte, subPath, destDir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("invalid gzip archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	subPath = strings.Trim(subPath, "/")
	destClean := filepath.Clean(destDir) + string(filepath.Separator)
	fileCount := 0
	hasSkillMD := false

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("archive read: %w", err)
		}
		name := filepath.ToSlash(filepath.Clean(hdr.Name))
		if name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
			return fmt.Errorf("archive contains unsafe path %q", hdr.Name)
		}
		// Strip the leading "<repo>-<ref>/" directory GitHub adds.
		i := strings.Index(name, "/")
		if i < 0 {
			continue
		}
		rel := name[i+1:]
		if subPath != "" {
			if rel == subPath {
				continue
			}
			if !strings.HasPrefix(rel, subPath+"/") {
				continue
			}
			rel = rel[len(subPath)+1:]
		}
		if rel == "" {
			continue
		}
		target := filepath.Join(destDir, filepath.FromSlash(rel))
		if !strings.HasPrefix(filepath.Clean(target)+string(filepath.Separator), destClean) {
			return fmt.Errorf("archive member %q escapes target dir", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if hdr.Size > maxSkillBundleBytes {
				return fmt.Errorf("archive member %q exceeds %d-byte cap", hdr.Name, maxSkillBundleBytes)
			}
			fileCount++
			if fileCount > maxSkillFiles {
				return fmt.Errorf("archive has too many files (%d cap)", maxSkillFiles)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := hdr.FileInfo().Mode().Perm()
			if mode == 0 {
				mode = 0o644
			}
			//nolint:gosec // G304: target is validated to stay under destDir above.
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.CopyN(out, tr, maxSkillBundleBytes+1); err != nil && err != io.EOF {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
			if rel == "SKILL.md" {
				hasSkillMD = true
			}
		default:
			// Symlinks and other special members are refused outright.
			return fmt.Errorf("archive member %q has unsupported type %d", hdr.Name, hdr.Typeflag)
		}
	}
	if !hasSkillMD {
		return fmt.Errorf("archive does not contain SKILL.md at the skill root")
	}
	return nil
}
