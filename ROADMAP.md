
# 🌱 Rhizome Roadmap

> **Vision**: A personal AI agent that runs everywhere you already have hardware — secure, autonomous, and cheap enough to leave running. Automate the mundane, unleash your creativity.

> **Re-evaluated post-v0.11.0**: items that shipped are marked with their release; the roadmap now tracks only what's actually open. Work is planned in `.todo.md` sprint tracks (GitHub issues are disabled on this repo); upstream PicoClaw issue numbers from the original roadmap are triaged in `docs/project/upstream-issue-triage.md`. Current sprint: **v0.12.0** (`docs/design/v0.12.0-sprint.md`).

---

## 🌐 1. Ubiquity: An Agent on Every Machine

*Our defining characteristic is omnipresence, not minimalism. Rhizome targets meaningful hardware people already own — aging laptops, obsolete Macs, old Android phones, Raspberry-Pi-class SBCs, free-tier VMs — not sub-64 MB proof-of-concept boards. Lightweight enough to forget it's running; never stripped of useful functionality to hit a number.*

* **Efficiency as a budget, not a floor**
  * **Goal**: Resident daemon comfortable on 256 MB-class boards and up; one-shot CLI on anything that can exec a Go binary. Idle cost and cold-start time matter more than absolute footprint.
  * ✅ Binary-size regression gate in CI (`scripts/binary-size-baseline.txt` — warn at +15%, fail at +30%).
  * ✅ Runtime RSS measurement (v0.10.0 Track 64): `scripts/measure-footprint.sh` — three profiles, JSON → `docs/architecture/footprint-audit.md` (worker ≈50 MB / full ≈55 MB steady RSS, 74.5 MB stripped binary); standing idle-RSS watch per release.
  * ✅ **Worker-node tier** (v0.10.0 Track 64): `mesh.role=worker` joins the mesh without infrastructure roles (no public DHT, relay service, AutoNAT service; pairs with `daemon --no-gateway`); `role` is signed into the capability manifest — the seam for role-aware routing (v0.12.0 Track 80).
  * **Action**: New dependencies must justify their cost; optimize idle CPU and cold start before shaving megabytes.


## 🛡️ 2. Security Hardening: Defense in Depth

*Paying off early technical debt. We invite security experts to help build a "Secure-by-Default" agent.*

* **Input Defense & Permission Control**
  * ✅ Prompt-injection defense (v0.7.1): shared `pkg/guard` patterns — shell-command screening, tool-call JSON extraction drops injected calls, registry arg scans.
  * ✅ SSRF protection (v0.7.1): `web_fetch`/`exec`/`browser_*` block private/loopback/link-local/metadata IPs at pre-flight and dial time; per-tool whitelists.
  * ✅ Filesystem sandbox (v0.7.1): `restrict_to_workspace` + read/write path whitelists.
  * ✅ Privacy redaction (v0.7.1): `pkg/redact` masks Bearer/API-key/token patterns, Authorization headers, and configured `SecureString`s in logs and tool output.

* **Authentication & Secrets**
  * ✅ Identity encryption (v0.9.x era): OS keyring / passphrase (scrypt) / none for `node.json`.
  * ✅ OAuth 2.0 provider flows (partial): browser+PKCE login in `pkg/auth/oauth.go` + `pkg/providers/oauth/` (codex/claude/antigravity) + `auth_method` plumbing. **Residual → v0.12.0 Track 82**: headless/device-code path + coverage/docs.
  * **Open → v0.12.0 Track 82**: `ChaCha20-Poly1305`-class modern secret storage (`enc2://` XChaCha20-Poly1305 alongside `enc://` AES-256-GCM).
  * ✅ Module supply-chain verification (v0.10.0 Track 60): checksum-pinned companion downloads (sha256/sha512 per platform). ✅ **Deepened (v0.11.0 Track 70)**: signed catalog + curated remote index + `module verify`.
  * **New → v0.12.0 Track 77 (see §9 Stage 2)**: web3 signing safety — transaction send gated behind allowlists, spending caps, signer scoping (`from_addresses`), and an async pending-approval queue (CLI/daemon/UI) layered on the `approve_tool` hook seam.


