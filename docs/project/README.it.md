<div align="center">
<img src="../../assets/logo.webp" alt="Rhizome" width="512">

<h1>Rhizome: Assistente AI Ultra-Efficiente in Go</h1>

<h3>Hardware $10 · 10MB RAM · Boot in ms · Let's Go, Rhizome!</h3>
  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/Arch-x86__64%2C%20ARM64%2C%20MIPS%2C%20RISC--V%2C%20LoongArch-blue" alt="Hardware">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
    <br>
    <a href="https://github.com/stpinkie/rhizome"><img src="https://img.shields.io/badge/GitHub-stpinkie/rhizome-181717?style=flat&logo=github&logoColor=white" alt="GitHub"></a>
    <a href="https://github.com/stpinkie/rhizome/tree/main/docs"><img src="https://img.shields.io/badge/Docs-007acc?style=flat&logo=read-the-docs&logoColor=white" alt="Docs"></a>
    <a href="https://discord.gg/V4sAZ9XWpN"><img src="https://img.shields.io/badge/Discord-Community-4c60eb?style=flat&logo=discord&logoColor=white" alt="Discord"></a>
    <br>
    <a href="../../assets/wechat.png"><img src="https://img.shields.io/badge/WeChat-Group-41d56b?style=flat&logo=wechat&logoColor=white"></a>
  </p>

