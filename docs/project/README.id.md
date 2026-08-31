<div align="center">
<img src="../../assets/logo.webp" alt="Rhizome" width="512">

<h1>Rhizome: Asisten AI Ultra-Efisien Berbasis Go</h1>

<h3>Perangkat Keras $10 · 10MB RAM · Boot ms · Let's Go, Rhizome!</h3>
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

[中文](README.zh.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Italiano](README.it.md) | **Bahasa Indonesia** | [Malay](README.ms.md) | [English](../../README.md)

</div>

---

> **Rhizome** adalah hard fork yang dikelola komunitas dari [PicoClaw](https://github.com/sipeed/picoclaw). Dibuat sepenuhnya dalam **Go** dan melanjutkan tujuan menjadi asisten AI pribadi ultra-ringan.

**Rhizome** adalah asisten AI pribadi yang terinspirasi oleh [NanoBot](https://github.com/HKUDS/nanobot). Ini menambahkan mesh P2P Go, sinkronisasi ruang kerja, dan gateway agen di atas ide PicoClaw asli.

**Satu binary Go tunggal, tanpa dependensi runtime** — berjalan secara native di Linux, Windows, macOS, FreeBSD/NetBSD, dan Android. Lihat [Daftar Kompatibilitas Perangkat Keras](../guides/hardware-compatibility.md) untuk papan yang terverifikasi dan persyaratan sumber daya dua tingkat saat ini.

<p align="center">
<img src="../../assets/rhizome_mem.gif" width="360" height="240">
</p>

> [!CAUTION]
> **Pemberitahuan Keamanan**
>
> * **TIDAK ADA CRYPTO:** Rhizome **belum** menerbitkan token atau mata uang kripto resmi. Setiap klaim di `pump.fun` atau platform perdagangan lainnya adalah **penipuan**.
> * **CANONICAL SOURCE:** Sumber dan lokasi rilis resmi adalah **<https://github.com/stpinkie/rhizome>**; rilis dipublikasikan di GitHub Releases. Waspadai domain pihak ketiga yang mengklaim resmi.
> * **Peringatan:** Banyak domain `.ai/.org/.com/.net/...` telah didaftarkan oleh pihak ketiga. Jangan percaya.
> * **Catatan:** Rhizome sedang dalam tahap pengembangan awal yang cepat. Mungkin ada masalah keamanan yang belum terselesaikan. Jangan deploy ke produksi sebelum v1.0.
> * **Catatan:** Binary `rhizome` lengkap sekitar 98 MB dan daemon menggunakan sekitar 60 MB memori pribadi. Kami berencana membangun versi `nonetwork` untuk lebih mengurangi jejak di papan ultra-kecil. Optimasi sumber daya direncanakan setelah fitur stabil.

## 📢 Berita

2026-05-28 🚀 **v0.2.9 dirilis!** Manajemen server MCP di Web UI, pencarian web Sogou yang dapat dikonfigurasi, animasi umpan balik alat saluran, nilai default `pretty_print` dan `disable_escape_html`, serta berbagai perbaikan bug pada provider dan saluran.

2026-05-14 🚀 **v0.2.8 dirilis!** Perintah MCP CLI (`show`, `add`, `list`, `remove`, `test`, `edit`), objek kosong menggantikan null untuk parameter alat MCP, dan perbaikan build.

2026-05-07 🚀 **v0.2.7 dirilis!** Pencarian web Sogou yang dapat dikonfigurasi, animasi umpan balik alat saluran, perbaikan linter.

2026-04-23 🚀 **v0.2.6 dirilis!** Hook dengan tindakan respons dan dokumentasi lengkap, dukungan isolasi, perbaikan banner bantuan.

2026-04-11 🚀 **v0.2.5 dirilis!** Zoneinfo dari variabel lingkungan TZ/ZONEINFO, penyelarasan rendering Matrix CommonMark, `read_file` per baris.

2026-03-31 📱 **Dukungan Android!** Rhizome sekarang berjalan di Android! APK Android tidak didistribusikan dari fork ini; build dari sumber atau periksa [GitHub Releases](https://github.com/stpinkie/rhizome/releases) untuk APK di masa depan.

2026-03-25 🚀 **v0.2.4 dirilis!** Restrukturisasi arsitektur Agen (SubTurn, Hook, Steering, EventBus), integrasi mendalam WeChat/WeCom, peningkatan keamanan (.security.yml, penyaringan data sensitif), provider baru (AWS Bedrock, Azure, Xiaomi MiMo) dan 35 perbaikan bug. Rhizome mencapai **26K Bintang**!

2026-03-17 🚀 **v0.2.3 dirilis!** UI baki sistem (Windows & Linux), kueri status sub-agen (`spawn_status`), hot-reload Gateway eksperimental, pengaman keamanan Cron, dan 2 perbaikan keamanan. Rhizome mencapai **25K Bintang**!

2026-03-09 🎉 **v0.2.1 — Pembaruan terbesar sejauh ini!** Dukungan protokol MCP, 4 saluran baru (Matrix/IRC/WeCom/Discord Proxy), 3 provider baru (Kimi/Minimax/Avian), pipeline penglihatan, penyimpanan memori JSONL, perutean model.

2026-02-28 📦 **v0.2.0** dirilis dengan dukungan Docker Compose dan Web UI Launcher.

<details>
<summary>Berita sebelumnya...</summary>

2026-02-26 🎉 Rhizome mencapai **20K Bintang** dalam hanya 17 hari! Orkestrasi saluran otomatis dan antarmuka kemampuan kini tersedia.

2026-02-16 🎉 Rhizome melebihi 12K Bintang dalam seminggu! Peran perawat komunitas dan [Peta Jalan](../../ROADMAP.md) secara resmi dirilis.

2026-02-13 🎉 Rhizome melebihi 5000 Bintang dalam 4 hari! Peta jalan proyek dan grup pengembang sedang dibangun.

2026-02-09 🎉 **Rhizome Dirilis!** Dibangun dalam 1 hari untuk menjelajahi Agen AI ultra-ringan. Let's Go, Rhizome!

</details>

## ✨ Fitur

🪶 **Satu binary, tanpa dependensi runtime**: Satu eksekusi Go yang ditautkan secara statis, berjalan di Linux, Windows, macOS, FreeBSD/NetBSD, dan Android.*

💰 **Biaya Minimal**: Cukup efisien untuk berjalan di berbagai papan ARM dan RISC-V berbiaya rendah; lihat [Daftar Kompatibilitas Perangkat Keras](../guides/hardware-compatibility.md).

⚡️ **Boot Kilat**: Berjalan dalam waktu kurang dari 1 detik di papan berbiaya rendah yang terverifikasi.

🌍 **Sangat Portabel**: Satu binary di berbagai arsitektur RISC-V, ARM, MIPS, dan x86. Satu binary, berjalan di mana saja!

🤖 **Di-bootstrap oleh AI**: Implementasi native murni Go — 95% kode inti dibuat oleh Agen dan disempurnakan melalui ulasan manusia.

🔌 **Dukungan MCP**: Integrasi native [Model Context Protocol](https://modelcontextprotocol.io/) — hubungkan server MCP apa pun untuk memperluas kemampuan Agen.

👁️ **Pipeline Penglihatan**: Kirim gambar dan file langsung ke Agen — pengkodean base64 otomatis untuk LLM multimodal.

🧠 **Perutean Cerdas**: Perutean model berbasis aturan — kueri sederhana dialihkan ke model ringan, menghemat biaya API.

_*Pengukuran jejak dilakukan di Windows dengan `CGO_ENABLED=0`, tag `goolm,stdjson`, dan `-ldflags "-s -w"`; binary yang di-strip sekitar 98 MB. Kami berencana membangun versi `nonetwork` untuk lebih mengurangi di papan ultra-kecil._

<div align="center">

### Jejak Build Saat Ini

| Moda | Kasus Penggunaan | Total RAM | RAM Bebas | Penyimpanan |
|------|------------------|-----------|-----------|-------------|
| **Dasar** | `rhizome agent`, `rhizome onboard` satu kali | 256 MB | 128 MB | 128 MB |
| **Lengkap** | `rhizome daemon` dengan P2P, syncer, dan gateway | 512 MB | 256 MB | 128 MB |

</div>

> **[Daftar Kompatibilitas Perangkat Keras](../guides/hardware-compatibility.md)** — Lihat semua papan yang diuji, dari Raspberry Pi ke ponsel Android. Papan Anda tidak terdaftar? Kirim PR!

<p align="center">
<img src="../../assets/hardware-banner.jpg" alt="Rhizome Hardware Compatibility" width="100%">
</p>

## 🦾 Demonstrasi

### 🛠️ Alur Kerja Asisten Standar

<table align="center">
<tr align="center">
<th><p align="center">Mode Insinyur Full-Stack</p></th>
<th><p align="center">Pencatatan & Perencanaan</p></th>
<th><p align="center">Pencarian Web & Pembelajaran</p></th>
</tr>
<tr>
<td align="center"><p align="center"><img src="../../assets/rhizome_code.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_memory.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_search.gif" width="240" height="180"></p></td>
</tr>
<tr>
<td align="center">Kembangkan · Deploy · Skalakan</td>
<td align="center">Jadwalkan · Otomatisasi · Ingat</td>
<td align="center">Temukan · Wawasan · Tren</td>
</tr>
</table>

### 🐜 Deploy Inovatif dengan Footprint Rendah

Rhizome dapat di-deploy di berbagai perangkat Linux dan tertanam!

- $15 [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) (atau [Zero 2 W](https://www.raspberrypi.com/products/raspberry-pi-zero-2-w/)), untuk asisten rumah minimal
- $50~70 [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/), untuk penggunaan tertanam berbasis RISC-V
- $100 [NanoKVM-Pro](https://www.aliexpress.com/item/1005010048471263.html), untuk operasi server otomatis
- $100 [MaixCAM2](https://www.kickstarter.com/projects/zepan/maixcam2-build-your-next-gen-4k-ai-camera), untuk pengawasan pintar

> Lihat [Daftar Kompatibilitas Perangkat Keras](../guides/hardware-compatibility.md) untuk daftar lengkap papan yang terverifikasi dan persyaratan dua tingkat saat ini.

<https://private-user-images.githubusercontent.com/83055338/547056448-e7b031ff-d6f5-4468-bcca-5726b6fecb5c.mp4>

🌟 Lebih Banyak Kasus Deploy Menunggu!

## 📦 Instalasi

### Unduh dari GitHub Releases (Direkomendasikan)

Kunjungi halaman [GitHub Releases](https://github.com/stpinkie/rhizome/releases) dan unduh binary untuk platform Anda.

### Unduh binary yang sudah dikompilasi

Sebagai alternatif, unduh binary untuk platform Anda dari halaman [GitHub Releases](https://github.com/stpinkie/rhizome/releases).

### Build dari source (untuk pengembangan)

Prasyarat:

- Go 1.25+
- Node.js 22+ dan pnpm 10.33.0+ untuk build Web UI / launcher

```bash
git clone https://github.com/stpinkie/rhizome.git

cd rhizome
make deps

# Instal dependensi frontend
(cd web/frontend && pnpm install --frozen-lockfile)

# Build binary inti untuk platform saat ini
make build

# Build Web UI Launcher (diperlukan untuk mode WebUI)
make build-launcher

# Build binary inti untuk semua platform yang dikelola Makefile
make build-all

# Build untuk Raspberry Pi Zero 2 W
# 32-bit: make build-linux-arm
# 64-bit: make build-linux-arm64
make build-pi-zero

# Build dan pasang
make install
```

**Raspberry Pi Zero 2 W:** Gunakan binary yang cocok dengan OS Anda: Raspberry Pi OS 32-bit -> `make build-linux-arm`; 64-bit -> `make build-linux-arm64`. Atau jalankan `make build-pi-zero` untuk membangun keduanya.

## 🚀 Panduan Memulai Cepat

### 🌐 WebUI Launcher (Direkomendasikan untuk Desktop)

WebUI Launcher menyediakan antarmuka berbasis browser untuk konfigurasi dan chat. Ini adalah cara termudah untuk memulai — tidak memerlukan pengetahuan baris perintah.

**Opsi 1: Klik dua kali (Desktop)**

Setelah mengunduh dari [GitHub Releases](https://github.com/stpinkie/rhizome/releases), klik dua kali `rhizome-launcher` (atau `rhizome-launcher.exe` di Windows). Browser Anda akan terbuka otomatis di `http://localhost:18800`.

**Opsi 2: Baris perintah**

```bash
rhizome-launcher
# Buka http://localhost:18800 di browser Anda
```

> [!TIP]
> **Akses jarak jauh / Docker / VM:** Tambahkan flag `-public` untuk mendengarkan di semua antarmuka:
> ```bash
> rhizome-launcher -public
> ```

<p align="center">
<img src="../../assets/launcher-webui.jpg" alt="WebUI Launcher" width="600">
</p>

**Memulai:**

Buka WebUI, lalu: **1)** Konfigurasikan Provider (tambahkan kunci API LLM Anda) → **2)** Konfigurasikan Channel (mis. Telegram) → **3)** Mulai Gateway → **4)** Chat!

Untuk dokumentasi terperinci, lihat [folder docs/](https://github.com/stpinkie/rhizome/tree/main/docs) di repo ini.

<details>
<summary><b>Docker (alternatif)</b></summary>

```bash
# 1. Clone repo ini
git clone https://github.com/stpinkie/rhizome.git
cd rhizome

# 2. Jalankan pertama — membuat docker/data/config.json secara otomatis lalu keluar
#    (hanya dipicu ketika config.json dan workspace/ keduanya tidak ada)
docker compose -f docker/docker-compose.yml --profile launcher up
# Kontainer mencetak "First-run setup complete." dan berhenti.

# 3. Tetapkan kunci API Anda
vim docker/data/config.json

# 4. Mulai
docker compose -f docker/docker-compose.yml --profile launcher up -d
# Buka http://localhost:18800
```

> **Pengguna Docker / VM:** Gateway mendengarkan di `127.0.0.1` secara default. Atur `RHIZOME_GATEWAY_HOST=0.0.0.0` atau gunakan flag `-public` untuk membuatnya dapat diakses dari host.

```bash
# Periksa log
docker compose -f docker/docker-compose.yml logs -f

# Hentikan
docker compose -f docker/docker-compose.yml --profile launcher down

# Perbarui
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

</details>

<details>
<summary><b>macOS — Peringatan Keamanan Peluncuran Pertama</b></summary>

macOS mungkin memblokir `rhizome-launcher` saat pertama kali diluncurkan karena diunduh dari internet dan tidak dinotarisasi melalui Mac App Store.

**Langkah 1:** Klik dua kali `rhizome-launcher`. Anda akan melihat peringatan keamanan:

<p align="center">
<img src="../../assets/macos-gatekeeper-warning.jpg" alt="Peringatan macOS Gatekeeper" width="400">
</p>

> *"rhizome-launcher" Tidak Dibuka — Apple tidak dapat memverifikasi "rhizome-launcher" bebas dari malware yang dapat membahayakan Mac atau mengganggu privasi Anda.*

**Langkah 2:** Buka **Pengaturan Sistem** → **Privasi & Keamanan** → gulir ke bagian **Keamanan** → klik **Buka Juga** → konfirmasi dengan mengklik **Buka Juga** pada dialog.

<p align="center">
<img src="../../assets/macos-gatekeeper-allow.jpg" alt="macOS Privasi & Keamanan — Buka Juga" width="600">
</p>

Setelah langkah satu kali ini, `rhizome-launcher` akan terbuka secara normal pada peluncuran berikutnya.

</details>

<a id="-run-on-old-android-phones"></a>
### 📱 Android

Beri ponsel lama Anda kehidupan kedua! Ubah menjadi Asisten AI Pintar dengan Rhizome.

**Opsi 1: Instal APK**

Pratinjau:

<table>
  <tr>
    <td><img src="../../assets/fui_main_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_web_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_log_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_setting_page.jpg" width="200"></td>
  </tr>
</table>

APK Android saat ini tidak didistribusikan dari fork ini; build dari sumber atau periksa [GitHub Releases](https://github.com/stpinkie/rhizome/releases) untuk APK di masa depan.

**Opsi 2: Termux**

Untuk daftar periksa penyiapan baris perintah lengkap, lihat [Panduan Android Termux](../guides/android-termux.md).

<details>
<summary><b>Terminal Launcher (untuk lingkungan dengan sumber daya terbatas)</b></summary>

1. Instal [Termux](https://github.com/termux/termux-app) (unduh dari [GitHub Releases](https://github.com/termux/termux-app/releases), atau cari di F-Droid / Google Play)
2. Jalankan perintah berikut:

```bash
# Unduh rilis terbaru
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz
pkg install proot
termux-chroot ./rhizome onboard   # chroot menyediakan tata letak sistem file Linux standar
```

Kemudian ikuti bagian Terminal Launcher di bawah untuk menyelesaikan konfigurasi.

<img src="../../assets/termux.jpg" alt="Rhizome on Termux" width="512">

Untuk lingkungan minimal di mana hanya binary inti `rhizome` yang tersedia (tanpa Launcher UI), Anda dapat mengonfigurasi semuanya melalui baris perintah dan file konfigurasi JSON.

**1. Inisialisasi**

```bash
rhizome onboard
```

Ini membuat `~/.rhizome/config.json` dan direktori workspace.

**2. Konfigurasi** (`~/.rhizome/config.json`)

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
      // api_key sekarang dimuat dari .security.yml
    }
  ]
}
```

> Untuk templat konfigurasi lengkap dengan semua opsi yang tersedia, lihat `config/config.example.json` di repo.
>
> Perhatikan: config.example.json adalah format version 0, berisi kode sensitif, dan akan dimigrasikan otomatis ke version 1+; kemudian config.json hanya menyimpan data tidak sensitif, sementara kode sensitif disimpan di .security.yml. Jika perlu mengubah kode secara manual, lihat `docs/security/security_configuration.md`.

**3. Chat**

```bash
# Satu pertanyaan
rhizome agent -m "What is 2+2?"

# Mode interaktif
rhizome agent

# Mulai gateway untuk integrasi aplikasi chat
rhizome gateway
```

</details>

## 🔌 Providers (LLM)

Rhizome mendukung 30+ provider LLM melalui konfigurasi `model_list`. Gunakan format `protocol/model`:

| Provider | Protocol | API Key | Catatan |
|----------|----------|---------|---------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | Diperlukan | GPT-5.4, GPT-4o, o3, dll. |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | Diperlukan | Claude Opus 4.6, Sonnet 4.6, dll. |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | Diperlukan | Gemini 3 Flash, 2.5 Pro, dll. |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | Diperlukan | 200+ model, API terpadu |
| [Zhipu (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | Diperlukan | GLM-4.7, GLM-5, dll. |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | Diperlukan | DeepSeek-V3, DeepSeek-R1 |
| [Volcengine](https://console.volcengine.com) | `volcengine/` | Diperlukan | Doubao, model Ark |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | Diperlukan | Qwen3, Qwen-Max, dll. |
| [Groq](https://console.groq.com/keys) | `groq/` | Diperlukan | Inferensi cepat (Llama, Mixtral) |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | Diperlukan | Model Kimi |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | Diperlukan | Model MiniMax |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | Diperlukan | Mistral Large, Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | Diperlukan | Model yang di-host NVIDIA |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | Diperlukan | Inferensi cepat |
| [Novita AI](https://novita.ai/) | `novita/` | Diperlukan | Berbagai model open |
| [Xiaomi MiMo](https://platform.xiaomimimo.com/) | `mimo/` | Diperlukan | Model MiMo |
| [Ollama](https://ollama.com/) | `ollama/` | Tidak perlu | Model lokal, self-hosted |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | Tidak perlu | Deploy lokal, kompatibel OpenAI |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | Bervariasi | Proxy untuk 100+ provider |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | Diperlukan | Deploy Azure enterprise |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | Login dengan device code |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |

<details>
<summary><b>Deploy lokal (Ollama, vLLM, dll.)</b></summary>

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

Untuk detail konfigurasi provider lengkap, lihat [Providers & Models](../guides/providers.md).

</details>

## 💬 Channels (Aplikasi Chat)

Bicara dengan Rhizome Anda melalui 17+ platform pesan:

| Channel | Pengaturan | Protocol | Dokumentasi |
|---------|------------|----------|-------------|
| **Telegram** | Mudah (bot token) | Long polling | [Panduan](../channels/telegram/README.md) |
| **Discord** | Mudah (bot token + intents) | WebSocket | [Panduan](../channels/discord/README.md) |
| **WhatsApp** | Mudah (scan QR atau bridge URL) | Native / Bridge | [Panduan](../guides/chat-apps.md#whatsapp) |
| **Weixin** | Mudah (scan QR native) | iLink API | [Panduan](../guides/chat-apps.md#weixin) |
| **QQ** | Mudah (AppID + AppSecret) | WebSocket | [Panduan](../channels/qq/README.md) |
| **Slack** | Mudah (bot + app token) | Socket Mode | [Panduan](../channels/slack/README.md) |
| **Matrix** | Sedang (homeserver + token) | Sync API | [Panduan](../channels/matrix/README.md) |
| **DingTalk** | Sedang (client credentials) | Stream | [Panduan](../channels/dingtalk/README.md) |
| **Feishu / Lark** | Sedang (App ID + Secret) | WebSocket/SDK | [Panduan](../channels/feishu/README.md) |
| **LINE** | Sedang (credentials + webhook) | Webhook | [Panduan](../channels/line/README.md) |
| **WeCom** | Mudah (login QR atau manual) | WebSocket | [Panduan](../channels/wecom/README.md) |
| **IRC** | Sedang (server + nick) | IRC protocol | [Panduan](../guides/chat-apps.md#irc) |
| **OneBot** | Sedang (WebSocket URL) | OneBot v11 | [Panduan](../channels/onebot/README.md) |
| **MaixCam** | Mudah (aktifkan) | TCP socket | [Panduan](../channels/maixcam/README.md) |
| **Pico** | Mudah (aktifkan) | Native protocol | Bawaan |
| **Pico Client** | Mudah (WebSocket URL) | WebSocket | Bawaan |

> Semua channel berbasis webhook berbagi satu server HTTP Gateway (`gateway.host`:`gateway.port`, default `127.0.0.1:18790`). Feishu menggunakan mode WebSocket/SDK dan tidak menggunakan server HTTP bersama.

> Verbositas log dikontrol oleh `gateway.log_level` (default: `warn`). Nilai yang didukung: `debug`, `info`, `warn`, `error`, `fatal`. Juga dapat diatur melalui `RHIZOME_LOG_LEVEL`. Lihat [Konfigurasi](../guides/configuration.md#gateway-log-level) untuk detail.

Untuk instruksi pengaturan channel lengkap, lihat [Konfigurasi Aplikasi Chat](../guides/chat-apps.md).

## 🔧 Tools

### 🔍 Pencarian Web

Rhizome dapat mencari web untuk memberikan informasi terkini. Konfigurasi di `tools.web`:

| Mesin Pencari | API Key | Tier Gratis | Tautan |
|--------------|---------|-------------|--------|
| DuckDuckGo | Tidak perlu | Tidak terbatas | Fallback bawaan |
| [Baidu Search](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | Diperlukan | 1500 kueri/bulan (alokasi harian) | Bertenaga AI, dioptimalkan untuk bahasa Mandarin |
| [Tavily](https://tavily.com) | Diperlukan | 1000 kueri/bulan | Dioptimalkan untuk AI Agent |
| [Brave Search](https://brave.com/search/api) | Diperlukan | 2000 kueri/bulan | Cepat dan privat |
| [Perplexity](https://www.perplexity.ai) | Diperlukan | Berbayar | Pencarian bertenaga AI |
| [SearXNG](https://github.com/searxng/searxng) | Tidak perlu | Self-hosted | Mesin metasearch gratis |
| [GLM Search](https://open.bigmodel.cn/) | Diperlukan | Bervariasi | Pencarian web Zhipu |

### ⚙️ Tools Lainnya

Rhizome menyertakan tools bawaan untuk operasi file, eksekusi kode, penjadwalan, dan lainnya. Lihat [Konfigurasi Tools](../reference/tools_configuration.md) untuk detail.

## 🎯 Skills

Skills adalah kapabilitas modular yang memperluas Agent Anda. Dimuat dari file `SKILL.md` di workspace Anda.

**Instal skills dari ClawHub:**

```bash
rhizome skills search "web scraping"
rhizome skills install <skill-name>
```

**Konfigurasi token ClawHub** (opsional, untuk rate limit lebih tinggi):

Tambahkan ke `config.json` Anda:
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

Untuk detail lebih lanjut, lihat [Konfigurasi Tools - Skills](../reference/tools_configuration.md#skills-tool).

## 🔗 MCP (Model Context Protocol)

Rhizome mendukung [MCP](https://modelcontextprotocol.io/) secara native — hubungkan server MCP mana pun untuk memperluas kapabilitas Agent Anda dengan tools dan sumber data eksternal.

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

Untuk konfigurasi MCP lengkap (transport stdio, SSE, HTTP, Tool Discovery), lihat [Konfigurasi Tools - MCP](../reference/tools_configuration.md#mcp-tool).

## <img src="../../assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> Bergabung dengan Jaringan Sosial Agent

Hubungkan Rhizome ke Jaringan Sosial Agent hanya dengan mengirim satu pesan melalui CLI atau Aplikasi Chat terintegrasi mana pun.

**Baca `https://clawdchat.ai/skill.md` dan ikuti instruksi untuk bergabung dengan [ClawdChat.ai](https://clawdchat.ai)**

## 🖥️ Referensi CLI

| Perintah                   | Deskripsi                        |
| -------------------------- | -------------------------------- |
| `rhizome onboard`         | Inisialisasi konfigurasi & workspace |
| `rhizome auth weixin` | Hubungkan akun WeChat via QR |
| `rhizome agent -m "..."` | Chat dengan agent                |
| `rhizome agent`           | Mode chat interaktif             |
| `rhizome gateway`         | Mulai gateway                    |
| `rhizome status`          | Tampilkan status                 |
| `rhizome version`         | Tampilkan info versi             |
| `rhizome model`           | Lihat atau ganti model default   |
| `rhizome cron list`       | Daftar semua tugas terjadwal     |
| `rhizome cron add ...`    | Tambah tugas terjadwal           |
| `rhizome cron disable`    | Nonaktifkan tugas terjadwal      |
| `rhizome cron remove`     | Hapus tugas terjadwal            |
| `rhizome skills list`     | Daftar skill yang terinstal      |
| `rhizome skills install`  | Instal skill                     |
| `rhizome migrate`         | Migrasi data dari versi lama     |
| `rhizome auth login`      | Autentikasi dengan provider      |

### ⏰ Tugas Terjadwal / Pengingat

Rhizome mendukung pengingat terjadwal dan tugas berulang melalui tool `cron`:

* **Pengingat satu kali**: "Ingatkan saya dalam 10 menit" -> terpicu sekali setelah 10 menit
* **Tugas berulang**: "Ingatkan saya setiap 2 jam" -> terpicu setiap 2 jam
* **Ekspresi cron**: "Ingatkan saya jam 9 pagi setiap hari" -> menggunakan ekspresi cron

## 📚 Dokumentasi

Untuk panduan lengkap di luar README ini:

| Topik | Deskripsi |
|-------|-----------|
| [Docker & Panduan Cepat](../guides/docker.md) | Pengaturan Docker Compose, mode Launcher/Agent |
| [Aplikasi Chat](../guides/chat-apps.md) | Semua 17+ panduan pengaturan channel |
| [Konfigurasi](../guides/configuration.md) | Variabel environment, tata letak workspace, sandbox keamanan |
| [Providers & Models](../guides/providers.md) | 30+ provider LLM, routing model, konfigurasi model_list |
| [Spawn & Tugas Async](../guides/spawn-tasks.md) | Tugas cepat, tugas panjang dengan spawn, orkestrasi sub-agent async |
| [Hooks](../architecture/hooks/README.md) | Sistem hook berbasis event: observer, interceptor, approval hook |
| [Steering](../architecture/steering.md) | Menyuntikkan pesan ke dalam loop agent yang sedang berjalan |
| [SubTurn](../architecture/subturn.md) | Koordinasi subagent, kontrol konkurensi, siklus hidup |
| [Pemecahan Masalah](../operations/troubleshooting.md) | Masalah umum dan solusinya |
| [Konfigurasi Tools](../reference/tools_configuration.md) | Aktifkan/nonaktifkan per-tool, kebijakan exec, MCP, Skills |
| [Kompatibilitas Hardware](../guides/hardware-compatibility.md) | Board yang telah diuji, persyaratan minimum |

## 🤝 Kontribusi & Roadmap

PR sangat diterima! Codebase sengaja dibuat kecil dan mudah dibaca.

Lihat [Roadmap Komunitas](https://github.com/stpinkie/rhizome/issues/988) dan [CONTRIBUTING.md](../../CONTRIBUTING.md) untuk panduan.

Grup pengembang sedang dibangun, bergabunglah setelah PR pertama Anda di-merge!

Grup Pengguna:

Discord: <https://discord.gg/V4sAZ9XWpN>

WeChat:
<img src="../../assets/wechat.png" alt="Kode QR grup WeChat" width="512">