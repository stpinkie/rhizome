<div align="center">
<img src="../../assets/logo.webp" alt="Rhizome" width="512">

<h1>Rhizome: Pembantu AI Ultra-Efisien Berasaskan Go</h1>

<h3>Perkakasan $10 · 10MB RAM · But ms · Let's Go, Rhizome!</h3>
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

[中文](README.zh.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Italiano](README.it.md) | [Bahasa Indonesia](README.id.md) | **Malay** | [English](../../README.md)

</div>

---

> **Rhizome** adalah hard fork penyelenggaraan komuniti bagi [PicoClaw](https://github.com/sipeed/picoclaw). Ia ditulis sepenuhnya dalam **Go** dan meneruskan matlamat menjadi pembantu AI peribadi ultra-ringan.

**Rhizome** ialah pembantu AI peribadi yang diilhamkan oleh [NanoBot](https://github.com/HKUDS/nanobot). Ia menambah mesh P2P Go, penyegerakan ruang kerja dan gerbang ejen ke atas idea PicoClaw asal.

**Satu binari Go tunggal, tanpa kebergantungan runtime** — berjalan asli di Linux, Windows, macOS, FreeBSD/NetBSD dan Android. Lihat [Senarai Keserasian Perkakasan](../guides/hardware-compatibility.ms.md) untuk papan yang disahkan dan keperluan sumber semasa dua peringkat.

<p align="center">
<img src="../../assets/rhizome_mem.gif" width="360" height="240">
</p>

> [!CAUTION]
> **Notis Keselamatan**
>
> * **TIADA KRIPTO:** Rhizome **tidak** mengeluarkan sebarang token rasmi atau mata wang kripto. Sebarang tuntutan di `pump.fun` atau platform dagangan lain adalah **penipuan**.
> * **SUMBER KANONIKAL (CANONICAL SOURCE):** Sumber dan lokasi keluaran rasmi adalah **<https://github.com/stpinkie/rhizome>**; keluaran diterbitkan di GitHub Releases. Berhati-hati dengan domain pihak ketiga yang mendakwa rasmi.
> * **AMARAN:** Banyak domain `.ai/.org/.com/.net/...` telah didaftarkan oleh pihak ketiga. Jangan percayai mereka.
> * **NOTA:** Rhizome sedang dalam pembangunan awal yang pantas. Mungkin terdapat isu keselamatan yang belum diselesaikan. Jangan laksanakan dalam persekitaran pengeluaran sebelum v1.0.
> * **NOTA:** Binari `rhizome` lengkap kira-kira 98 MB dan daemon menggunakan kira-kira 60 MB memori peribadi. Kami bercadang membina versi `nonetwork` untuk mengurangkan lagi jejak untuk papan ultra-kecil. Pengoptimuman sumber dirancang selepas fungsi menjadi stabil.

## 📢 Berita

2026-05-28 🚀 **v0.2.9 dikeluarkan!** Pengurusan pelayan MCP dalam Web UI, carian web Sogou yang boleh konfigur, animasi maklum balas alat saluran, nilai lalai `pretty_print` dan `disable_escape_html`, dan pelbagai pembetulan pepijat untuk pembekal dan saluran.

2026-05-14 🚀 **v0.2.8 dikeluarkan!** Arahan MCP CLI (`show`, `add`, `list`, `remove`, `test`, `edit`), objek kosong menggantikan null untuk parameter alat MCP, dan pembetulan binaan.

2026-05-07 🚀 **v0.2.7 dikeluarkan!** Carian web Sogou yang boleh konfigur, animasi maklum balas alat saluran, pembetulan linter.

2026-04-23 🚀 **v0.2.6 dikeluarkan!** Hook dengan tindakan respons dan dokumentasi menyeluruh, sokongan pengasingan, pembetulan bendera bantuan.

2026-04-11 🚀 **v0.2.5 dikeluarkan!** Zoneinfo daripada pembolehubah persekitaran TZ/ZONEINFO, penyelarasan perenderan Matrix CommonMark, `read_file` mengikut baris.

2026-03-31 📱 **Sokongan Android!** Rhizome kini berjalan di Android! APK Android tidak diedarkan daripada fork ini; bina daripada sumber atau semak [GitHub Releases](https://github.com/stpinkie/rhizome/releases) untuk APK pada masa hadapan.

2026-03-25 🚀 **v0.2.4 dikeluarkan!** Penstrukturan semula seni bina Ejen (SubTurn, Hook, Steering, EventBus), integrasi WeChat/WeCom mendalam, peningkatan keselamatan (.security.yml, penapisan data sensitif), pembekal baharu (AWS Bedrock, Azure, Xiaomi MiMo) dan 35 pembetulan pepijat. Rhizome mencapai **26K Bintang**!

2026-03-17 🚀 **v0.2.3 dikeluarkan!** UI dulang sistem (Windows & Linux), pertanyaan status sub-ejen (`spawn_status`), pemuatan semula panas Gateway eksperimen, kawal keselamatan Cron, dan 2 pembetulan keselamatan. Rhizome mencapai **25K Bintang**!

2026-03-09 🎉 **v0.2.1 — Kemas kini terbesar setakat ini!** Sokongan protokol MCP, 4 saluran baharu (Matrix/IRC/WeCom/Discord Proxy), 3 pembekal baharu (Kimi/Minimax/Avian), saluran visual, storan memori JSONL, penghalaan model.

2026-02-28 📦 **v0.2.0** dikeluarkan dengan sokongan Docker Compose dan Web UI Launcher.

<details>
<summary>Berita terdahulu...</summary>

2026-02-26 🎉 Rhizome mencapai **20K Bintang** dalam hanya 17 hari! Orkestrasi saluran automatik dan antara muka keupayaan kini tersedia.

2026-02-16 🎉 Rhizome melebihi 12K Bintang dalam seminggu! Peranan penyelenggara komuniti dan [Peta Jalan](../../ROADMAP.md) rasmi dilancarkan.

2026-02-13 🎉 Rhizome melebihi 5000 Bintang dalam 4 hari! Peta jalan projek dan kumpulan pembangun sedang dibina.

2026-02-09 🎉 **Rhizome Dilancarkan!** Dibina dalam 1 hari untuk meneroka Ejen AI ultra-ringan. Let's Go, Rhizome!

</details>

## ✨ Ciri-ciri

🪶 **Satu binari, tanpa kebergantungan runtime**: Satu executable Go yang diikat secara statik, berjalan di Linux, Windows, macOS, FreeBSD/NetBSD dan Android.*

💰 **Kos minimum**: Mencukupi untuk berjalan di pelbagai papan ARM dan RISC-V kos rendah; lihat [Senarai Keserasian Perkakasan](../guides/hardware-compatibility.ms.md).

⚡️ **But kilat**: Bermula dalam kurang dari 1 saat di papan kos rendah yang disahkan.

🌍 **Benar-benar mudah alih**: Satu binari merentasi seni bina RISC-V, ARM, MIPS dan x86. Satu binari, berjalan di mana-mana!

🤖 **Dibootstrapped oleh AI**: Pelaksanaan Go tulen — 95% kod teras dijana oleh Ejen dan dihaluskan melalui semakan manusia.

🔌 **Sokongan MCP**: Integrasi asal [Model Context Protocol](https://modelcontextprotocol.io/) — sambung mana-mana pelayan MCP untuk melanjutkan keupayaan Ejen.

👁️ **Saluran visual**: Hantar imej dan fail terus kepada Ejen — pengekodan base64 automatik untuk LLM multimodal.

🧠 **Penghalaan pintar**: Penghalaan model berasaskan peraturan — pertanyaan mudah ke model ringan, menjimatkan kos API.

_*Pengukuran jejak dibuat pada Windows dengan `CGO_ENABLED=0`, tag `goolm,stdjson` dan `-ldflags "-s -w"`; binari yang dijalankan kira-kira 98 MB. Kami bercadang membina versi `nonetwork` untuk mengurangkan lagi untuk papan ultra-kecil._

<div align="center">

### Jejak Binaan Semasa

| Mod | Kes Penggunaan | Jumlah RAM | RAM Kosong | Storan |
|-----|----------------|------------|------------|--------|
| **Asas** | `rhizome agent`, `rhizome onboard` sekali | 256 MB | 128 MB | 128 MB |
| **Lengkap** | `rhizome daemon` dengan P2P, syncer dan gateway | 512 MB | 256 MB | 128 MB |

</div>

> **[Senarai Keserasian Perkakasan](../guides/hardware-compatibility.ms.md)** — Lihat semua papan yang diuji, dari Raspberry Pi ke telefon Android. Papan anda tidak tersenarai? Hantar PR!

<p align="center">
<img src="../../assets/hardware-banner.jpg" alt="Rhizome Hardware Compatibility" width="100%">
</p>

## 🦾 Demonstrasi

### 🛠️ Aliran Kerja Pembantu Standard

<table align="center">
<tr align="center">
<th><p align="center">Mod Jurutera Full-Stack</p></th>
<th><p align="center">Pengelogan & Perancangan</p></th>
<th><p align="center">Carian Web & Pembelajaran</p></th>
</tr>
<tr>
<td align="center"><p align="center"><img src="../../assets/rhizome_code.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_memory.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_search.gif" width="240" height="180"></p></td>
</tr>
<tr>
<td align="center">Membangunkan · Melaksanakan · Menskala</td>
<td align="center">Menjadual · Mengautomasi · Mengingat</td>
<td align="center">Menemui · Pemahaman · Trend</td>
</tr>
</table>

### 🐜 Deployment Jejak Rendah yang Inovatif

Rhizome boleh dilaksanakan di pelbagai peranti Linux dan tertanam!

- $15 [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) (atau [Zero 2 W](https://www.raspberrypi.com/products/raspberry-pi-zero-2-w/)), untuk pembantu rumah yang minimum
- $50~70 [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/), untuk penggunaan tertanam berasaskan RISC-V
- $100 [NanoKVM-Pro](https://www.aliexpress.com/item/1005010048471263.html), untuk operasi pelayan automatik
- $100 [MaixCAM2](https://www.kickstarter.com/projects/zepan/maixcam2-build-your-next-gen-4k-ai-camera), untuk pengawasan pintar

> Lihat [Senarai Keserasian Perkakasan](../guides/hardware-compatibility.ms.md) untuk senarai lengkap papan yang disahkan dan keperluan dua peringkat semasa.

<https://private-user-images.githubusercontent.com/83055338/547056448-e7b031ff-d6f5-4468-bcca-5726b6fecb5c.mp4>

🌟 Lebih Banyak Kes Deployment Menanti!

## 📦 Pemasangan

### Muat turun dari GitHub Releases (Disyorkan)

Lawati laman [GitHub Releases](https://github.com/stpinkie/rhizome/releases) dan muat turun binari untuk platform anda.

### Muat turun binari pra-kompil

Sebagai alternatif, muat turun binari untuk platform anda daripada laman [GitHub Releases](https://github.com/stpinkie/rhizome/releases).

### Bina dari sumber (untuk pembangunan)

Keperluan:

- Go 1.26+
- Node.js 22+ dan pnpm 10.33.0+ untuk binaan Web UI / launcher

```bash
git clone https://github.com/stpinkie/rhizome.git

cd rhizome
make deps

# Pasang kebergantungan frontend
(cd web/frontend && pnpm install --frozen-lockfile)

# Bina binari teras untuk platform semasa
make build

# Bina Web UI Launcher (diperlukan untuk mod WebUI)
make build-launcher

# Bina binari teras untuk semua platform yang diuruskan Makefile
make build-all

# Bina untuk Raspberry Pi Zero 2 W
# 32-bit: make build-linux-arm
# 64-bit: make build-linux-arm64
make build-pi-zero

# Bina dan pasang
make install
```

**Raspberry Pi Zero 2 W:** Gunakan binari yang sepadan dengan OS anda: Raspberry Pi OS 32-bit -> `make build-linux-arm`; 64-bit -> `make build-linux-arm64`. Atau jalankan `make build-pi-zero` untuk membina kedua-duanya.

## 🚀 Panduan Permulaan Pantas

### 🌐 Pelancar WebUI (Disyorkan untuk Desktop)

Pelancar WebUI menyediakan antara muka berasaskan pelayan web untuk konfigurasi dan sembang. Ini ialah cara termudah untuk bermula — tiada pengetahuan baris perintah diperlukan.

**Pilihan 1: Klik dua kali (Desktop)**

Selepas memuat turun daripada [GitHub Releases](https://github.com/stpinkie/rhizome/releases), klik dua kali `rhizome-launcher` (atau `rhizome-launcher.exe` di Windows). Pelayar web anda akan dibuka secara automatik di `http://localhost:18800`.

**Pilihan 2: Baris perintah**

```bash
rhizome-launcher
# Buka http://localhost:18800 dalam pelayar web anda
```

> [!TIP]
> **Akses jauh / Docker / VM:** Tambah bendera `-public` untuk mendengar di semua antara muka:
> ```bash
> rhizome-launcher -public
> ```

<p align="center">
<img src="../../assets/launcher-webui.jpg" alt="Pelancar WebUI" width="600">
</p>

**Mulakan:**

Buka WebUI, kemudian: **1)** Konfigurasi Pembekal (tambah API key LLM anda) → **2)** Konfigurasi Saluran (cth. Telegram) → **3)** Mulakan Gateway → **4)** Sembang!

Untuk dokumentasi terperinci, lihat [folder docs/](https://github.com/stpinkie/rhizome/tree/main/docs) dalam repo ini.

<details>
<summary><b>Docker (alternatif)</b></summary>

```bash
# 1. Clone repo ini
git clone https://github.com/stpinkie/rhizome.git
cd rhizome

# 2. Larian pertama — menjana docker/data/config.json secara automatik kemudian keluar
#    (hanya dicetuskan apabila kedua-dua config.json dan workspace/ tiada)
docker compose -f docker/docker-compose.yml --profile launcher up
# Bekas mencetak "First-run setup complete." dan berhenti.

# 3. Tetapkan API key anda
vim docker/data/config.json

# 4. Mulakan
docker compose -f docker/docker-compose.yml --profile launcher up -d
# Buka http://localhost:18800
```

> **Pengguna Docker / VM:** Gateway mendengar di `127.0.0.1` secara lalai. Tetapkan `RHIZOME_GATEWAY_HOST=0.0.0.0` atau gunakan bendera `-public` untuk menjadikannya boleh diakses dari host.

```bash
# Semak log
docker compose -f docker/docker-compose.yml logs -f

# Hentikan
docker compose -f docker/docker-compose.yml --profile launcher down

# Kemas kini
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

</details>

<details>
<summary><b>macOS — Amaran Keselamatan Pelancaran Pertama</b></summary>

macOS mungkin menyekat `rhizome-launcher` pada pelancaran pertama kerana ia dimuat turun dari internet dan tidak disahkan oleh Mac App Store.

**Langkah 1:** Klik dua kali `rhizome-launcher`. Anda akan melihat amaran keselamatan:

<p align="center">
<img src="../../assets/macos-gatekeeper-warning.jpg" alt="Amaran macOS Gatekeeper" width="400">
</p>

> *"rhizome-launcher" Tidak Dibuka — Apple tidak dapat mengesahkan "rhizome-launcher" bebas daripada perisian hasad yang mungkin menjejaskan Mac atau mengancam privasi anda.*

**Langkah 2:** Buka **Tetapan Sistem** → **Privasi & Keselamatan** → tatal ke bawah ke bahagian **Keselamatan** → klik **Buka Juga** → sahkan dengan mengklik **Buka Juga** dalam dialog.

<p align="center">
<img src="../../assets/macos-gatekeeper-allow.jpg" alt="macOS Privasi & Keselamatan — Buka Juga" width="600">
</p>

Selepas langkah sekali ini, `rhizome-launcher` akan dibuka secara normal pada pelancaran seterusnya.

</details>

<a id="-run-on-old-android-phones"></a>
### 📱 Android

Berikan telefon lama anda kehidupan kedua! Jadikannya Pembantu AI Pintar dengan Rhizome.

**Pilihan 1: Pasang APK**

Pratonton:

<table>
  <tr>
    <td><img src="../../assets/fui_main_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_web_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_log_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_setting_page.jpg" width="200"></td>
  </tr>
</table>

APK Android tidak diedarkan daripada fork ini pada masa ini; bina daripada sumber atau semak [GitHub Releases](https://github.com/stpinkie/rhizome/releases) untuk APK pada masa hadapan.

**Pilihan 2: Termux**

Untuk senarai semak lengkap persediaan baris perintah, lihat [Panduan Android Termux](../guides/android-termux.md).

<details>
<summary><b>Pelancar Terminal (untuk persekitaran terhad sumber)</b></summary>

1. Pasang [Termux](https://github.com/termux/termux-app) (muat turun daripada [GitHub Releases](https://github.com/termux/termux-app/releases), atau cari dalam F-Droid / Google Play)
2. Jalankan arahan berikut:

```bash
# Muat turun keluaran terkini
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz
pkg install proot
termux-chroot ./rhizome onboard   # chroot menyediakan susun atur sistem fail Linux standard
```

Kemudian ikuti bahagian Pelancar Terminal di bawah untuk melengkapkan konfigurasi.

<img src="../../assets/termux.jpg" alt="Rhizome on Termux" width="512">

Untuk persekitaran minimum yang hanya mempunyai binari teras `rhizome` (tiada UI Pelancar), anda boleh mengkonfigurasi semuanya melalui baris perintah dan fail konfigurasi JSON.

**1. Mulakan**

```bash
rhizome onboard
```

Ini mencipta `~/.rhizome/config.json` dan direktori workspace.

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
      // api_key kini dimuatkan daripada .security.yml
    }
  ]
}
```

> Lihat `config/config.example.json` dalam repo untuk templat konfiguran lengkap dengan semua pilihan.
>
> Ambil perhatian: config.example.json adalah format version 0, mengandungi kod sensitif, dan akan automatik dimigrate ke version 1+; kemudian config.json hanya menyimpan data tidak sensitif, manakala kod sensitif disimpan dalam .security.yml. Jika perlu mengubah kod secara manual, lihat `docs/security/security_configuration.md`.

**3. Sembang**

```bash
# Satu soalan
rhizome agent -m "What is 2+2?"

# Mod interaktif
rhizome agent

# Mulakan gateway untuk integrasi aplikasi sembang
rhizome gateway
```

</details>

## 🔌 Penyedia (LLM)

Rhizome menyokong 30+ penyedia LLM melalui konfigurasi `model_list`. Gunakan format `protokol/model`:

| Penyedia | Protokol | Kunci API | Nota |
|----------|----------|-----------|------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | Diperlukan | GPT-5.4, GPT-4o, o3, dll. |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | Diperlukan | Claude Opus 4.6, Sonnet 4.6, dll. |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | Diperlukan | Gemini 3 Flash, 2.5 Pro, dll. |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | Diperlukan | 200+ model, API bersatu |
| [Zhipu (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | Diperlukan | GLM-4.7, GLM-5, dll. |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | Diperlukan | DeepSeek-V3, DeepSeek-R1 |
| [Volcengine](https://console.volcengine.com) | `volcengine/` | Diperlukan | Doubao, model Ark |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | Diperlukan | Qwen3, Qwen-Max, dll. |
| [Groq](https://console.groq.com/keys) | `groq/` | Diperlukan | Inferens pantas (Llama, Mixtral) |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | Diperlukan | Model Kimi |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | Diperlukan | Model MiniMax |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | Diperlukan | Mistral Large, Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | Diperlukan | Model hos NVIDIA |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | Diperlukan | Inferens pantas |
| [Novita AI](https://novita.ai/) | `novita/` | Diperlukan | Pelbagai model terbuka |
| [Xiaomi MiMo](https://platform.xiaomimimo.com/) | `mimo/` | Diperlukan | Model MiMo |
| [Ollama](https://ollama.com/) | `ollama/` | Tidak perlu | Model tempatan, self-hosted |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | Tidak perlu | Deployment tempatan, serasi OpenAI |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | Berbeza | Proksi untuk 100+ penyedia |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | Diperlukan | Deployment Azure perusahaan |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | Log masuk kod peranti |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |
| [AWS Bedrock](https://console.aws.amazon.com/bedrock)* | `bedrock/` | Kelayakan AWS | Claude, Llama, Mistral pada AWS |

> \* AWS Bedrock memerlukan tag binaan: `go build -tags bedrock`. Tetapkan `api_base` kepada nama rantau (cth. `us-east-1`) untuk resolusi endpoint automatik merentasi semua partition AWS. Apabila menggunakan URL endpoint penuh, anda juga perlu mengkonfigurasi `AWS_REGION` melalui pemboleh ubah persekitaran.

<details>
<summary><b>Deployment tempatan (Ollama, vLLM, dll.)</b></summary>

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

Untuk butiran konfigurasi penyedia penuh, lihat [Penyedia & Model](../guides/providers.md).

</details>

## 💬 Saluran (Aplikasi Sembang)

Bercakap dengan Rhizome anda melalui 17+ platform pemesejan:

| Saluran | Persediaan | Protokol | Dok |
|---------|-----------|----------|-----|
| **Telegram** | Mudah (token bot) | Long polling | [Panduan](../channels/telegram/README.md) |
| **Discord** | Mudah (token bot + intents) | WebSocket | [Panduan](../channels/discord/README.md) |
| **WhatsApp** | Mudah (imbas QR atau URL jambatan) | Natif / Jambatan | [Panduan](../guides/chat-apps.ms.md#whatsapp) |
| **Weixin** | Mudah (imbas QR natif) | iLink API | [Panduan](../guides/chat-apps.ms.md#weixin) |
| **QQ** | Mudah (AppID + AppSecret) | WebSocket | [Panduan](../channels/qq/README.md) |
| **Slack** | Mudah (token bot + app) | Socket Mode | [Panduan](../channels/slack/README.md) |
| **Matrix** | Sederhana (homeserver + token) | Sync API | [Panduan](../channels/matrix/README.md) |
| **DingTalk** | Sederhana (kelayakan klien) | Stream | [Panduan](../channels/dingtalk/README.md) |
| **Feishu / Lark** | Sederhana (App ID + Secret) | WebSocket/SDK | [Panduan](../channels/feishu/README.md) |
| **LINE** | Sederhana (kelayakan + webhook) | Webhook | [Panduan](../channels/line/README.md) |
| **WeCom** | Mudah (log masuk QR atau manual) | WebSocket | [Panduan](../channels/wecom/README.md) |
| **IRC** | Sederhana (pelayan + nick) | Protokol IRC | [Panduan](../guides/chat-apps.ms.md#irc) |
| **OneBot** | Sederhana (URL WebSocket) | OneBot v11 | [Panduan](../channels/onebot/README.md) |
| **MaixCam** | Mudah (aktifkan) | TCP socket | [Panduan](../channels/maixcam/README.md) |
| **Pico** | Mudah (aktifkan) | Protokol natif | Terbina dalam |
| **Pico Client** | Mudah (URL WebSocket) | WebSocket | Terbina dalam |

> Semua saluran berasaskan webhook berkongsi satu pelayan HTTP Gateway (`gateway.host`:`gateway.port`, lalai `127.0.0.1:18790`). Feishu menggunakan mod WebSocket/SDK dan tidak menggunakan pelayan HTTP yang dikongsi.

> Tahap perincian log dikawal oleh `gateway.log_level` (lalai: `warn`). Nilai yang disokong: `debug`, `info`, `warn`, `error`, `fatal`. Boleh juga ditetapkan melalui `RHIZOME_LOG_LEVEL`. Lihat [Konfigurasi](../guides/configuration.ms.md#gateway-log-level) untuk butiran.

Untuk arahan persediaan saluran terperinci, lihat [Konfigurasi Aplikasi Sembang](../guides/chat-apps.ms.md).

## 🔧 Alat

### 🔍 Carian Web

Rhizome boleh mencari web untuk menyediakan maklumat terkini. Konfigurasikan dalam `tools.web`:

| Enjin Carian | Kunci API | Peringkat Percuma | Pautan |
|-------------|-----------|-------------------|--------|
| DuckDuckGo | Tidak perlu | Tanpa had | Sandaran terbina dalam |
| [Baidu Search](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | Diperlukan | 1500 pertanyaan/bulan (peruntukan harian) | Dikuasai AI, dioptimumkan untuk China |
| [Tavily](https://tavily.com) | Diperlukan | 1000 pertanyaan/bulan | Dioptimumkan untuk AI Agent |
| [Brave Search](https://brave.com/search/api) | Diperlukan | 2000 pertanyaan/bulan | Pantas dan peribadi |
| [Perplexity](https://www.perplexity.ai) | Diperlukan | Berbayar | Carian dikuasai AI |
| [SearXNG](https://github.com/searxng/searxng) | Tidak perlu | Self-hosted | Enjin metasearch percuma |
| [GLM Search](https://open.bigmodel.cn/) | Diperlukan | Berbeza | Carian web Zhipu |

### ⚙️ Alat Lain

Rhizome menyertakan alat terbina dalam untuk operasi fail, pelaksanaan kod, penjadualan, dan banyak lagi. Lihat [Konfigurasi Alat](../reference/tools_configuration.md) untuk butiran.

## 🎯 Kemahiran

Kemahiran adalah keupayaan modular yang melanjutkan Agent anda. Ia dimuatkan dari fail `SKILL.md` dalam ruang kerja anda.

**Pasang kemahiran dari ClawHub:**

```bash
rhizome skills search "web scraping"
rhizome skills install <nama-kemahiran>
```

**Konfigurasikan token ClawHub** (pilihan, untuk had kadar lebih tinggi):

Tambah ke `config.json` anda:
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

Untuk butiran lanjut, lihat [Konfigurasi Alat - Kemahiran](../reference/tools_configuration.md#skills-tool).

## 🔗 MCP (Protokol Konteks Model)

Rhizome menyokong [MCP](https://modelcontextprotocol.io/) secara natif — sambungkan mana-mana pelayan MCP untuk melanjutkan keupayaan Agent anda dengan alat dan sumber data luaran.

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

Untuk konfigurasi MCP penuh (pengangkutan stdio, SSE, HTTP, Penemuan Alat), lihat [Konfigurasi Alat - MCP](../reference/tools_configuration.md#mcp-tool).

## <img src="../../assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> Sertai Rangkaian Sosial Agent

Sambungkan Rhizome ke Rangkaian Sosial Agent dengan menghantar satu mesej melalui CLI atau mana-mana Aplikasi Sembang yang disepadukan.

**Baca `https://clawdchat.ai/skill.md` dan ikuti arahan untuk menyertai [ClawdChat.ai](https://clawdchat.ai)**

## 🖥️ Rujukan CLI

| Arahan | Penerangan |
| ------ | ---------- |
| `rhizome onboard` | Mulakan konfigurasi & ruang kerja |
| `rhizome auth weixin` | Sambungkan akaun WeChat melalui QR |
| `rhizome agent -m "..."` | Sembang dengan agent |
| `rhizome agent` | Mod sembang interaktif |
| `rhizome gateway` | Mulakan gateway |
| `rhizome status` | Tunjukkan status |
| `rhizome version` | Tunjukkan maklumat versi |
| `rhizome model` | Lihat atau tukar model lalai |
| `rhizome cron list` | Senaraikan semua kerja berjadual |
| `rhizome cron add ...` | Tambah kerja berjadual |
| `rhizome cron disable` | Lumpuhkan kerja berjadual |
| `rhizome cron remove` | Buang kerja berjadual |
| `rhizome skills list` | Senaraikan kemahiran yang dipasang |
| `rhizome skills install` | Pasang kemahiran |
| `rhizome migrate` | Migrasi data dari versi lama |
| `rhizome auth login` | Sahkan dengan penyedia |

### ⏰ Tugasan Berjadual / Peringatan

Rhizome menyokong peringatan berjadual dan tugasan berulang melalui alat `cron`:

* **Peringatan sekali**: "Ingatkan saya dalam 10 minit" -> pencetus sekali selepas 10 minit
* **Tugasan berulang**: "Ingatkan saya setiap 2 jam" -> pencetus setiap 2 jam
* **Ungkapan Cron**: "Ingatkan saya pada pukul 9 pagi setiap hari" -> menggunakan ungkapan cron

## 📚 Dokumentasi

Untuk panduan terperinci melebihi README ini:

| Topik | Penerangan |
|-------|------------|
| [Docker & Permulaan Pantas](../guides/docker.ms.md) | Persediaan Docker Compose, mod Launcher/Agent |
| [Aplikasi Sembang](../guides/chat-apps.ms.md) | Panduan persediaan 17+ saluran |
| [Konfigurasi](../guides/configuration.ms.md) | Pemboleh ubah persekitaran, susun atur ruang kerja |
| [Penyedia & Model](../guides/providers.md) | 30+ penyedia LLM, penghalaan model |
| [Spawn & Tugasan Async](../guides/spawn-tasks.ms.md) | Tugasan pantas, tugasan panjang dengan spawn |
| [Penyelesaian Masalah](../operations/troubleshooting.ms.md) | Isu biasa dan penyelesaian |
| [Konfigurasi Alat](../reference/tools_configuration.md) | Aktif/nyahaktif alat, dasar exec, MCP, Kemahiran |
| [Keserasian Perkakasan](../guides/hardware-compatibility.md) | Papan yang diuji, keperluan minimum |

## 🤝 Sumbangan & Peta Jalan

PR dialu-alukan! Kod sumber sengaja dibuat kecil dan mudah dibaca.

Lihat [Peta Jalan Komuniti](https://github.com/stpinkie/rhizome/issues/988) dan [CONTRIBUTING.md](../../CONTRIBUTING.md) untuk panduan.

Kumpulan pembangun sedang dibina, sertai selepas PR pertama anda digabungkan!

Kumpulan Pengguna:

Discord: <https://discord.gg/V4sAZ9XWpN>

WeChat:
<img src="../../assets/wechat.png" alt="Kod QR kumpulan WeChat" width="512">