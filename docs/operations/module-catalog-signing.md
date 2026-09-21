# Module Catalog Signing

Track 70 (v0.11.0) adds an optional remote module catalog. Release
artifacts include a signed `catalog.json` (+ `catalog.json.sig`) so an
operator can point `module_index.url` at the published catalog and merge
extra curated modules with the embedded set.

## Trust model

- The embedded catalog (compiled into the binary) is the trust root. It
  needs no signature — it ships inside the signed release artifact.
- A remote catalog is JSON + a detached base64 Ed25519 signature over the
  exact bytes served. The verifying public key is baked into
  `pkg/modules/catalog.go` (`releasePubKeyB64`).
- Unsigned, badly-signed, or wrong-schema catalogs are refused outright —
  no fallback, no warning mode.
- On an ID collision the embedded entry wins; the remote cannot shadow the
  trust-rooted set. `module list --json` reports `source: embedded|remote`
  per module.
- Verified catalogs cache under `<RHIZOME_HOME>/catalog-cache/` (1 h TTL).
  When a *fetch* fails, the last verified cache is served whatever its age
  (a signed catalog does not become unsafe because the network is down);
  a *signature/schema* failure never falls back.

## Configuration

```json
"module_index": { "enabled": true, "url": "https://…/path/" }
```

`module_index` is a sibling of `modules`, not a key inside it — the modules
section is a map keyed by module id, so a `catalog_url` key inside it would
decode as a module. The URL must be HTTPS and serves `catalog.json` +
`catalog.json.sig` relative to it.

## Events

- `module.catalog.merged` — once per successful fetch+verify (not per
  `module list`), with `remote_total`/`merged`/`shadowed` counts.
- `module.catalog.rejected` — a fetched or cached catalog failed trust
  checks (`reason` field).
- `module.catalog.stale` — fetch failed, serving the expired verified cache.
- `module.verify.ok` / `module.verify.failed` — `module verify` outcomes.

## Release signing

`release.yml` emits and signs the catalog when the
`MODULE_CATALOG_SIGNING_KEY` secret exists (base64 Ed25519 seed; same
secret-gate pattern as Docker Hub creds — absent → step skipped):

```sh
go run -tags goolm,stdjson ./cmd/rhizome module catalog \
  --out catalog.json --sign-with-env MODULE_CATALOG_SIGNING_KEY
```

`--sign-with-env` names the *env var* holding the seed so the key never
appears in argv or shell history. The step uploads both files to the
release with `gh release upload … --clobber`.

## Key generation & rotation

```sh
rhizome module catalog-keygen   # hidden command
```

- Bake `public:` into `releasePubKeyB64` in `pkg/modules/catalog.go`.
- Store `private:` as the `MODULE_CATALOG_SIGNING_KEY` repo secret
  (`gh secret set … < seed.txt` — via stdin, never argv).
- The seed must not be committed, logged, or pasted into chat.

Rotating: generate a new pair, swap the baked-in pubkey, cut a release —
binaries older than the release that changes the key will refuse catalogs
signed by the new key (expected; catalogs are forward-compatible only
within `catalog_version`).

## `module verify`

`rhizome module verify <id>` re-hashes the installed binary against the
sha256 recorded at install time (`<module>/<version>/.binary-digest`),
which install produced after checking the catalog's artifact digest pin.
A mismatch means post-install drift — corruption or tampering. Modules
installed before verification support have no record; reinstall them to
enable verification.
