# Curated Skills Index Signing

Track 90 (v0.13.0) adds the first-party `rhizome` skills registry: a signed,
curated index of skills published as release assets. Clients fetch
`index.json` + `index.json.sig`, verify the Ed25519 signature against the
baked-in release key, and install entries through the existing GitHub
installer path.

## Trust model

- The signing key is the same Ed25519 pair that signs the remote module
  catalog: the public half is baked into `pkg/sigverify`
  (`ReleasePubKeyB64`), the private half lives only in the
  `MODULE_CATALOG_SIGNING_KEY` GitHub secret. One trust root, two signed
  documents.
- `index.json.sig` is a detached base64 Ed25519 signature over the exact
  `index.json` bytes served.
- Unsigned, badly-signed, or wrong-schema indexes are refused outright —
  no fallback, no warning mode. A *signature/schema* failure never serves
  the cache either (tamper evidence outranks availability).
- Verified indexes cache under `<RHIZOME_HOME>/skills-index-cache/` (1 h
  TTL). When a *fetch* fails, the last verified cache is served whatever
  its age (a signed index does not become unsafe because the network is
  down), with a `skill.index.stale` event.
- Index entries install via `source.github` through the same GitHub
  installer the `github` registry uses — guard scans, skill-directory
  validation, and `.skill-origin.json` provenance are unchanged. Curated
  installs record `origin_kind: "curated"` (the web UI shows a teal
  Curated badge).
- The curated contract is "install what the signed index declares": a
  caller-supplied version that disagrees with the index entry is refused.

## Index entry shape

```json
{
  "slug": "summarize",
  "display_name": "Summarize",
  "summary": "Summarize or extract text/transcripts from URLs, podcasts, and local files.",
  "version": "1.0.0",
  "source": { "github": "stpinkie/rhizome@main/workspace/skills/summarize" },
  "sha256": "…optional…"
}
```

- `source.github` — `owner/repo[@ref][/path]` or a full
  `https://github.com/...` URL. `@ref` pins the git ref; when absent the
  entry `version` is tried as the ref, then the repo default branch.
- `version` — the display/pinned version label. Also used as the git ref
  when the source carries no `@ref`.
- `sha256` — optional. When present it pins the **GitHub auto-generated
  `.tar.gz` archive** (`<repo>/archive/<ref>.tar.gz`) for the resolved ref.
  Only pin against immutable refs (commit SHAs, tags) — GitHub can
  regenerate archives for moving refs like `main`, which would break the
  pin. A mismatch refuses the install outright.
- Top level: `{"index_version": 1, "generated_at": "…", "skills": […]}`.
  Clients refuse `index_version` values they do not understand.

## Configuration

The `rhizome` registry ships enabled by default:

```json
"tools": { "skills": { "registries": [
  { "name": "rhizome", "enabled": true,
    "base_url": "https://github.com/stpinkie/rhizome/releases/latest/download" }
] } }
```

`base_url` must be HTTPS and serves `index.json` + `index.json.sig`
relative to it. Useful params: `param.proxy` (index fetch + GitHub
downloads), `param.cache_ttl_seconds` (default 3600), and `auth_token`
(GitHub token for source downloads — falls back to
`tools.skills.github.token`, as does the proxy).

## Events

- `skill.index.merged` — once per successful fetch+verify, with the
  `skills` entry count.
- `skill.index.rejected` — a fetched index failed trust checks (`reason`
  field) or could not be fetched.
- `skill.index.stale` — fetch failed, serving the expired verified cache.

## Release signing

`release.yml` emits and signs the index when the
`MODULE_CATALOG_SIGNING_KEY` secret exists (same secret gate as the module
catalog):

```sh
go run -tags goolm,stdjson ./cmd/rhizome skills index \
  --out index.json --sign-with-env MODULE_CATALOG_SIGNING_KEY
```

The embedded index source lives in `pkg/skills/curated_index.json`; the
emit command re-validates it (`MarshalCuratedIndex`) before writing, so a
malformed edit fails CI rather than shipping. The step uploads both files
to the release with `gh release upload … --clobber`.

Until the first release that ships these assets, `releases/latest/download`
returns 404 — the registry treats it as a fetch failure: warn, no cached
results, other registries unaffected.

## Key rotation

Same key as the module catalog — see
[module-catalog-signing.md](module-catalog-signing.md). Rotating the pair
changes `sigverify.ReleasePubKeyB64`; older binaries refuse indexes signed
by the new key.
