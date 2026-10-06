# Curated Market Index Signing

Track 125 (v0.16.0) adds the first-party curated provider index for the
market module: `stpinkie/rhizome-market-index`, a repo (not a service)
publishing `index.json` + `index.json.sig` — the module-catalog pattern
applied to market provider admission. `rhizome market find` resolves it
when `market_index_url` is set.

## Trust model

- The signing key is the same Ed25519 pair that signs the remote module
  catalog and the curated skills index: the public half is baked into
  `pkg/sigverify` (`ReleasePubKeyB64`), the private half lives only in the
  `MODULE_CATALOG_SIGNING_KEY` GitHub secret. One trust root, three signed
  documents. Operators running a private index can instead set
  `market_index_pubkey` to pin a different curator key — the module then
  verifies against that key and ignores the baked release key.
- `index.json.sig` is a detached base64 Ed25519 signature over the exact
  `index.json` bytes served.
- Unsigned, badly-signed, wrong-schema, or wrong-signer indexes are
  refused outright — no fallback, no warning mode. A *signature/schema*
  failure never serves the cache either (tamper evidence outranks
  availability — same posture as the skills index).
- Verified indexes cache in `<module_dir>/index-cache.json` (1 h TTL).
  When a *fetch* fails, the last verified cache is served whatever its
  age (`stale: true` on every row), with a `market.index.stale_serve`
  audit event.
- Two rollback rails beyond the signature:
  - `seq` — a monotonic publication counter. A fetched index whose `seq`
    is below the cached `seq` is refused and the cache serves stale
    (`market.index.seq_regressed` audit event).
  - `expires_at` — self-declared document expiry. Past the deadline the
    index still serves (availability) but every row carries `stale: true`
    and the fresh-cache shortcut is skipped so the next call refetches.
- The envelope's optional `signer` must equal the key that verified the
  signature — an index declaring a different curator than the one that
  signed it is refused (curator-confusion guard).

## Index document shape

```json
{
  "v": 1,
  "updated_at": "2026-10-06T12:00:00Z",
  "signer": "<base64 Ed25519 pubkey — must equal the verifying key>",
  "seq": 7,
  "expires_at": "2026-10-13T12:00:00Z",
  "providers": [
    {
      "peer_id": "12D3KooW…",
      "multiaddr": "/dns4/seller.example/tcp/443",
      "addrs": ["/dns4/seller.example/tcp/443", "/ip4/…"],
      "advert": { "…": "the provider's advert.json verbatim" },
      "attestations": [ { "kind": "runtime", "issuer": "curator" } ]
    }
  ]
}
```

- `v` — clients refuse anything but `1`.
- `providers[].advert` — the provider's signed `advert.json` blob
  (offers, payout, runtime posture, escrow posture). Verbatim; the module
  decodes it into rows.
- `multiaddr` / `addrs` — contact hints; `addrs` (list) is preferred when
  present, `multiaddr` is the legacy single. The first entry surfaces as
  the row's `multiaddr`.
- `attestations` — curator-signed claims, opaque to the client; each
  renders as `kind@issuer` on find rows. Track 130 (portable signed
  reputation) defines their wire signature shape.
- Bounds (enforced by `pkg/marketindex.Parse` on both client and publish
  side): 1 MiB document, 500 providers, 32 KiB per advert, 8 addrs, 16
  attestations, unique decodable peer_ids, RFC3339 timestamps, `v == 1`.

## Publish pipeline

The index repo's CI runs on tag pushes (validate → bump → sign → commit):

```yaml
# .github/workflows/publish.yml in stpinkie/rhizome-market-index
name: publish-index
on:
  push:
    tags: ["v*"]
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: stable }
      - name: Fetch rhizome tooling
        run: git clone --depth 1 https://github.com/stpinkie/rhizome /tmp/rhizome
      - name: Validate
        run: cd /tmp/rhizome && go run ./scripts/marketindex validate "$OLDPWD/index.json"
      - name: Bump seq + updated_at
        run: cd /tmp/rhizome && go run ./scripts/marketindex bump "$OLDPWD/index.json"
      - name: Sign
        env:
          MARKET_INDEX_SIGNING_KEY: ${{ secrets.MODULE_CATALOG_SIGNING_KEY }}
        run: cd /tmp/rhizome && go run ./scripts/marketindex sign "$OLDPWD/index.json"
      - name: Commit + push
        run: |
          git add index.json index.json.sig
          git -c user.name=market-index-bot -c user.email=ops@localhost \
            commit -m "publish index $(date -u +%Y%m%dT%H%M%SZ)"
          git push origin HEAD:main
```

`scripts/marketindex` (this repo) exposes `validate` (schema + bounds),
`bump` (`seq+1`, `updated_at=now`), and `sign` (refuses to sign a doc that
fails validation; reads `MARKET_INDEX_SIGNING_KEY`, falling back to
`MODULE_CATALOG_SIGNING_KEY`).

Every PR to the index repo should run `validate` before merge — a
malformed index must fail CI rather than publish.

## Admission policy (v1)

- **Single first-party curator.** Federation and community listing are
  deferred; the design question is answered by shipping one signer first.
- A provider lists by PR against the index repo carrying its
  `advert.json`. Admission checks (the curator verifies before merge):
  valid advert schema, reachable `addrs`, non-expired advert, declared
  escrow posture (`fixture` entries are listed but buy-side shows the
  posture honestly), and the peer_id matching the advert's signer once
  Track 130 lands.
- Removal is a delete + `seq` bump + sign — clients pick it up on the
  next TTL expiry; the seq guard prevents a hijacked mirror from serving
  an older, larger index.
- `market_index_url` stays unset-by-default until the index repo ships
  its first signed tag — the default flips at graduation (Track 132/136)
  so a 404ing default can't break `market find` today.

## Key custody, rotation, revocation

Same key as the module catalog — see
[module-catalog-signing.md](module-catalog-signing.md). Rotating the pair
changes `sigverify.ReleasePubKeyB64`; older binaries refuse indexes signed
by the new key, so rotation ships with a release. A dedicated curator key
(recommended once listing volume grows) is a different
`market_index_pubkey` operators opt into — the envelope `signer` field
pins which key is expected, so mixed-key deployments fail loudly instead
of silently trusting either.