[中文](README.zh.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | **Italiano** | [Bahasa Indonesia](README.id.md) | [Malay](README.ms.md) | [English](../../README.md)

</div>

---

> **Rhizome** è un hard fork mantenuto dalla comunità di [PicoClaw](https://github.com/sipeed/picoclaw). È scritto interamente in **Go** e prosegue l'obiettivo di essere un assistente AI personale ultra-leggero.

**Rhizome** è un assistente AI personale ispirato a [NanoBot](https://github.com/HKUDS/nanobot). Aggiunge una mesh P2P nativa Go, la sincronizzazione del workspace e un gateway agent sopra l'idea originale di PicoClaw.

**Un unico binario Go, senza dipendenze runtime** — gira nativamente su Linux, Windows, macOS, FreeBSD/NetBSD e Android. Vedi la [Lista di Compatibilità Hardware](../guides/hardware-compatibility.md) per le schede verificate e i requisiti attuali a due livelli.

<p align="center">
<img src="../../assets/rhizome_mem.gif" width="360" height="240">
</p>

> [!CAUTION]
> **Avviso di Sicurezza**
>
> * **NO CRYPTO:** Rhizome **non** ha emesso alcun token ufficiale o criptovaluta. Qualsiasi affermazione su `pump.fun` o altre piattaforme di trading è una **truffa**.
> * **CANONICAL SOURCE:** La fonte canonica e la sede di rilascio sono **<https://github.com/stpinkie/rhizome>**; i rilasci sono pubblicati su GitHub Releases. Attenzione ai domini di terze parti che affermano di essere ufficiali.
> * **Attenzione:** Molti domini `.ai/.org/.com/.net/...` sono stati registrati da terze parti. Non fidatevi.
> * **Nota:** Rhizome è in una fase iniziale di sviluppo rapido. Potrebbero esserci problemi di sicurezza non risolti. Non distribuire in produzione prima della v1.0.
> * **Nota:** Il binario `rhizome` completo è di circa 98 MB e il daemon usa circa 60 MB di memoria privata. Abbiamo in programma una build `nonetwork` per ridurre ulteriormente l'impronta su schede ultra-piccole. L'ottimizzazione delle risorse è pianificata dopo la stabilizzazione delle funzionalità.

## 📢 Novità

2026-05-28 🚀 **Rilasciata v0.2.9!** Gestione dei server MCP nella Web UI, ricerca web Sogou configurabile, animazione di feedback degli strumenti del canale, valori predefiniti `pretty_print` e `disable_escape_html`, e varie correzioni di bug per provider e canali.

2026-05-14 🚀 **Rilasciata v0.2.8!** Comandi MCP CLI (`show`, `add`, `list`, `remove`, `test`, `edit`), oggetto vuoto invece di null per i parametri degli strumenti MCP, e correzioni di build.

2026-05-07 🚀 **Rilasciata v0.2.7!** Ricerca web Sogou configurabile, animazione di feedback degli strumenti del canale, correzioni del linter.

2026-04-23 🚀 **Rilasciata v0.2.6!** Hook con azione di risposta e documentazione completa, supporto all'isolamento, correzione del banner di aiuto.

2026-04-11 🚀 **Rilasciata v0.2.5!** Zoneinfo dalle variabili d'ambiente TZ/ZONEINFO, allineamento del rendering Matrix CommonMark, `read_file` per righe.

2026-03-31 📱 **Supporto Android!** Rhizome ora gira su Android! L'APK Android non è distribuito da questo fork; compilare dai sorgenti o controllare i [GitHub Releases](https://github.com/stpinkie/rhizome/releases) per un APK futuro.

2026-03-25 🚀 **Rilasciata v0.2.4!** Ristrutturazione completa dell'architettura Agent (SubTurn, Hook, Steering, EventBus), integrazione profonda WeChat/WeCom, potenziamento della sicurezza (.security.yml, filtraggio dati sensibili), nuovi provider (AWS Bedrock, Azure, Xiaomi MiMo) e 35 correzioni di bug. Rhizome ha raggiunto **26K Stars**!

2026-03-17 🚀 **Rilasciata v0.2.3!** UI nel vassoio di sistema (Windows & Linux), query di stato del sub-agent (`spawn_status`), hot-reload sperimentale del Gateway, gate di sicurezza Cron, e 2 correzioni di sicurezza. Rhizome ha raggiunto **25K Stars**!

2026-03-09 🎉 **v0.2.1 — L'aggiornamento più grande finora!** Supporto al protocollo MCP, 4 nuovi channel (Matrix/IRC/WeCom/Discord Proxy), 3 nuovi provider (Kimi/Minimax/Avian), pipeline visiva, memoria JSONL, routing dei modelli.

2026-02-28 📦 **v0.2.0** rilasciata con supporto a Docker Compose e Web UI Launcher.

<details>
<summary>Novità precedenti...</summary>

2026-02-26 🎉 Rhizome raggiunge **20K Stars** in soli 17 giorni! Orchestrazione automatica dei channel e interfaccia delle capacità disponibili.

2026-02-16 🎉 Rhizome supera 12K Stars in una settimana! Ruolo di maintainer della comunità e [Roadmap](../../ROADMAP.md) rilasciati ufficialmente.

2026-02-13 🎉 Rhizome supera 5000 Stars in 4 giorni! Roadmap del progetto e gruppo sviluppatori in costruzione.

2026-02-09 🎉 **Rhizome rilasciato!** Costruito in 1 giorno per esplorare Agent AI ultra-leggeri. Let's Go, Rhizome!

</details>

## ✨ Caratteristiche

🪶 **Binario unico, senza dipendenze runtime**: Un eseguibile Go collegato staticamente che gira su Linux, Windows, macOS, FreeBSD/NetBSD e Android.*

💰 **Costo minimo**: Abbastanza efficiente da girare su una vasta gamma di schede ARM e RISC-V a basso costo; vedi la [Lista di Compatibilità Hardware](../guides/hardware-compatibility.md).

⚡️ **Boot fulmineo**: Si avvia in meno di un secondo sulle schede a basso costo verificate.

🌍 **Veramente portatile**: Un unico binario tra le architetture RISC-V, ARM, MIPS e x86. Un binario, gira ovunque!

🤖 **AI-bootstrapped**: Implementazione pura nativa Go — il 95% del codice core è stato generato da un Agent e affinato attraverso revisione umana.

🔌 **Supporto MCP**: Integrazione nativa con il [Model Context Protocol](https://modelcontextprotocol.io/) — connetti qualsiasi server MCP per estendere le capacità dell'Agent.

👁️ **Pipeline visiva**: Invia immagini e file direttamente all'Agent — codifica base64 automatica per LLM multimodali.

🧠 **Routing intelligente**: Routing dei modelli basato su regole — le query semplici vanno a modelli leggeri, risparmiando costi API.

_*La misurazione dell'impronta è stata effettuata su Windows con `CGO_ENABLED=0`, tag `goolm,stdjson` e `-ldflags "-s -w"`; il binario strip è di circa 98 MB. Una build `nonetwork` è pianificata per ridurre ulteriormente su schede ultra-piccole._

<div align="center">

### Impronta Attuale della Build

| Modo | Caso d'uso | RAM Totale | RAM Libera | Archiviazione |
|------|------------|------------|------------|---------------|
| **Base** | `rhizome agent`, `rhizome onboard` one-shot | 256 MB | 128 MB | 128 MB |
| **Completo** | `rhizome daemon` con P2P, syncer e gateway | 512 MB | 256 MB | 128 MB |

</div>

> **[Lista di Compatibilità Hardware](../guides/hardware-compatibility.md)** — Vedi tutte le schede testate, dal Raspberry Pi ai telefoni Android. La tua scheda non è in lista? Invia una PR!

<p align="center">
<img src="../../assets/hardware-banner.jpg" alt="Rhizome Hardware Compatibility" width="100%">
</p>

## 🦾 Dimostrazione

### 🛠️ Flussi di Lavoro Standard dell'Assistente

<table align="center">
<tr align="center">
<th><p align="center">Modalità Ingegnere Full-Stack</p></th>
<th><p align="center">Logging e Pianificazione</p></th>
<th><p align="center">Ricerca Web e Apprendimento</p></th>
</tr>
<tr>
<td align="center"><p align="center"><img src="../../assets/rhizome_code.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_memory.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_search.gif" width="240" height="180"></p></td>
</tr>
<tr>
<td align="center">Sviluppare · Deployare · Scalar</td>
<td align="center">Schedulare · Automatizzare · Ricordare</td>
<td align="center">Scoprire · Insight · Trend</td>
</tr>
</table>

### 🐜 Deploy Innovativo a Bassa Impronta

Rhizome può essere distribuito su un'ampia gamma di dispositivi Linux ed embedded!

- $15 [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) (o [Zero 2 W](https://www.raspberrypi.com/products/raspberry-pi-zero-2-w/)), per un assistente domestico minimale
- $50~70 [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/), per uso embedded basato su RISC-V
- $100 [NanoKVM-Pro](https://www.aliexpress.com/item/1005010048471263.html), per operazioni server automatizzate
- $100 [MaixCAM2](https://www.kickstarter.com/projects/zepan/maixcam2-build-your-next-gen-4k-ai-camera), per sorveglianza intelligente

> Vedi la [Lista di Compatibilità Hardware](../guides/hardware-compatibility.md) per l'elenco completo delle schede verificate e i requisiti attuali a due livelli.

<https://private-user-images.githubusercontent.com/83055338/547056448-e7b031ff-d6f5-4468-bcca-5726b6fecb5c.mp4>

🌟 Ulteriori Casi di Deploy in Arrivo!

## 📦 Installazione

### Scarica da GitHub Releases (Consigliato)

Visita la pagina [GitHub Releases](https://github.com/stpinkie/rhizome/releases) e scarica il binario per la tua piattaforma.

### Scarica il binario precompilato

In alternativa, scarica il binario per la tua piattaforma dalla pagina [GitHub Releases](https://github.com/stpinkie/rhizome/releases).

### Compila dai sorgenti (per lo sviluppo)

Prerequisiti:

- Go 1.25+
- Node.js 22+ e pnpm 10.33.0+ per i build di Web UI / launcher

```bash
git clone https://github.com/stpinkie/rhizome.git

cd rhizome
make deps

# Installa le dipendenze frontend
(cd web/frontend && pnpm install --frozen-lockfile)

# Build del binario core per la piattaforma corrente
make build

# Build del Web UI Launcher (necessario per la modalità WebUI)
make build-launcher

# Build dei binari core per tutte le piattaforme gestite dal Makefile
make build-all

# Build per Raspberry Pi Zero 2 W
# 32-bit: make build-linux-arm
# 64-bit: make build-linux-arm64
make build-pi-zero

# Build e installazione
make install
```

**Raspberry Pi Zero 2 W:** Usa il binario corrispondente al tuo SO: Raspberry Pi OS 32-bit -> `make build-linux-arm`; 64-bit -> `make build-linux-arm64`. Oppure esegui `make build-pi-zero` per buildare entrambi.

## 🚀 Guida Rapida

### 🌐 WebUI Launcher (Consigliato per Desktop)

WebUI Launcher fornisce un'interfaccia basata su browser per configurazione e chat. È il modo più semplice per iniziare — nessuna conoscenza della riga di comando richiesta.

**Opzione 1: Doppio clic (Desktop)**

Dopo aver scaricato da [GitHub Releases](https://github.com/stpinkie/rhizome/releases), fai doppio clic su `rhizome-launcher` (o `rhizome-launcher.exe` su Windows). Il browser si aprirà automaticamente su `http://localhost:18800`.

**Opzione 2: Riga di comando**

```bash
rhizome-launcher
# Apri http://localhost:18800 nel browser
```

> [!TIP]
> **Accesso remoto / Docker / VM:** Aggiungi il flag `-public` per ascoltare su tutte le interfacce:
> ```bash
> rhizome-launcher -public
> ```

<p align="center">
<img src="../../assets/launcher-webui.jpg" alt="WebUI Launcher" width="600">
</p>

**Per iniziare:**

Apri il WebUI, poi: **1)** Configura un Provider (aggiungi la tua API key LLM) → **2)** Configura un Channel (es. Telegram) → **3)** Avvia il Gateway → **4)** Chatta!

Per la documentazione dettagliata, vedi la [cartella docs/](https://github.com/stpinkie/rhizome/tree/main/docs) in questo repo.

<details>
<summary><b>Docker (alternativa)</b></summary>

```bash
# 1. Clona questo repo
git clone https://github.com/stpinkie/rhizome.git
cd rhizome

# 2. Prima esecuzione — genera automaticamente docker/data/config.json poi esce
#    (si attiva solo quando sia config.json che workspace/ sono assenti)
docker compose -f docker/docker-compose.yml --profile launcher up
# Il container stampa "First-run setup complete." e si ferma.

# 3. Imposta le tue API key
vim docker/data/config.json

# 4. Avvia
docker compose -f docker/docker-compose.yml --profile launcher up -d
# Apri http://localhost:18800
```

> **Utenti Docker / VM:** Il Gateway ascolta su `127.0.0.1` di default. Imposta `RHIZOME_GATEWAY_HOST=0.0.0.0` o usa il flag `-public` per renderlo accessibile dall'host.

```bash
# Controlla i log
docker compose -f docker/docker-compose.yml logs -f

# Ferma
docker compose -f docker/docker-compose.yml --profile launcher down

# Aggiorna
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

</details>

<details>
<summary><b>macOS — Avviso di sicurezza al primo avvio</b></summary>

macOS potrebbe bloccare `rhizome-launcher` al primo avvio perché è stato scaricato da internet e non è notarizzato tramite il Mac App Store.

**Passo 1:** Fai doppio clic su `rhizome-launcher`. Vedrai un avviso di sicurezza:

<p align="center">
<img src="../../assets/macos-gatekeeper-warning.jpg" alt="Avviso macOS Gatekeeper" width="400">
</p>

> *"rhizome-launcher" non aperto — Apple non ha potuto verificare che "rhizome-launcher" sia libero da malware che potrebbe danneggiare il tuo Mac o compromettere la tua privacy.*

**Passo 2:** Apri **Impostazioni di Sistema** → **Privacy e Sicurezza** → scorri fino alla sezione **Sicurezza** → clicca **Apri comunque** → conferma cliccando **Apri comunque** nel dialogo.

<p align="center">
<img src="../../assets/macos-gatekeeper-allow.jpg" alt="macOS Privacy & Sicurezza — Apri comunque" width="600">
</p>

Dopo questo passo una tantum, `rhizome-launcher` si aprirà normalmente nei lanci successivi.

</details>

<a id="-run-on-old-android-phones"></a>
### 📱 Android

Dai una seconda vita al tuo vecchio telefono! Trasformalo in un Assistente AI Intelligente con Rhizome.

**Opzione 1: Installazione APK**

Anteprima:

<table>
  <tr>
    <td><img src="../../assets/fui_main_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_web_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_log_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_setting_page.jpg" width="200"></td>
  </tr>
</table>

L'APK Android non è attualmente pubblicato da questo fork; compila dai sorgenti o controlla i [GitHub Releases](https://github.com/stpinkie/rhizome/releases) per un APK futuro.

**Opzione 2: Termux**

Per una lista di controllo completa dell'installazione da riga di comando, vedi la [Guida Android Termux](../guides/android-termux.md).

<details>
<summary><b>Terminal Launcher (per ambienti con risorse limitate)</b></summary>

1. Installa [Termux](https://github.com/termux/termux-app) (scarica dai [GitHub Releases](https://github.com/termux/termux-app/releases), o cerca in F-Droid / Google Play)
2. Esegui i seguenti comandi:

```bash
# Scarica l'ultimo release
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz
pkg install proot
termux-chroot ./rhizome onboard   # chroot fornisce un layout standard del filesystem Linux
```

Poi segui la sezione Terminal Launcher qui sotto per completare la configurazione.

<img src="../../assets/termux.jpg" alt="Rhizome on Termux" width="512">

Per ambienti minimi in cui è disponibile solo il binario core `rhizome` (senza Launcher UI), puoi configurare tutto tramite riga di comando e un file di configurazione JSON.

**1. Inizializza**

```bash
rhizome onboard
```

Questo crea `~/.rhizome/config.json` e la directory workspace.

**2. Configura** (`~/.rhizome/config.json`)

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
      // api_key è ora caricata da .security.yml
    }
  ]
}
```

> Per un modello di configurazione completo con tutte le opzioni disponibili, vedi `config/config.example.json` nel repo.
>
> Nota: config.example.json è nel formato version 0, con codici sensibili, e verrà migrato automaticamente alla versione 1+; poi config.json conterrà solo dati non sensibili, mentre i codici sensibili saranno in .security.yml. Se devi modificare manualmente i codici, vedi `docs/security/security_configuration.md`.

**3. Chat**

```bash
# Una domanda una tantum
rhizome agent -m "What is 2+2?"

# Modalità interattiva
rhizome agent

# Avvia il gateway per l'integrazione con l'app di chat
rhizome gateway
```

</details>

## 🔌 Provider (LLM)

Rhizome supporta 30+ provider LLM tramite la configurazione `model_list`. Usa il formato `protocollo/modello`:

| Provider | Protocollo | API Key | Note |
|----------|------------|---------|------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | Richiesta | GPT-5.4, GPT-4o, o3, ecc. |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | Richiesta | Claude Opus 4.6, Sonnet 4.6, ecc. |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | Richiesta | Gemini 3 Flash, 2.5 Pro, ecc. |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | Richiesta | 200+ modelli, API unificata |
| [Zhipu (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | Richiesta | GLM-4.7, GLM-5, ecc. |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | Richiesta | DeepSeek-V3, DeepSeek-R1 |
| [Volcengine](https://console.volcengine.com) | `volcengine/` | Richiesta | Doubao, modelli Ark |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | Richiesta | Qwen3, Qwen-Max, ecc. |
| [Groq](https://console.groq.com/keys) | `groq/` | Richiesta | Inferenza veloce (Llama, Mixtral) |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | Richiesta | Modelli Kimi |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | Richiesta | Modelli MiniMax |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | Richiesta | Mistral Large, Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | Richiesta | Modelli ospitati NVIDIA |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | Richiesta | Inferenza veloce |
| [Novita AI](https://novita.ai/) | `novita/` | Richiesta | Vari modelli open |
| [Xiaomi MiMo](https://platform.xiaomimimo.com/) | `mimo/` | Richiesta | Modelli MiMo |
| [Ollama](https://ollama.com/) | `ollama/` | Non necessaria | Modelli locali, self-hosted |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | Non necessaria | Deploy locale, compatibile OpenAI |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | Variabile | Proxy per 100+ provider |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | Richiesta | Deploy Azure enterprise |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | Login con device code |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |

<details>
<summary><b>Deploy locale (Ollama, vLLM, ecc.)</b></summary>

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

Per i dettagli completi sulla configurazione dei provider, vedi [Provider & Modelli](../guides/providers.md).

</details>

## 💬 Channel (App di Chat)

Parla con il tuo Rhizome attraverso 17+ piattaforme di messaggistica:

| Channel | Configurazione | Protocollo | Docs |
|---------|----------------|------------|------|
| **Telegram** | Facile (bot token) | Long polling | [Guida](../channels/telegram/README.md) |
| **Discord** | Facile (bot token + intents) | WebSocket | [Guida](../channels/discord/README.md) |
| **WhatsApp** | Facile (QR scan o bridge URL) | Nativo / Bridge | [Guida](../guides/chat-apps.md#whatsapp) |
| **Weixin** | Facile (scan QR nativo) | iLink API | [Guida](../guides/chat-apps.md#weixin) |
| **QQ** | Facile (AppID + AppSecret) | WebSocket | [Guida](../channels/qq/README.md) |
| **Slack** | Facile (bot + app token) | Socket Mode | [Guida](../channels/slack/README.md) |
| **Matrix** | Medio (homeserver + token) | Sync API | [Guida](../channels/matrix/README.md) |
| **DingTalk** | Medio (credenziali client) | Stream | [Guida](../channels/dingtalk/README.md) |
| **Feishu / Lark** | Medio (App ID + Secret) | WebSocket/SDK | [Guida](../channels/feishu/README.md) |
| **LINE** | Medio (credenziali + webhook) | Webhook | [Guida](../channels/line/README.md) |
| **WeCom** | Facile (login QR o manuale) | WebSocket | [Guida](../channels/wecom/README.md) |
| **IRC** | Medio (server + nick) | Protocollo IRC | [Guida](../guides/chat-apps.md#irc) |
| **OneBot** | Medio (WebSocket URL) | OneBot v11 | [Guida](../channels/onebot/README.md) |
| **MaixCam** | Facile (abilita) | TCP socket | [Guida](../channels/maixcam/README.md) |
| **Pico** | Facile (abilita) | Protocollo nativo | Integrato |
| **Pico Client** | Facile (WebSocket URL) | WebSocket | Integrato |

> Tutti i channel basati su webhook condividono un singolo server HTTP Gateway (`gateway.host`:`gateway.port`, default `127.0.0.1:18790`). Feishu usa la modalità WebSocket/SDK e non usa il server HTTP condiviso.

> La verbosità dei log è controllata da `gateway.log_level` (default: `warn`). Valori supportati: `debug`, `info`, `warn`, `error`, `fatal`. Può essere impostato anche tramite `RHIZOME_LOG_LEVEL`. Vedi [Configurazione](../guides/configuration.md#gateway-log-level) per i dettagli.

Per istruzioni dettagliate sulla configurazione dei channel, vedi [Configurazione App di Chat](../guides/chat-apps.md).

## 🔧 Strumenti

### 🔍 Ricerca Web

Rhizome può cercare sul web per fornire informazioni aggiornate. Configura in `tools.web`:

| Motore di Ricerca | API Key | Piano Gratuito | Link |
|-------------------|---------|----------------|------|
| DuckDuckGo | Non necessaria | Illimitato | Fallback integrato |
| [Baidu Search](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | Richiesta | 1500 query/mese (allocazione giornaliera) | IA, ottimizzato per il cinese |
| [Tavily](https://tavily.com) | Richiesta | 1000 query/mese | Ottimizzato per AI Agent |
| [Brave Search](https://brave.com/search/api) | Richiesta | 2000 query/mese | Veloce e privato |
| [Perplexity](https://www.perplexity.ai) | Richiesta | A pagamento | Ricerca potenziata dall'IA |
| [SearXNG](https://github.com/searxng/searxng) | Non necessaria | Self-hosted | Metasearch engine gratuito |
| [GLM Search](https://open.bigmodel.cn/) | Richiesta | Variabile | Ricerca web Zhipu |

### ⚙️ Altri Strumenti

Rhizome include strumenti integrati per operazioni su file, esecuzione di codice, pianificazione e altro. Vedi [Configurazione degli Strumenti](../reference/tools_configuration.md) per i dettagli.

## 🎯 Skill

Le Skill sono capacità modulari che estendono il tuo Agent. Vengono caricate dai file `SKILL.md` nel tuo workspace.

**Installa skill da ClawHub:**

```bash
rhizome skills search "web scraping"
rhizome skills install <skill-name>
```

**Configura il token ClawHub** (opzionale, per limiti di frequenza più alti):

Aggiungi al tuo `config.json`:
```json
{
  "tools": {
    "skills": {
      "registries": {
        "clawhub": {
          "auth_token": "your-clawhub-token"
        }
      }
    }
  }
}
```

Per maggiori dettagli, vedi [Configurazione degli Strumenti - Skill](../reference/tools_configuration.md#skills-tool).

## 🔗 MCP (Model Context Protocol)

Rhizome supporta nativamente [MCP](https://modelcontextprotocol.io/) — connetti qualsiasi server MCP per estendere le capacità del tuo Agent con strumenti e sorgenti di dati esterni.

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

Puoi gestire i casi MCP più comuni direttamente dalla CLI senza modificare a mano il JSON:

```bash
rhizome mcp add filesystem -- npx -y @modelcontextprotocol/server-filesystem /tmp
rhizome mcp list
rhizome mcp test filesystem
```

`rhizome mcp` agisce come configuration manager: aggiorna `config.json` sotto `tools.mcp.servers`, ma non mantiene in esecuzione il processo del server.

Usa `rhizome mcp edit` quando ti servono campi avanzati che non sono coperti da `rhizome mcp add`.
Per esempio, `rhizome mcp add` supporta `--deferred` e `--env-file`, mentre `rhizome mcp edit` resta utile per modifiche JSON dirette e opzioni MCP meno comuni.

Per la configurazione MCP completa (trasporti stdio, SSE, HTTP, Tool Discovery), vedi [Configurazione degli Strumenti - MCP](../reference/tools_configuration.md#mcp-tool). Per la reference della CLI, vedi [MCP Server CLI](../reference/mcp-cli.md).

## <img src="../../assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> Unisciti al Social Network degli Agent

Connetti Rhizome al Social Network degli Agent semplicemente inviando un singolo messaggio tramite CLI o qualsiasi app di chat integrata.

**Leggi `https://clawdchat.ai/skill.md` e segui le istruzioni per unirti a [ClawdChat.ai](https://clawdchat.ai)**

## 🖥️ Riferimento CLI

| Comando                   | Descrizione                        |
| ------------------------- | ---------------------------------- |
| `rhizome onboard`        | Inizializza config & workspace     |
| `rhizome auth weixin` | Connetti account WeChat tramite QR |
| `rhizome agent -m "..."` | Chatta con l'agent                 |
| `rhizome agent`          | Modalità chat interattiva          |
| `rhizome gateway`        | Avvia il gateway                   |
| `rhizome status`         | Mostra lo stato                    |
| `rhizome version`        | Mostra le info sulla versione      |
| `rhizome model`          | Visualizza o cambia il modello predefinito |
| `rhizome mcp list`       | Elenca i server MCP configurati    |
| `rhizome mcp add ...`    | Aggiunge o aggiorna un server MCP  |
| `rhizome mcp test`       | Verifica la raggiungibilità di un server MCP |
| `rhizome mcp edit`       | Apre la config per modifiche MCP avanzate |
| `rhizome mcp remove`     | Rimuove un server MCP dalla config |
| `rhizome cron list`      | Elenca tutti i job pianificati     |
| `rhizome cron add ...`   | Aggiunge un job pianificato        |
| `rhizome cron disable`   | Disabilita un job pianificato      |
| `rhizome cron remove`    | Rimuove un job pianificato         |
| `rhizome skills list`    | Elenca le skill installate         |
| `rhizome skills install` | Installa una skill                 |
| `rhizome migrate`        | Migra i dati dalle versioni precedenti |
| `rhizome auth login`     | Autenticazione con i provider          |

### ⏰ Task Pianificati / Promemoria

Rhizome supporta promemoria pianificati e task ricorrenti tramite lo strumento `cron`:

* **Promemoria una tantum**: "Ricordami tra 10 minuti" -> si attiva una volta dopo 10 min
* **Task ricorrenti**: "Ricordami ogni 2 ore" -> si attiva ogni 2 ore
* **Espressioni cron**: "Ricordami alle 9 ogni giorno" -> usa un'espressione cron

## 📚 Documentazione

Per guide dettagliate oltre questo README:

| Argomento | Descrizione |
|-----------|-------------|
| [Docker & Avvio Rapido](../guides/docker.md) | Configurazione Docker Compose, modalità Launcher/Agent |
| [App di Chat](../guides/chat-apps.md) | Tutte le guide di configurazione per 17+ channel |
| [Configurazione](../guides/configuration.md) | Variabili d'ambiente, struttura del workspace, sandbox di sicurezza |
| [MCP Server CLI](../reference/mcp-cli.md) | Aggiunta, elenco, test, modifica e rimozione dei server MCP da CLI |
| [Provider & Modelli](../guides/providers.md) | 30+ provider LLM, routing dei modelli, configurazione model_list |
| [Spawn & Task Asincroni](../guides/spawn-tasks.md) | Task veloci, task lunghi con spawn, orchestrazione asincrona di sub-agent |
| [Hooks](../architecture/hooks/README.md) | Sistema di hook event-driven: observer, interceptor, approval hook |
| [Steering](../architecture/steering.md) | Iniettare messaggi in un loop agent in esecuzione |
| [SubTurn](../architecture/subturn.md) | Coordinamento subagent, controllo concorrenza, ciclo di vita |
| [Risoluzione Problemi](../operations/troubleshooting.md) | Problemi comuni e soluzioni |
| [Configurazione degli Strumenti](../reference/tools_configuration.md) | Abilitazione/disabilitazione per strumento, politiche exec, MCP, Skill |
| [Compatibilità Hardware](../guides/hardware-compatibility.md) | Schede testate, requisiti minimi |

## 🤝 Contribuisci & Roadmap

Le PR sono benvenute! Il codice è volutamente piccolo e leggibile.

Consulta la nostra [Roadmap della Community](https://github.com/stpinkie/rhizome/issues/988) e [CONTRIBUTING.md](../../CONTRIBUTING.md) per le linee guida.

Gruppo sviluppatori in costruzione, unisciti dopo la tua prima PR accettata!

Gruppi utenti:

Discord: <https://discord.gg/V4sAZ9XWpN>

WeChat:
<img src="../../assets/wechat.png" alt="WeChat group QR code" width="512">