## 🔌 3. Connectivity: Protocol-First Architecture

*Connect every model, reach every platform — and every agent.*

* **Provider**
  * ✅ Protocol-based catalog (v0.4.8): OpenAI-compatible, Anthropic Messages, Gemini; first-class local protocols (Ollama, vLLM, LM Studio, LiteLLM); named `openai-compatible` preset (v0.9.0).
  * Online models: continued frontier support (ongoing).

* **Channel**
  * ✅ Attachment matrix (v0.9.0): inbound + `SendMedia` coverage across the IM fleet — see `docs/channels/media-matrix.md`.
  * IM matrix: QQ, WeChat (Work), DingTalk, Feishu (Lark), Telegram, Discord, WhatsApp, LINE, Slack, Email, KOOK, Signal, IRC ... (incremental).
  * ✅ OneBot protocol support (`pkg/channels/onebot` + `docs/channels/onebot/`).

* **Agent Interop**
  * ✅ **Agent Client Protocol (Zed ACP)** (v0.10.0 Tracks 61–62): server (`rhizome acp`: editors like Zed/JetBrains drive Rhizome over stdio JSON-RPC) and client (external ACP agents bound as first-class agent ids — routable via `delegate`, `spawn`, `network route`, and swarm offers). **Deepening → v0.12.0 Track 81**: `session/load` persistence, per-session MCP passthrough, `terminal` client capability (deny-default).

* **Skill Distribution**
  * ✅ Mesh skill distribution (v0.8.0): `mesh.skill_share` allowlist, `/rhizome/skill/1.0.0` + blob transport, guard-scanned install, mesh provenance.
  * ✅ Registry discovery: `find_skills`/`install_skill` tools + `pkg/skills` ClawHub/GitHub registries + Hub marketplace UI.
  * **Open**: curated Rhizome skill index / additional registries (depth, not discovery).


## 🧠 4. Advanced Capabilities: From Chatbot to Agentic AI

*Beyond conversation—focusing on action and collaboration.*

* **Operations**
  * ✅ MCP support (v0.8.0): `tools.mcp` servers + `tools.mcp.presets` (context7).
  * ✅ Browser automation (v0.7.1 backends + v0.8.0 native Go CDP client).
  * **Blocked**: Android device control — no test hardware.

* **Multi-Agent Collaboration**
  * ✅ Basic multi-agent + model routing basics (`pkg/routing`, `delegate`/`spawn`/`subagent`).
  * ✅ Swarm Mode (v0.7.0): trusted-peer swarms, presence, offer/claim work queue, coordinator election, `swarm run` goal orchestration, optional GossipSub.
  * ✅ Mesh depth (v0.8.0): trust pairing, blob transfer, task attachments, capability-aware claims, DAG orchestration.
  * ✅ Shared context + agent identity (v0.9.0): swarm blackboard + signed AIEOS-style agent manifests.
  * ✅ **Mesh observability** (v0.10.0 Track 63): per-connection transport/direction/RTT details, peer score surfacing, bandwidth counters, activity feed, dashboard panels.
  * **New → v0.12.0 Track 80**: role-aware `PickPeer` + role-preferred coordinator election + dashboard topology graph. Task/cost-aware dispatch deferred — folded into the mesh-economy module seed (`docs/design/mesh-economy-module.md`).
  * AIEOS: continued exploration of AI-native OS interaction paradigms.


## 📚 5. Developer Experience (DevEx) & Documentation

*Lowering the barrier to entry so anyone can deploy in minutes.*

* ✅ Zero-config onboarding (v0.9.0): interactive `rhizome onboard` wizard + web `/setup` first-run flow.
* Comprehensive documentation (ongoing): platform guides, provider/channel tutorials, AI-assisted docs with human verification.


