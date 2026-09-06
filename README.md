<div align="center">
<img src="assets/logo.webp" alt="Rhizome" width="512">

<h1>Rhizome: Ultra-Efficient AI Assistant in Go</h1>

<h3>$10 Hardware · 10MB RAM · ms Boot · Let's Go, Rhizome!</h3>
  <p>
    <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/Arch-x86__64%2C%20ARM64%2C%20MIPS%2C%20RISC--V%2C%20LoongArch-blue" alt="Hardware">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
    <br>
    <a href="https://github.com/stpinkie/rhizome"><img src="https://img.shields.io/badge/GitHub-stpinkie/rhizome-181717?style=flat&logo=github&logoColor=white" alt="GitHub"></a>
    <a href="https://github.com/stpinkie/rhizome/tree/main/docs"><img src="https://img.shields.io/badge/Docs-007acc?style=flat&logo=read-the-docs&logoColor=white" alt="Docs"></a>
    <a href="https://discord.gg/V4sAZ9XWpN"><img src="https://img.shields.io/badge/Discord-Community-4c60eb?style=flat&logo=discord&logoColor=white" alt="Discord"></a>
    <br>
    <a href="./assets/wechat.png"><img src="https://img.shields.io/badge/WeChat-Group-41d56b?style=flat&logo=wechat&logoColor=white"></a>
  </p>

[中文](docs/project/README.zh.md) | [日本語](docs/project/README.ja.md) | [한국어](docs/project/README.ko.md) | [Português](docs/project/README.pt-br.md) | [Tiếng Việt](docs/project/README.vi.md) | [Français](docs/project/README.fr.md) | [Italiano](docs/project/README.it.md) | [Bahasa Indonesia](docs/project/README.id.md) | [Malay](docs/project/README.ms.md) | **English**

</div>

---

