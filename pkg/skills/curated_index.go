// Rhizome - Ultra-lightweight personal agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package skills

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/utils"
)

// skillIndexVersionSupported is the highest curated-index schema version
// this binary understands. Indexes carrying a newer version are refused so
// an old binary never silently misreads a newer wire shape.
const skillIndexVersionSupported = 1

// skillIndexEnvelope is the signed curated-index wire format served at
// <registries.rhizome.base_url>/index.json.
type skillIndexEnvelope struct {
	IndexVersion int               `json:"index_version"`
	GeneratedAt  string            `json:"generated_at,omitempty"`
	Skills       []skillIndexEntry `json:"skills"`
}

// skillIndexEntry is one curated skill. source.github resolves through the
// existing GitHub installer path; sha256 optionally pins the GitHub
// auto-generated .tar.gz archive for the resolved ref.
type skillIndexEntry struct {
	Slug        string           `json:"slug"`
	DisplayName string           `json:"display_name"`
	Summary     string           `json:"summary"`
	Version     string           `json:"version"`
	Source      skillIndexSource `json:"source"`
	SHA256      string           `json:"sha256,omitempty"`
}

type skillIndexSource struct {
	GitHub string `json:"github"`
}

// skillIndex is the parsed, validated index with a slug lookup map.
type skillIndex struct {
	entries []skillIndexEntry
	bySlug  map[string]skillIndexEntry
}

//go:embed curated_index.json
var curatedIndexJSON []byte

// MarshalCuratedIndex renders the embedded curated index in its canonical
// signed form — the exact bytes release signing covers (mirrors
// modules.MarshalCatalog). The embedded content is validated before it is
// emitted so a malformed edit fails here and in tests, not at clients.
func MarshalCuratedIndex() ([]byte, error) {
	var env skillIndexEnvelope
	if err := json.Unmarshal(curatedIndexJSON, &env); err != nil {
		return nil, fmt.Errorf("embedded skills index: %w", err)
	}
	env.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, err
	}
	if _, err := parseSkillIndex(data, ""); err != nil {
		return nil, fmt.Errorf("embedded skills index fails validation: %w", err)
	}
	return data, nil
}

// parseSkillIndex unmarshals and sanity-checks signed index bytes.
// githubBaseURL is the registry's configured GitHub base ("" = github.com);
// sources must resolve against it.
func parseSkillIndex(data []byte, githubBaseURL string) (*skillIndex, error) {
	var env skillIndexEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("index JSON: %w", err)
	}
	if env.IndexVersion < 1 || env.IndexVersion > skillIndexVersionSupported {
		return nil, fmt.Errorf(
			"unsupported index_version %d (this build understands <= %d)",
			env.IndexVersion, skillIndexVersionSupported)
	}
	idx := &skillIndex{bySlug: map[string]skillIndexEntry{}}
	for i, e := range env.Skills {
		if err := validateIndexEntry(e, githubBaseURL); err != nil {
			return nil, fmt.Errorf("index entry %d (%q): %w", i, e.Slug, err)
		}
		if _, dup := idx.bySlug[e.Slug]; dup {
			return nil, fmt.Errorf("duplicate slug %q", e.Slug)
		}
		idx.entries = append(idx.entries, e)
		idx.bySlug[e.Slug] = e
	}
	return idx, nil
}

func validateIndexEntry(e skillIndexEntry, githubBaseURL string) error {
	if err := utils.ValidateSkillIdentifier(e.Slug); err != nil {
		return fmt.Errorf("slug: %w", err)
	}
	target, _, err := parseCuratedGitHubSource(e.Source.GitHub)
	if err != nil {
		return fmt.Errorf("source.github: %w", err)
	}
	if _, err := parseGitHubTargetWithBaseURL(target, githubBaseURL, "main"); err != nil {
		return fmt.Errorf("source.github: %w", err)
	}
	if e.SHA256 != "" {
		sum := normalizeSHA256Pin(e.SHA256)
		if len(sum) != sha256.Size*2 {
			return fmt.Errorf("sha256: want %d hex chars, got %d", sha256.Size*2, len(sum))
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return fmt.Errorf("sha256: %w", err)
		}
	}
	return nil
}

// normalizeSHA256Pin strips an optional "sha256:" algorithm prefix and
// lowercases the hex for comparison.
func normalizeSHA256Pin(pin string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(pin), "sha256:"))
}

// parseCuratedGitHubSource splits the index grammar
// "owner/repo[@ref][/path]" into an installer target ("owner/repo[/path]")
// and an optional ref. Full https://github.com/... URLs pass through
// unchanged — the installer's own parser resolves their /tree/|/blob/ refs.
func parseCuratedGitHubSource(raw string) (target, ref string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("empty github source")
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw, "", nil
	}
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		return "", "", fmt.Errorf("expected owner/repo[@ref][/path], got %q", raw)
	}
	if strings.Contains(parts[0], "@") {
		return "", "", fmt.Errorf("invalid owner %q", parts[0])
	}
	if i := strings.Index(parts[1], "@"); i >= 0 {
		ref = parts[1][i+1:]
		parts[1] = parts[1][:i]
		if parts[1] == "" || ref == "" {
			return "", "", fmt.Errorf("malformed ref in %q", raw)
		}
	}
	return strings.Join(parts, "/"), ref, nil
}