## 🤖 6. Engineering: AI-Powered Open Source

*Born from Vibe Coding, we continue to use AI to accelerate development.*

* ✅ AI-assisted development loop: code review, lint sweeps, validation reports (`.rhizome-tests/`).
* **Open**: formalized bot triage/labeling and noise reduction in CI.


## 🎨 7. Brand & Community

* **Blocked**: Logo design (needs a real asset — no art pipeline): Mantis Shrimp concept, "Small but Mighty, Lightning Fast Strikes."
* **Open**: translated-README rebrand tail (PicoClaw mascot/slogan/emoji removal across 9 locales).


## 🧩 8. Extensibility: Companion Modules

*The base binary stays lean; optional capabilities ship as managed sidecar binaries installed after the base install. Generalizes the proven `pkg/browser` backend + install-manager pattern beyond browsers.*

* ✅ **Companion module system** (v0.10.0 Track 60)
  * Catalog-driven module specs; `daemon` (supervised long-running, e.g. a node), `on-demand` (spawned per use), and `config` (endpoint descriptor) lifecycle kinds.
  * Checksum-pinned downloads (`github-release`/`npm`/`detect`/`config` install methods); daemon supervision with backoff restart; `module.*` events.
  * `rhizome module list|status|install|uninstall|enable|disable|start|stop|restart|logs|set|validate` CLI + a web **Modules** page.
* ✅ **First tenants** (v0.10.0 Track 65): `nimbus-verified-proxy` (verified Ethereum JSON-RPC; linux/darwin/**windows**; P2P light-client sync default) + `ethereum-rpc` (config-only remote endpoint).
* ✅ **Module trust** (v0.11.0 Track 70): signed catalog (Rhizome Ed25519 release key), opt-in curated remote index (`module_index.url`), `module verify` drift detection.
* ✅ **New tenants** (v0.11.0 Track 71): `helios` (Ethereum light client; linux/darwin amd64+arm64, sha256-pinned), `ipfs-kubo` (IPFS node; adds `.zip` extraction, per-GOOS asset templates, `{module_dir}` placeholder, and marker-gated `init_args`/per-launch `setup_args` — catalog schema v2). `llama.cpp` deferred — needs symlink-safe layout-preserving extraction + a model-weights trust story; design standalone. Future candidates: ACP agent wrappers.


## 🪙 9. Web3 Rails (exploration)

*The Ethereum node option explicitly includes plans for **agentic web3 operations** — staged so the risky parts (signing) only arrive after the safe parts prove out.*

* ✅ **Stage 1 — Node availability** (v0.10.0 Track 65): the `nimbus-verified-proxy` module serves a verified local JSON-RPC (default :8545) for the user's own scripts and dapps. No agent tools.
* **Stage 2 — Agentic web3 operations**: `web3_*` agent tools over the module endpoint — ✅ chain reads (`eth_call`, balances, blocks, receipts, event logs; v0.11.0 Track 68); **→ v0.12.0 Tracks 77–79**: wallet/key management reusing the identity-encryption posture (keyring/passphrase); **gated signing** — transaction send behind contract/method allowlists, spending caps, signer scoping, and an async pending-approval queue on the `approve_tool` hook seam; **contract interaction** — ABI registry, `web3_contract_call`/`web3_contract_send`, ERC-20/721 helpers, ENS; **operations depth** — dashboard wallet/approvals panels, log watches, channel approvals (stretches).
* **Stage 3 — Mesh economy (deferred, optional module)**: pricing/settling remote task/swarm work between trusted peers — re-scoped during v0.12.0 planning as an opt-in module candidate rather than core mesh work; seed doc `docs/design/mesh-economy-module.md`.


---

### 🤝 Call for Contributions

We welcome community contributions to any item on this roadmap! Work is tracked in `.todo.md` sprint tracks — open a PR or comment on one to get involved. Let's build the best Edge AI Agent together!
