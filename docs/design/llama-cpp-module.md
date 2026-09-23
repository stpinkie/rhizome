# llama.cpp companion module — design

Status: **design record, not scheduled.** Queued behind v0.13.0 Track 95
(the catalog trust tail this depends on) and the module-docs linkage in
Track 93. This page records the shape so implementation can start from a
decision, not a survey.

A `llama-cpp` module would ship `llama-server` — the OpenAI-compatible
HTTP server from `ggml-org/llama.cpp` — as a local inference backend for
Rhizome agents, installed and supervised like `ipfs-kubo` rather than
built into the base binary.

## Why a module (and why ondemand)

llama.cpp releases are large per-platform archives that change weekly,
and most operators will never run local inference — exactly the "extend
without growing the base binary" case the module system exists for.

Kind: **`ondemand`**. The server is spawned when a consumer needs it —
a configured `llm` provider entry pointing at the module, or the
operator running `rhizome module start llama-cpp` — not at daemon boot.
`llama-server` is a long-running HTTP service once up, so `run` keeps a
normal `args_template` + `health` probe (`http` GET `/health`), and the
supervisor treats exit as final for the request that spawned it.

## Extraction needs — layout preservation

Current extractors (Track 71) flatten the named binary to the version-dir
root and keep every other member at its archive-relative path. llama.cpp
archives (`llama-b<build>-bin-{win,ubuntu,macos}-<variant>-<arch>.zip|.tar.gz`)
are **multi-binary + shared-library layouts**: `llama-server`,
`llama-cli`, `llama-quantize`, … alongside `ggml*.dll|libggml*.so|*.dylib`
and backend-plugin libs.

- **Flattening must not break the loader.** If the shared libs live at
  the archive root next to the binaries, today's behavior already works:
  the flattened binary lands beside its libs. If a release variant nests
  them (`bin/`, `lib/`), the binary must keep its archive-relative
  location so the loader's relative search path resolves.
- Proposed catalog field: `install.binary_path` — when set, extraction
  does **not** flatten; `binary_path` is the archive-relative location
  of the executable (e.g. `bin/llama-server`), and `run.workdir` +
  `binaryPath` resolve it there. Absent → current flatten behavior.
- **Symlink safety stays strict.** The zip extractor rejects symlink
  members outright; llama.cpp archives don't ship symlinks (verified at
  impl time per variant). The tar extractor must additionally refuse
  `TypeSymlink`/`TypeLink` before this module can ship a `.tar.gz`
  variant — today it silently skips non-regular types, which is safe
  but should be hardened to an explicit error for parity with zip.

## Release pinning

`ggml-org/llama.cpp` publishes `b<build>` tags with per-platform
`{os}-{backend}-{arch}` assets. Asset names encode the **backend
variant** (`cpu`, `cuda`, `vulkan`, `metal`, …) — so `install.
asset_templates` (v2) is not enough on its own: the selected backend is
a *field* (`gpu_backend`) that must feed the asset template, e.g.
`llama-b{build}-bin-{goos}-{gpu_backend}-{goarch}.zip`. Either the
field resolver grows a template hook (field → asset placeholder), or
the catalog pins one asset set per backend as separate modules
(`llama-cpp-cuda`, `llama-cpp-vulkan`). **Lean: field-fed template** —
one module id, backend chosen at config time; impl-time check that the
resolver can substitute fields into `asset_template`/`download_url`
before digest lookup (digests stay per-release per-platform per-variant
— `sha256` keys become `"goos/goarch/backend"` or releases pin one
variant each and the field only selects among pinned releases).

Upstream publishes `sha256` sums per asset — digests are pinned in the
catalog as usual. Upstream does **not** publish cryptographic
signatures on llama.cpp release assets (same posture as nimbus/helios/
kubo — see Track 95), so `signature` stays unset until that changes.

## Model weights — the trust story

The server binary is the small half; the weights are the trust-critical
half: `.gguf` files run 1–100 GB and llama-server maps them into the
agent's context pipeline. Two acquisition paths:

1. **`llama-server --hf-repo owner/repo --hf-file f.gguf`** — the binary
   pulls from Hugging Face itself over HTTPS. Integrity is TLS-only
   unless a digest is pinned; llama.cpp does not verify a pinned hash
   for HF downloads. **Not acceptable as the only path** for a module
   whose other inputs are digest-pinned.
2. **Digest-pinned local weights (the design).** Module fields:
   - `model_url` — HTTPS URL to the `.gguf` (Hugging Face resolve URL,
     ModelScope, or any host)
   - `model_sha256` — **required when `model_url` is set**; the
     first-run fetcher downloads to
     `<modules>/llama-cpp/weights/<sha256-prefix>.gguf`, streams the
     download through sha256 like `installRelease` does, and refuses a
     mismatch — the same floor, applied to weights.
   - Where the operator gets the hash: Hugging Face's API exposes the
     LFS SHA-256 per file (`/api/models/<repo>?expand[]=lfs` →
     `siblings[].lfs.sha256`, also `x-linked-etag` on a HEAD request to
     the resolve URL). `rhizome module set llama-cpp model_url=…
     model_sha256=…` is validated at `module validate` time; a later
     `model fetch` subcommand could fetch-and-pin in one step.
   - Weights land outside `<version>/` so reinstalling the binary does
     not re-download gigabytes; `.binary-digest`-style records can pin
     the weights file too.

## Field sketch

```text
model_url        HTTPS URL of the .gguf (secret? no — public weights are
                 fine; authenticated endpoints → secret)
model_sha256     sha256 of the weights file (required with model_url)
gpu_backend      cpu|cuda|rocm|vulkan|metal|sycl → asset variant select
gpu_layers       -ngl (0 = CPU only, -1 = all layers)
ctx_size         -c context window
threads          -t (default: host cores)
port             8080 default; loopback bind only unless overridden
api_key          secret field → --api-key (loopback doesn't need it;
                 remote binds do)
mlock            flag → --mlock
flash_attn       flag → --flash-attn
extra_args       escape hatch appended to args_template verbatim
```

`run.args_template` composes `-m {weights_path} --port {port} -c
{ctx_size} -ngl {gpu_layers} …`; `weights_path` is the verified local
path, never a URL — the server never fetches weights itself.

## Health and consumers

- `health`: `http` GET `http://127.0.0.1:{port}/health` → 200.
- Consumer story (impl-time): an `llm` provider profile `type:
  "openai-compatible"` with `base_url: http://127.0.0.1:{port}/v1`,
  optionally auto-starting the module when the provider is selected.
  The daemon's module endpoints already expose start/stop/status — the
  UI just needs an llama.cpp card like nimbus's.

## Open questions for impl

- Exact archive layouts per OS/backend variant (root vs `bin/`+`lib/`)
  — decides whether `binary_path` is required at ship time.
- Whether `gpu_backend` → asset substitution lands as a template hook
  or as separate catalog ids.
- Apple-silicon + Windows-CUDA coverage matrix; keep the first pinned
  release to `cpu` everywhere + `metal`/`cuda` where build exists.
