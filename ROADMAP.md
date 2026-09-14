
# 🌱 Rhizome Roadmap

> **Vision**: A personal AI agent that runs everywhere you already have hardware — secure, autonomous, and cheap enough to leave running. Automate the mundane, unleash your creativity.

> **Re-evaluated post-v0.9.1**: items that shipped are marked with their release; the roadmap now tracks only what's actually open. Work is planned in `.todo.md` sprint tracks (GitHub issues are disabled on this repo); upstream PicoClaw issue numbers from the original roadmap are triaged in `docs/project/upstream-issue-triage.md`.

---

## 🌐 1. Ubiquity: An Agent on Every Machine

*Our defining characteristic is omnipresence, not minimalism. Rhizome targets meaningful hardware people already own — aging laptops, obsolete Macs, old Android phones, Raspberry-Pi-class SBCs, free-tier VMs — not sub-64 MB proof-of-concept boards. Lightweight enough to forget it's running; never stripped of useful functionality to hit a number.*

* **Efficiency as a budget, not a floor**
  * **Goal**: Resident daemon comfortable on 256 MB-class boards and up; one-shot CLI on anything that can exec a Go binary. Idle cost and cold-start time matter more than absolute footprint.
  * ✅ Binary-size regression gate in CI (`scripts/binary-size-baseline.txt` — warn at +15%, fail at +30%).
  * **Open → v0.10.0 Track 64**: runtime RSS measurement (`scripts/measure-footprint.sh` — three profiles, JSON → `docs/architecture/footprint-audit.md`) + a standing idle-RSS watch per release.
  * **Open → v0.10.0 Track 64**: **worker-node tier** — a mesh participant that consumes swarm/sync/task services without running infrastructure roles (no public DHT, relay service, AutoNAT service; pairs with `daemon --no-gateway`); `role` on the signed capability manifest is the seam for later role-aware routing.
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
  * **Open**: OAuth 2.0 flows for providers — deprecate hardcoded API keys.
  * **Open**: `ChaCha20-Poly1305`-class modern secret storage.
  * **New → v0.10.0 Track 60**: module supply-chain verification — checksum-pinned companion downloads; signatures (cosign/sigstore) later.
  * **New (see §9 Stage 2)**: web3 signing safety — transaction send gated behind allowlists, spending caps, and the approval-hook seam.


## 🔌 3. Connectivity: Protocol-First Architecture

*Connect every model, reach every platform — and every agent.*

* **Provider**
  * ✅ Protocol-based catalog (v0.4.8): OpenAI-compatible, Anthropic Messages, Gemini; first-class local protocols (Ollama, vLLM, LM Studio, LiteLLM); named `openai-compatible` preset (v0.9.0).
  * Online models: continued frontier support (ongoing).

* **Channel**
  * ✅ Attachment matrix (v0.9.0): inbound + `SendMedia` coverage across the IM fleet — see `docs/channels/media-matrix.md`.
  * IM matrix: QQ, WeChat (Work), DingTalk, Feishu (Lark), Telegram, Discord, WhatsApp, LINE, Slack, Email, KOOK, Signal, IRC ... (incremental).
  * **Open**: OneBot protocol support.

* **Agent Interop**
  * **New → v0.10.0 Tracks 61–62**: **Agent Client Protocol (Zed ACP)** — server (`rhizome acp`: editors like Zed/JetBrains drive Rhizome over stdio JSON-RPC) and client (external ACP agents bound as first-class agent ids — routable via `delegate`, `spawn`, `network route`, and swarm offers).

* **Skill Distribution**
  * ✅ Mesh skill distribution (v0.8.0): `mesh.skill_share` allowlist, `/rhizome/skill/1.0.0` + blob transport, guard-scanned install, mesh provenance.
  * **Open**: `find_skill` registry discovery (GitHub Skills Repo / other registries).


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
  * **New → v0.10.0 Track 63**: **mesh observability** — per-connection transport/direction/RTT details, peer score surfacing, bandwidth counters, activity feed, dashboard panels.
  * Smart routing deepening (task/cost-aware dispatch) — open.
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

* **New → v0.10.0 Track 60**: **companion module system**
  * Catalog-driven module specs; `daemon` (supervised long-running, e.g. a node) and `on-demand` (spawned per use, e.g. an agent process) lifecycle kinds.
  * Checksum-pinned downloads (`github-release`/`npm`/`detect`/`config` install methods); daemon supervision with backoff restart; `module.*` events.
  * `rhizome module list|status|install|uninstall|enable|disable|start|stop|logs` CLI + a web **Modules** page.
* **First tenants → v0.10.0 Track 65**: `nimbus-verified-proxy` (verified Ethereum JSON-RPC; ships linux/darwin/**windows**) + `ethereum-rpc` (config-only remote endpoint). Future candidates: `helios` (linux/darwin only — no Windows binary), IPFS node, bundled local-model runtime, ACP agent wrappers.
* **Later**: cosign/sigstore signature verification; curated module index.


## 🪙 9. Web3 Rails (exploration)

*The Ethereum node option explicitly includes plans for **agentic web3 operations** — staged so the risky parts (signing) only arrive after the safe parts prove out.*

* **Stage 1 — Node availability → v0.10.0 Track 65**: the `nimbus-verified-proxy` module serves a verified local JSON-RPC (default :8545) for the user's own scripts and dapps. No agent tools.
* **Stage 2 — Agentic web3 operations** (roadmap): `web3_*` agent tools over the module endpoint — chain reads (`eth_call`, balances, ENS, event logs) and contract ABI interaction; wallet/key management reusing the identity-encryption posture (keyring/passphrase); **gated signing** — transaction send behind contract/method allowlists, spending caps, and the approval-hook seam.
* **Stage 3 — Mesh settlement** (horizon): paying trusted peers for remote task/swarm work over the mesh; exploratory, only after Stage 2 hardening.


---

### 🤝 Call for Contributions

We welcome community contributions to any item on this roadmap! Work is tracked in `.todo.md` sprint tracks — open a PR or comment on one to get involved. Let's build the best Edge AI Agent together!