> **Rhizome** is a community-maintained hard fork of [PicoClaw](https://github.com/sipeed/picoclaw). It is written entirely in **Go** and continues the goal of an ultra-lightweight personal AI assistant.

**Rhizome** is a personal AI assistant inspired by [NanoBot](https://github.com/HKUDS/nanobot). It adds a Go-native P2P mesh, workspace sync, and an agent gateway on top of the original PicoClaw idea.

**Single Go binary, no runtime dependencies** — runs natively on Linux, Windows, macOS, FreeBSD/NetBSD, and Android, including 32-bit x86, ARMv7, and native Android 4.4+ targets. See the [Hardware Compatibility List](docs/guides/hardware-compatibility.md) for verified boards and the current two-tier resource requirements.

<p align="center">
<img src="assets/rhizome_mem.gif" width="360" height="240">
</p>

> [!CAUTION]
> **Security Notice**
>
> * **NO CRYPTO:** Rhizome has **not** issued any official tokens or cryptocurrency. All claims on `pump.fun` or other trading platforms are **scams**.
> * **CANONICAL SOURCE:** The canonical source and release location is **<https://github.com/stpinkie/rhizome>**; releases are published under GitHub Releases. Beware of third-party domains claiming to be official.
> * **BEWARE:** Many `.ai/.org/.com/.net/...` domains have been registered by third parties. Do not trust them.
> * **NOTE:** Rhizome is in early rapid development. There may be unresolved security issues. Do not deploy to production before v1.0.
> * **NOTE:** The full `rhizome` binary is ~98 MB and the daemon uses ~60 MB private memory. A `nonetwork` build is planned to reduce the footprint for very small boards. Resource optimization is planned after feature stabilization.

## 📢 News

2026-09-07 🌐 **v0.6.1 — Mesh operations follow-through** adds a persisted task store (`~/.rhizome/mesh-tasks.jsonl`) so remote tasks survive daemon restarts, and a live `GET /api/network/tasks/events` Server-Sent Event stream that drives the Network page Remote Tasks panel with near-realtime status updates and completion toasts.

2026-09-06 🌐 **v0.6.0 — Mesh operations** makes the v0.5 mesh stack operationally usable: remote tasks are now exposed over the daemon gateway, the launcher, the web console, and the CLI, with capability-driven routing (`active_tasks` load), `rhizome mesh route`, mesh audit log, and new protocol unit tests. See the [v0.6.0 release notes](docs/release-notes/v0.6.0.md).

2026-09-05 🌐 **v0.5.0 — Mesh maturity** adds NAT traversal (AutoNATv2, hole punching, circuit relay v2 with self-organizing relays), asynchronous remote agent tasks over `/rhizome/agent-task/1.0.0`, enforced mesh security (signed requests and capability manifests, per-peer ACLs, rate limits, audit trail), and hardened workspace sync with reconnect catch-up. See the [v0.5.0 release notes](docs/release-notes/v0.5.0.md).

2026-09-04 🚀 **v0.4.2–v0.4.8** shipped the live network status API and Network dashboard, trust/capability persistence, saved-peer management across CLI/daemon/launcher, and a catalog-driven provider protocol refactor with first-class local providers (Ollama, vLLM, LM Studio, LiteLLM). Release notes: [v0.4.2](docs/release-notes/v0.4.2.md)–[v0.4.8](docs/release-notes/v0.4.8.md).

2026-09-01 🚀 **v0.4.0 — 32-bit and Android portability** adds `linux/386`, `windows/386`, `linux/armv7`, and native Android `arm64`/`arm`/`386`/`amd64` builds. P2P transports are hardened for Linux 3.4 / Android 4.4 kernels, and mDNS now fails gracefully when multicast is unavailable.

2026-05-28 🚀 **v0.2.9 Released!** MCP server management in Web UI, configurable Sogou-backed web search, tool feedback animation in channels, `pretty_print` and `disable_escape_html` defaults, and numerous bug fixes across providers and channels.

2026-05-14 🚀 **v0.2.8 Released!** MCP CLI commands (`show`, `add`, `list`, `remove`, `test`, `edit`), empty object instead of null for MCP tool parameters, and build fixes.

2026-05-07 🚀 **v0.2.7 Released!** Configurable Sogou-backed web search, channel tool feedback animation, linter fixes.

2026-04-23 🚀 **v0.2.6 Released!** Hooks with respond action and comprehensive documentation, isolation support, help banner fix.

2026-04-11 🚀 **v0.2.5 Released!** Zoneinfo from TZ/ZONEINFO env, Matrix CommonMark rendering alignment, `read_file` by lines.

2026-03-31 📱 **Android Support!** Rhizome now runs on Android! The Android APK is not currently distributed from this fork; build from source or check [GitHub Releases](https://github.com/stpinkie/rhizome/releases) for a future APK.

2026-03-25 🚀 **v0.2.4 Released!** Agent architecture overhaul (SubTurn, Hooks, Steering, EventBus), WeChat/WeCom integration, security hardening (.security.yml, sensitive data filtering), new providers (AWS Bedrock, Azure, Xiaomi MiMo), and 35 bug fixes. Rhizome has reached **26K Stars**!

2026-03-17 🚀 **v0.2.3 Released!** System tray UI (Windows & Linux), sub-agent status query (`spawn_status`), experimental Gateway hot-reload, Cron security gating, and 2 security fixes. Rhizome has reached **25K Stars**!

2026-03-09 🎉 **v0.2.1 — Biggest update yet!** MCP protocol support, 4 new channels (Matrix/IRC/WeCom/Discord Proxy), 3 new providers (Kimi/Minimax/Avian), vision pipeline, JSONL memory store, model routing.

2026-02-28 📦 **v0.2.0** released with Docker Compose and Web UI Launcher support.

<details>
<summary>Earlier news...</summary>

2026-02-26 🎉 Rhizome hits **20K Stars** in just 17 days! Channel auto-orchestration and capability interfaces are live.

2026-02-16 🎉 Rhizome breaks 12K Stars in one week! Community maintainer roles and [Roadmap](ROADMAP.md) officially launched.

2026-02-13 🎉 Rhizome breaks 5000 Stars in 4 days! Project roadmap and developer groups in progress.

2026-02-09 🎉 **Rhizome Released!** Built in 1 day to explore ultra-lightweight AI Agents. Let's Go, Rhizome!

</details>

## ✨ Features

🪶 **Single binary, no runtime dependencies**: One statically-linked Go executable that runs on Linux, Windows, macOS, FreeBSD/NetBSD, and Android.*

💰 **Minimal cost**: Efficient enough to run on a wide range of low-cost ARM and RISC-V boards; see the [Hardware Compatibility List](docs/guides/hardware-compatibility.md).

⚡️ **Lightning-fast boot**: Starts in under a second on the verified low-cost boards.

🌍 **Truly portable**: Single binary across RISC-V, ARM (including 32-bit ARMv7), MIPS, and x86 (including 32-bit i386) architectures. One binary, runs everywhere!

🤖 **AI-bootstrapped**: Pure Go native implementation — 95% of core code was generated by an Agent and fine-tuned through human-in-the-loop review.

🔌 **MCP support**: Native [Model Context Protocol](https://modelcontextprotocol.io/) integration — connect any MCP server to extend Agent capabilities.

👁️ **Vision pipeline**: Send images and files directly to the Agent — automatic base64 encoding for multimodal LLMs.

🧠 **Smart routing**: Rule-based model routing — simple queries go to lightweight models, saving API costs.

_*Measured on Windows with `CGO_ENABLED=0`, tags `goolm,stdjson`, and `-ldflags "-s -w"`; the stripped binary is ~98 MB. A `nonetwork` build is planned to reduce the footprint for very small boards._

<div align="center">

### Current Build Footprint

| Mode | Use case | Total RAM | Free RAM | Storage |
|------|------|-----------|----------|---------|
| **Base** | One-shot `rhizome agent`, `rhizome onboard` | 256 MB | 128 MB | 128 MB |
| **Full** | `rhizome daemon` with P2P, syncer, and gateway | 512 MB | 256 MB | 128 MB |

</div>

> **[Hardware Compatibility List](docs/guides/hardware-compatibility.md)** — See all verified boards, from Raspberry Pi to Android phones. Your board not listed? Submit a PR!

<p align="center">
<img src="assets/hardware-banner.jpg" alt="Rhizome Hardware Compatibility" width="100%">
</p>

## 🦾 Demonstration

### 🛠️ Standard Assistant Workflows

<table align="center">
<tr align="center">
<th><p align="center">Full-Stack Engineer Mode</p></th>
<th><p align="center">Logging & Planning</p></th>
<th><p align="center">Web Search & Learning</p></th>
</tr>
<tr>
<td align="center"><p align="center"><img src="assets/rhizome_code.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="assets/rhizome_memory.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="assets/rhizome_search.gif" width="240" height="180"></p></td>
</tr>
<tr>
<td align="center">Develop · Deploy · Scale</td>
<td align="center">Schedule · Automate · Remember</td>
<td align="center">Discover · Insights · Trends</td>
</tr>
</table>

### 🐜 Innovative Low-Footprint Deployment

Rhizome can be deployed on a wide range of Linux and embedded devices!

- $15 [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) (or [Zero 2 W](https://www.raspberrypi.com/products/raspberry-pi-zero-2-w/)), for a minimal home assistant
- $50~70 [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/), for RISC-V-based embedded use
- $100 [NanoKVM-Pro](https://www.aliexpress.com/item/1005010048471263.html), for automated server operations
- $100 [MaixCAM2](https://www.kickstarter.com/projects/zepan/maixcam2-build-your-next-gen-4k-ai-camera), for smart surveillance

> See the [Hardware Compatibility List](docs/guides/hardware-compatibility.md) for the full list of verified boards and the current two-tier requirements.

<https://private-user-images.githubusercontent.com/83055338/547056448-e7b031ff-d6f5-4468-bcca-5726b6fecb5c.mp4>

🌟 More Deployment Cases Await!

## 📦 Install

### Download from GitHub Releases (Recommended)

Visit the [GitHub Releases](https://github.com/stpinkie/rhizome/releases) page and download the binary for your platform.

### Download precompiled binary

Alternatively, download the binary for your platform from the [GitHub Releases](https://github.com/stpinkie/rhizome/releases) page.

### Build from source (for development)

Prerequisites:

- Go 1.26+
- Node.js 22+ and pnpm 10.33.0+ for Web UI / launcher builds

```bash
git clone https://github.com/stpinkie/rhizome.git

cd rhizome
make deps

# Install frontend dependencies
(cd web/frontend && pnpm install --frozen-lockfile)

# Build the core binary for the current platform
make build

# Build the Web UI Launcher (required for WebUI mode)
make build-launcher

# Build core binaries for all Makefile-managed platforms
make build-all

# Build for Raspberry Pi Zero 2 W
# 32-bit: make build-linux-arm
# 64-bit: make build-linux-arm64
make build-pi-zero

# Build and install
make install
```

**Raspberry Pi Zero 2 W:** Use the binary that matches your OS: 32-bit Raspberry Pi OS -> `make build-linux-arm`; 64-bit -> `make build-linux-arm64`. Or run `make build-pi-zero` to build both.

## 🚀 Quick Start Guide

### 🌐 WebUI Launcher (Recommended for Desktop)

The WebUI Launcher provides a browser-based interface for configuration and chat. This is the easiest way to get started — no command-line knowledge required.

**Option 1: Double-click (Desktop)**

After downloading from [GitHub Releases](https://github.com/stpinkie/rhizome/releases), double-click `rhizome-launcher` (or `rhizome-launcher.exe` on Windows). Your browser will open automatically at `http://localhost:18800`.

**Option 2: Command line**

```bash
rhizome-launcher
# Open http://localhost:18800 in your browser
```

> [!TIP]
> **Remote access / Docker / VM:** Add the `-public` flag to listen on all interfaces:
> ```bash
> rhizome-launcher -public
> ```

<p align="center">
<img src="assets/launcher-webui.jpg" alt="WebUI Launcher" width="600">
</p>

**Getting started:**

Open the WebUI, then: **1)** Configure a Provider (add your LLM API key) -> **2)** Configure a Channel (e.g., Telegram) -> **3)** Start the Gateway -> **4)** Chat!

For detailed documentation, see the [docs/ folder](https://github.com/stpinkie/rhizome/tree/main/docs) in this repo.

<details>
<summary><b>Docker (alternative)</b></summary>

```bash
# 1. Clone this repo
git clone https://github.com/stpinkie/rhizome.git
cd rhizome

# 2. First run — auto-generates docker/data/config.json then exits
#    (only triggers when both config.json and workspace/ are missing)
docker compose -f docker/docker-compose.yml --profile launcher up
# The container prints "First-run setup complete." and stops.

# 3. Set your API keys
vim docker/data/config.json

# 4. Start
docker compose -f docker/docker-compose.yml --profile launcher up -d
# Open http://localhost:18800
```

> **Docker / VM users:** The Gateway listens on `127.0.0.1` by default. Set `RHIZOME_GATEWAY_HOST=0.0.0.0` or use the `-public` flag to make it accessible from the host.

```bash
# Check logs
docker compose -f docker/docker-compose.yml logs -f

# Stop
docker compose -f docker/docker-compose.yml --profile launcher down

# Update
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

</details>

<details>
<summary><b>macOS — First Launch Security Warning</b></summary>

macOS may block `rhizome-launcher` on first launch because it is downloaded from the internet and not notarized through the Mac App Store.

**Step 1:** Double-click `rhizome-launcher`. You will see a security warning:

<p align="center">
<img src="assets/macos-gatekeeper-warning.jpg" alt="macOS Gatekeeper warning" width="400">
</p>

> *"rhizome-launcher" Not Opened — Apple could not verify "rhizome-launcher" is free of malware that may harm your Mac or compromise your privacy.*

**Step 2:** Open **System Settings** → **Privacy & Security** → scroll down to the **Security** section → click **Open Anyway** → confirm by clicking **Open Anyway** in the dialog.

<p align="center">
<img src="assets/macos-gatekeeper-allow.jpg" alt="macOS Privacy & Security — Open Anyway" width="600">
</p>

After this one-time step, `rhizome-launcher` will open normally on subsequent launches.

</details>

<a id="-run-on-old-android-phones"></a>
### 📱 Android

Give your decade-old phone a second life! Turn it into a smart AI Assistant with Rhizome.

**Option 1: APK Install**

Preview:

<table>
  <tr>
    <td><img src="assets/fui_main_page.jpg" width="200"></td>
    <td><img src="assets/fui_web_page.jpg" width="200"></td>
    <td><img src="assets/fui_log_page.jpg" width="200"></td>
    <td><img src="assets/fui_setting_page.jpg" width="200"></td>
  </tr>
</table>

The Android APK is not currently published from this fork; build from source or check [GitHub Releases](https://github.com/stpinkie/rhizome/releases) for a future APK.

**Option 2: Termux**

For a full command-line setup checklist, see the [Android Termux Guide](docs/guides/android-termux.md).

<details>
<summary><b>Terminal Launcher (for resource-constrained environments)</b></summary>

1. Install [Termux](https://github.com/termux/termux-app) (download from [GitHub Releases](https://github.com/termux/termux-app/releases), or search in F-Droid / Google Play)
2. Run the following commands:

```bash
# Download the latest release
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz
pkg install proot
termux-chroot ./rhizome onboard   # chroot provides a standard Linux filesystem layout
```

Then follow the Terminal Launcher section below to complete configuration.

<img src="assets/termux.jpg" alt="Rhizome on Termux" width="512">

For minimal environments where only the `rhizome` core binary is available (no Launcher UI), you can configure everything via the command line and a JSON config file.

**1. Initialize**

```bash
rhizome onboard
```

This creates `~/.rhizome/config.json` and the workspace directory.

**2. Configure** (`~/.rhizome/config.json`)

```json
{
  "agents": {
    "defaults": {
      "model_name": "gpt-5.4"
    }
  },
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4"
      // api_key is now loaded from .security.yml
    }
  ]
}
```

> See `config/config.example.json` in the repo for a complete configuration template with all available options.
>
> Please note: config.example.json format is version 0, with sensitive codes in it, and will be auto migrated to version 1+, then, the config.json will only store insensitive data, the sensitive codes will be stored in .security.yml, if you need manually modify the codes, please see `docs/security/security_configuration.md` for more details.


**3. Chat**

```bash
# One-shot question
rhizome agent -m "What is 2+2?"

# Interactive mode
rhizome agent

# Start gateway for chat app integration
rhizome gateway
```

</details>

## 🔌 Providers (LLM)

Rhizome supports 30+ LLM providers through the `model_list` configuration. Use the `protocol/model` format:

| Provider | Protocol | API Key | Notes |
|----------|----------|---------|-------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | Required | GPT-5.4, GPT-4o, o3, etc. |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic-messages/` | Required | Native Claude Messages API |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | Required | Gemini 3 Flash, 2.5 Pro, etc. |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | Required | 200+ models, unified API |
| [Zhipu (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | Required | GLM-4.7, GLM-5, etc. |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | Required | DeepSeek-V3, DeepSeek-R1 |
| [Volcengine](https://console.volcengine.com) | `volcengine/` | Required | Doubao, Ark models |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | Required | Qwen3, Qwen-Max, etc. |
| [Groq](https://console.groq.com/keys) | `groq/` | Required | Fast inference (Llama, Mixtral) |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | Required | Kimi models |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | Required | MiniMax models |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | Required | Mistral Large, Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | Required | NVIDIA hosted models |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | Required | Fast inference |
| [NEAR AI Cloud](https://near.ai/) | `nearai/` | Required | TEE inference, OpenAI-compatible |
| [Novita AI](https://novita.ai/) | `novita/` | Required | Various open models |
| [Xiaomi MiMo](https://platform.xiaomimimo.com/) | `mimo/` | Required | MiMo models |
| [Ollama](https://ollama.com/) | `ollama/` | Not needed | Local models, self-hosted |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | Not needed | Local deployment, OpenAI-compatible |
| [LM Studio](https://lmstudio.ai/) | `lmstudio/` | Not needed | Local GUI server, OpenAI-compatible |
|| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | Varies | Proxy for 100+ providers |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | API key or Entra ID** | Enterprise Azure deployment |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | Device code login |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |
| [AWS Bedrock](https://console.aws.amazon.com/bedrock)* | `bedrock/` | AWS credentials | Claude, Llama, Mistral on AWS |

> \* AWS Bedrock requires build tag: `go build -tags bedrock`. Set `api_base` to a region name (e.g., `us-east-1`) for automatic endpoint resolution across all AWS partitions (aws, aws-cn, aws-us-gov). When using a full endpoint URL instead, you must also configure `AWS_REGION` via environment variable or AWS config/profile.
>
> \*\* Azure OpenAI uses `api_key` when set. If `api_key` is omitted, the provider falls back to Microsoft Entra ID via `DefaultAzureCredential` (env vars, workload identity, managed identity, Azure CLI, etc.). The Entra ID path requires build tag: `go build -tags azidentity`.

<details>
<summary><b>Local deployment (Ollama, vLLM, etc.)</b></summary>

**Ollama:**
```json
{
  "model_list": [
    {
      "model_name": "local-llama",
      "model": "ollama/llama3.1:8b",
      "api_base": "http://localhost:11434/v1"
    }
  ]
}
```

**vLLM:**
```json
{
  "model_list": [
    {
      "model_name": "local-vllm",
      "model": "vllm/your-model",
      "api_base": "http://localhost:8000/v1"
    }
  ]
}
```

**LM Studio:**
```json
{
  "model_list": [
    {
      "model_name": "local-lmstudio",
      "model": "lmstudio/your-model",
      "api_base": "http://localhost:1234/v1"
    }
  ]
}
```

For full provider configuration details, see [Providers & Models](docs/guides/providers.md).

</details>

## 💬 Channels (Chat Apps)

Talk to your Rhizome through 19+ messaging platforms:

| Channel | Setup | Protocol | Docs |
|---------|-------|----------|------|
| **Telegram** | Easy (bot token) | Long polling | [Guide](docs/channels/telegram/README.md) |
| **Discord** | Easy (bot token + intents) | WebSocket | [Guide](docs/channels/discord/README.md) |
| **WhatsApp** | Easy (QR scan or bridge URL) | Native / Bridge | [Guide](docs/guides/chat-apps.md#whatsapp) |
| **Weixin** | Easy (Native QR scan) | iLink API | [Guide](docs/guides/chat-apps.md#weixin) |
| **QQ** | Easy (AppID + AppSecret) | WebSocket | [Guide](docs/channels/qq/README.md) |
| **Slack** | Easy (bot + app token) | Socket Mode | [Guide](docs/channels/slack/README.md) |
| **Matrix** | Medium (homeserver + token) | Sync API | [Guide](docs/channels/matrix/README.md) |
| **Delta Chat** | Easy (account script or email/password) | JSON-RPC (email/E2EE) | [Guide](docs/channels/deltachat/README.md) |
| **DingTalk** | Medium (client credentials) | Stream | [Guide](docs/channels/dingtalk/README.md) |
| **Feishu / Lark** | Medium (App ID + Secret) | WebSocket/SDK | [Guide](docs/channels/feishu/README.md) |
| **LINE** | Medium (credentials + webhook) | Webhook | [Guide](docs/channels/line/README.md) |
| **WeCom** | Easy (QR login or manual) | WebSocket | [Guide](docs/channels/wecom/README.md) |
| **VK** | Easy (group token) | Long Poll | [Guide](docs/channels/vk/README.md) |
| **IRC** | Medium (server + nick) | IRC protocol | [Guide](docs/guides/chat-apps.md#irc) |
| **OneBot** | Medium (WebSocket URL) | OneBot v11 | [Guide](docs/channels/onebot/README.md) |
| **MQTT** | Easy (broker + agent_id) | MQTT pub/sub | [Guide](docs/channels/mqtt/README.md) |
| **MaixCam** | Easy (enable) | TCP socket | [Guide](docs/channels/maixcam/README.md) |
| **Pico** | Easy (enable) | Native protocol | Built-in |
| **Pico Client** | Easy (WebSocket URL) | WebSocket | Built-in |

> All webhook-based channels share a single Gateway HTTP server (`gateway.host`:`gateway.port`, default `127.0.0.1:18790`). Feishu uses WebSocket/SDK mode and does not use the shared HTTP server.

> Log verbosity is controlled by `gateway.log_level` (default: `warn`). Supported values: `debug`, `info`, `warn`, `error`, `fatal`. Can also be set via `RHIZOME_LOG_LEVEL`. See [Configuration](docs/guides/configuration.md#gateway-log-level) for details.

For detailed channel setup instructions, see [Chat Apps Configuration](docs/guides/chat-apps.md).

## 🔧 Tools

### 🔍 Web Search

Rhizome can search the web to provide up-to-date information. Configure in `tools.web`:

| Search Engine | API Key | Free Tier | Link |
|--------------|---------|-----------|------|
| DuckDuckGo | Not needed | Unlimited | Built-in fallback |
| [Gemini Google Search](https://aistudio.google.com/apikey) | Required | Varies | Gemini with Google Search grounding |
| [Baidu Search](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | Required | 1500/month (daily allocation) | AI-powered, China-optimized |
| [Tavily](https://tavily.com) | Required | 1000 queries/month | Optimized for AI Agents |
| [Brave Search](https://brave.com/search/api) | Required | 2000 queries/month | Fast and private |
| [Kagi Search](https://help.kagi.com/kagi/api/search.html) | Required | Paid/limited by API setup | Premium search results |
| [Perplexity](https://www.perplexity.ai) | Required | Paid | AI-powered search |
| [SearXNG](https://github.com/searxng/searxng) | Not needed | Self-hosted | Free metasearch engine |
| [GLM Search](https://open.bigmodel.cn/) | Required | Varies | Zhipu web search |

### ⚙️ Other Tools

Rhizome includes built-in tools for file operations, code execution, scheduling, and more. See [Tools Configuration](docs/reference/tools_configuration.md) for details.

## 🎯 Skills

Skills are modular capabilities that extend your Agent. They are loaded from `SKILL.md` files in your workspace.

**Install skills from ClawHub:**

```bash
rhizome skills search "web scraping"
rhizome skills install <skill-name>
```

**Configure skill registries**:

Add to your `config.json`:
```json
{
  "tools": {
    "skills": {
      "registries": {
        "clawhub": {
          "auth_token": "your-clawhub-token"
        },
        "github": {
          "base_url": "https://github.com",
          "auth_token": "your-github-token",
          "proxy": ""
        }
      }
    }
  }
}
```

`tools.skills.github.*` is deprecated. Use `tools.skills.registries.github.*` instead.

For more details, see [Tools Configuration - Skills](docs/reference/tools_configuration.md#skills-tool).

## 🔗 MCP (Model Context Protocol)

Rhizome natively supports [MCP](https://modelcontextprotocol.io/) — connect any MCP server to extend your Agent's capabilities with external tools and data sources.

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "filesystem": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
        }
      }
    }
  }
}
```

You can manage common MCP setups directly from the CLI instead of editing JSON by hand:

```bash
rhizome mcp add filesystem -- npx -y @modelcontextprotocol/server-filesystem /tmp
rhizome mcp list
rhizome mcp test filesystem
```

`rhizome mcp` is a configuration manager: it updates `config.json` under `tools.mcp.servers`, but it does not keep the server process running itself.

Use `rhizome mcp edit` when you need advanced fields that are not covered by `rhizome mcp add`.
For example, `rhizome mcp add` supports `--deferred` and `--env-file`, while `rhizome mcp edit` is still useful for direct JSON editing and uncommon MCP settings.

For full MCP configuration (stdio, SSE, HTTP transports, Tool Discovery), see [Tools Configuration - MCP](docs/reference/tools_configuration.md#mcp-tool). For CLI usage and examples, see [MCP Server CLI](docs/reference/mcp-cli.md).

## <img src="assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> Join the Agent Social Network

Connect Rhizome to the Agent Social Network simply by sending a single message via the CLI or any integrated Chat App.

**Read `https://clawdchat.ai/skill.md` and follow the instructions to join [ClawdChat.ai](https://clawdchat.ai)**

## 🖥️ CLI Reference

| Command                   | Description                      |
| ------------------------- | -------------------------------- |
| `rhizome onboard`        | Initialize config & workspace    |
| `rhizome auth weixin` | Connect WeChat account via QR |
| `rhizome agent -m "..."` | Chat with the agent              |
| `rhizome agent`          | Interactive chat mode            |
| `rhizome gateway`        | Start the gateway                |
| `rhizome status`         | Show status                      |
| `rhizome version`        | Show version info                |
| `rhizome model`          | View or switch the default model |
| `rhizome mcp list`       | List configured MCP servers      |
| `rhizome mcp add ...`    | Add or update an MCP server entry |
| `rhizome mcp test`       | Probe a configured MCP server    |
| `rhizome mcp edit`       | Open config for advanced MCP editing |
| `rhizome mcp remove`     | Remove an MCP server entry       |
| `rhizome cron list`      | List all scheduled jobs          |
| `rhizome cron add ...`   | Add a scheduled job              |
| `rhizome cron disable`   | Disable a scheduled job          |
| `rhizome cron remove`    | Remove a scheduled job           |
| `rhizome skills list`    | List installed skills            |
| `rhizome skills install` | Install a skill                  |
| `rhizome migrate`        | Migrate data from older versions |
| `rhizome auth login`     | Authenticate with providers      |

### ⏰ Scheduled Tasks / Reminders

Rhizome supports scheduled reminders and recurring tasks through the `cron` tool:

* **One-time reminders**: "Remind me in 10 minutes" -> triggers once after 10min
* **Recurring tasks**: "Remind me every 2 hours" -> triggers every 2 hours
* **Cron expressions**: "Remind me at 9am daily" -> uses cron expression

See [docs/reference/cron.md](docs/reference/cron.md) for current schedule types, execution modes, command-job gates, and persistence details.

## 📚 Documentation

For detailed guides beyond this README:

| Topic | Description |
|-------|-------------|
| [Docker & Quick Start](docs/guides/docker.md) | Docker Compose setup, Launcher/Agent modes |
| [Chat Apps](docs/guides/chat-apps.md) | All 18+ channel setup guides |
| [Configuration](docs/guides/configuration.md) | Environment variables, workspace layout, security sandbox |
| [MCP Server CLI](docs/reference/mcp-cli.md) | Add, list, test, edit, and remove MCP server entries from the CLI |
| [Scheduled Tasks and Cron Jobs](docs/reference/cron.md) | Cron schedule types, deliver modes, command gates, job storage |
| [Providers & Models](docs/guides/providers.md) | 30+ LLM providers, model routing, model_list configuration |
| [Spawn & Async Tasks](docs/guides/spawn-tasks.md) | Quick tasks, long tasks with spawn, async sub-agent orchestration |
| [Hooks](docs/architecture/hooks/README.md) | Event-driven hook system: observers, interceptors, approval hooks |
| [Steering](docs/architecture/steering.md) | Inject messages into a running agent loop between tool calls |
| [SubTurn](docs/architecture/subturn.md) | Subagent coordination, concurrency control, lifecycle |
| [Troubleshooting](docs/operations/troubleshooting.md) | Common issues and solutions |
| [Tools Configuration](docs/reference/tools_configuration.md) | Per-tool enable/disable, exec policies, MCP, Skills |
| [Hardware Compatibility](docs/guides/hardware-compatibility.md) | Tested boards, minimum requirements |

## 🤝 Contribute & Roadmap

PRs welcome! The codebase is intentionally small and readable.

See our [Community Roadmap](https://github.com/stpinkie/rhizome/issues/988) and [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

Developer group building, join after your first merged PR!

User Groups:

Discord: <https://discord.gg/V4sAZ9XWpN>

WeChat:
<img src="assets/wechat.png" alt="WeChat group QR code" width="512">
