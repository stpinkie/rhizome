<div align="center">
<img src="../../assets/logo.webp" alt="Rhizome" width="512">

<h1>Rhizome: Trợ lý AI Siêu Nhẹ viết bằng Go</h1>

<h3>Phần cứng $10 · RAM 10MB · Khởi động ms · Let's Go, Rhizome!</h3>
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

[中文](README.zh.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Português](README.pt-br.md) | **Tiếng Việt** | [Français](README.fr.md) | [Italiano](README.it.md) | [Bahasa Indonesia](README.id.md) | [Malay](README.ms.md) | [English](../../README.md)

</div>

---

> **Rhizome** là một hard fork được cộng đồng duy trì từ [PicoClaw](https://github.com/sipeed/picoclaw). Nó được viết hoàn toàn bằng **Go** và tiếp tục mục tiêu trở thành trợ lý AI cá nhân siêu nhẹ.

**Rhizome** là trợ lý AI cá nhân lấy cảm hứng từ [NanoBot](https://github.com/HKUDS/nanobot). Nó thêm mesh P2P Go, đồng bộ workspace và gateway agent lên ý tưởng PicoClaw gốc.

**Một binary Go duy nhất, không có runtime dependency** — chạy trực tiếp trên Linux, Windows, macOS, FreeBSD/NetBSD và Android. Xem [Danh sách Tương thích Phần cứng](../guides/hardware-compatibility.vi.md) để biết các board đã kiểm tra và yêu cầu tài nguyên hai cấp hiện tại.

<p align="center">
<img src="../../assets/rhizome_mem.gif" width="360" height="240">
</p>

> [!CAUTION]
> **THÔNG BÁO BẢO MẬT**
>
> * **KHÔNG CÓ CRYPTO:** Rhizome **chưa** phát hành bất kỳ token hay tiền điện tử chính thức nào. Mọi thông tin trên `pump.fun` hoặc các nền tảng giao dịch khác đều là **lừa đảo**.
> * **NGUỒN CHÍNH THỨC (CANONICAL SOURCE):** Nguồn và địa điểm phát hành chính thức là **<https://github.com/stpinkie/rhizome>**; các bản phát hành đăng tải trên GitHub Releases. Hãy cẩn thận với các domain bên thứ ba tự nhận là chính thức.
> * **CẢNH BÁO:** Nhiều domain `.ai/.org/.com/.net/...` đã bị bên thứ ba đăng ký. Đừng tin tưởng chúng.
> * **LƯU Ý:** Rhizome đang trong giai đoạn phát triển nhanh. Có thể còn các vấn đề bảo mật chưa được giải quyết. Không triển khai lên môi trường production trước v1.0.
> * **LƯU Ý:** Binary `rhizome` đầy đủ khoảng 98 MB và daemon sử dụng khoảng 60 MB bộ nhớ riêng. Chúng tôi dự định xây dựng phiên bản `nonetwork` để giảm hơn nữa cho các board cực nhỏ. Tối ưu hóa tài nguyên được lên kế hoạch sau khi tính năng ổn định.

## 📢 Tin tức

2026-05-28 🚀 **v0.2.9 đã phát hành!** Quản lý MCP server trong Web UI, tìm kiếm web dựa trên Sogou có thể cấu hình, hiệu ứng phản hồi công cụ trong channel, giá trị mặc định `pretty_print` và `disable_escape_html`, và nhiều bản sửa lỗi trên provider và channel.

2026-05-14 🚀 **v0.2.8 đã phát hành!** Lệnh MCP CLI (`show`, `add`, `list`, `remove`, `test`, `edit`), đối tượng rỗng thay vì null cho tham số công cụ MCP, và các bản sửa build.

2026-05-07 🚀 **v0.2.7 đã phát hành!** Tìm kiếm web dựa trên Sogou có thể cấu hình, hiệu ứng phản hồi công cụ trong channel, sửa linter.

2026-04-23 🚀 **v0.2.6 đã phát hành!** Hook với hành động respond và tài liệu toàn diện, hỗ trợ cô lập, sửa banner trợ giúp.

2026-04-11 🚀 **v0.2.5 đã phát hành!** Zoneinfo từ biến môi trường TZ/ZONEINFO, căn chỉnh rendering Matrix CommonMark, `read_file` theo dòng.

2026-03-31 📱 **Hỗ trợ Android!** Rhizome giờ chạy trên Android! APK Android hiện chưa được phân phối từ fork này; hãy xây dựng từ mã nguồn hoặc kiểm tra [GitHub Releases](https://github.com/stpinkie/rhizome/releases) để có phiên bản APK trong tương lai.

2026-03-25 🚀 **v0.2.4 đã phát hành!** Tái cấu trúc kiến trúc Agent (SubTurn, Hooks, Steering, EventBus), tích hợp WeChat/WeCom, tăng cường bảo mật (.security.yml, lọc dữ liệu nhạy cảm), provider mới (AWS Bedrock, Azure, Xiaomi MiMo) và 35 bản vá lỗi. Rhizome đã đạt **26K Stars**!

2026-03-17 🚀 **v0.2.3 đã phát hành!** Giao diện system tray (Windows & Linux), truy vấn trạng thái sub-agent (`spawn_status`), thử nghiệm Gateway hot-reload, bảo mật Cron, và 2 bản vá bảo mật. Rhizome đã đạt **25K Stars**!

2026-03-09 🎉 **v0.2.1 — Bản cập nhật lớn nhất từ trước đến nay!** Hỗ trợ giao thức MCP, 4 Channel mới (Matrix/IRC/WeCom/Discord Proxy), 3 Provider mới (Kimi/Minimax/Avian), pipeline thị giác, bộ nhớ JSONL, định tuyến mô hình.

2026-02-28 📦 **v0.2.0** phát hành với hỗ trợ Docker Compose và Web UI Launcher.

<details>
<summary>Tin tức trước đó...</summary>

2026-02-26 🎉 Rhizome đạt **20K Stars** chỉ trong 17 ngày! Tự động điều phối Channel và giao diện khả năng đã hoạt động.

2026-02-16 🎉 Rhizome vượt 12K Stars trong một tuần! Vai trò người duy trì cộng đồng và [Lộ trình](../../ROADMAP.md) chính thức ra mắt.

2026-02-13 🎉 Rhizome vượt 5000 Stars trong 4 ngày! Lộ trình dự án và nhóm nhà phát triển đang được xây dựng.

2026-02-09 🎉 **Rhizome ra mắt!** Được xây dựng trong 1 ngày để khám phá AI Agent siêu nhẹ. Let's Go, Rhizome!

</details>

## ✨ Tính năng

🪶 **Binary duy nhất, không có runtime dependency**: Một executable Go liên kết tĩnh chạy trên Linux, Windows, macOS, FreeBSD/NetBSD và Android.*

💰 **Chi phí tối thiểu**: Đủ hiệu quả để chạy trên nhiều board ARM và RISC-V giá rẻ; xem [Danh sách Tương thích Phần cứng](../guides/hardware-compatibility.vi.md).

⚡️ **Khởi động cực nhanh**: Khởi động trong dưới 1 giây trên các board giá rẻ đã được kiểm chứng.

🌍 **Thực sự di động**: Một binary duy nhất cho các kiến trúc RISC-V, ARM, MIPS và x86. Một binary, chạy mọi nơi!

🤖 **Được AI khởi động**: Triển khai Go thuần túy — 95% mã lõi được tạo bởi Agent và tinh chỉnh qua quy trình human-in-the-loop.

🔌 **Hỗ trợ MCP**: Tích hợp [Model Context Protocol](https://modelcontextprotocol.io/) gốc — kết nối bất kỳ MCP server nào để mở rộng khả năng Agent.

👁️ **Pipeline thị giác**: Gửi hình ảnh và tệp trực tiếp đến Agent — tự động mã hóa base64 cho LLM đa phương thức.

🧠 **Định tuyến thông minh**: Định tuyến mô hình dựa trên quy tắc — các truy vấn đơn giản đến mô hình nhẹ, tiết kiệm chi phí API.

_*Số đo dấu chân được thực hiện trên Windows với `CGO_ENABLED=0`, tags `goolm,stdjson` và `-ldflags "-s -w"`; binary đã strip khoảng 98 MB. Chúng tôi dự định xây dựng phiên bản `nonetwork` để giảm hơn nữa cho các board cực nhỏ._

<div align="center">

### Dấu chân bản dựng hiện tại

| Chế độ | Trường hợp sử dụng | Tổng RAM | RAM trống | Lưu trữ |
|--------|--------------------|----------|-----------|---------|
| **Cơ bản** | Một lần `rhizome agent`, `rhizome onboard` | 256 MB | 128 MB | 128 MB |
| **Đầy đủ** | `rhizome daemon` với P2P, syncer và gateway | 512 MB | 256 MB | 128 MB |

</div>

> **[Danh sách Tương thích Phần cứng](../guides/hardware-compatibility.vi.md)** — Xem tất cả các board đã được kiểm tra, từ Raspberry Pi đến điện thoại Android. Board của bạn chưa có trong danh sách? Gửi PR!

<p align="center">
<img src="../../assets/hardware-banner.jpg" alt="Rhizome Hardware Compatibility" width="100%">
</p>

## 🦾 Minh họa

### 🛠️ Quy trình Trợ lý Tiêu chuẩn

<table align="center">
<tr align="center">
<th><p align="center">Chế độ Kỹ sư Full-Stack</p></th>
<th><p align="center">Ghi nhật ký & Lập kế hoạch</p></th>
<th><p align="center">Tìm kiếm Web & Học tập</p></th>
</tr>
<tr>
<td align="center"><p align="center"><img src="../../assets/rhizome_code.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_memory.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_search.gif" width="240" height="180"></p></td>
</tr>
<tr>
<td align="center">Phát triển · Triển khai · Mở rộng</td>
<td align="center">Lên lịch · Tự động hóa · Ghi nhớ</td>
<td align="center">Khám phá · Thông tin · Xu hướng</td>
</tr>
</table>

### 🐜 Triển khai Sáng tạo với Dấu chân Nhỏ

Rhizome có thể được triển khai trên nhiều thiết bị Linux và nhúng!

- $15 [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) (hoặc [Zero 2 W](https://www.raspberrypi.com/products/raspberry-pi-zero-2-w/)), cho trợ lý gia đình tối giản
- $50~70 [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/), cho ứng dụng nhúng dựa trên RISC-V
- $100 [NanoKVM-Pro](https://www.aliexpress.com/item/1005010048471263.html), cho vận hành máy chủ tự động
- $100 [MaixCAM2](https://www.kickstarter.com/projects/zepan/maixcam2-build-your-next-gen-4k-ai-camera), cho giám sát thông minh

> Xem [Danh sách Tương thích Phần cứng](../guides/hardware-compatibility.vi.md) để biết danh sách đầy đủ các board đã được xác minh và hai cấp yêu cầu hiện tại.

<https://private-user-images.githubusercontent.com/83055338/547056448-e7b031ff-d6f5-4468-bcca-5726b6fecb5c.mp4>

🌟 Còn nhiều trường hợp triển khai đang chờ đón!

## 📦 Cài đặt

### Tải xuống từ GitHub Releases (Khuyến nghị)

Truy cập trang [GitHub Releases](https://github.com/stpinkie/rhizome/releases) và tải xuống binary cho nền tảng của bạn.

### Tải xuống binary đã biên dịch sẵn

Ngoài ra, tải binary cho nền tảng của bạn từ trang [GitHub Releases](https://github.com/stpinkie/rhizome/releases).

### Xây dựng từ mã nguồn (để phát triển)

Yêu cầu:

- Go 1.25+
- Node.js 22+ và pnpm 10.33.0+ cho các bản build Web UI / launcher

```bash
git clone https://github.com/stpinkie/rhizome.git

cd rhizome
make deps

# Cài đặt dependencies frontend
(cd web/frontend && pnpm install --frozen-lockfile)

# Build binary lõi
make build

# Build Web UI Launcher (cần cho chế độ WebUI)
make build-launcher

# Build các binary lõi cho mọi nền tảng do Makefile quản lý
make build-all

# Build for Raspberry Pi Zero 2 W (32-bit: make build-linux-arm; 64-bit: make build-linux-arm64)
make build-pi-zero

# Build and install
make install
```

**Raspberry Pi Zero 2 W:** Sử dụng binary phù hợp với hệ điều hành của bạn: Raspberry Pi OS 32-bit -> `make build-linux-arm`; 64-bit -> `make build-linux-arm64`. Hoặc chạy `make build-pi-zero` để xây dựng cả hai.

## 🚀 Hướng dẫn Khởi động Nhanh

### 🌐 WebUI Launcher (Khuyến nghị cho Desktop)

WebUI Launcher cung cấp giao diện dựa trên trình duyệt để cấu hình và trò chuyện. Đây là cách dễ nhất để bắt đầu — không cần kiến thức dòng lệnh.

**Tùy chọn 1: Nhấp đúp (Desktop)**

Sau khi tải xuống từ [GitHub Releases](https://github.com/stpinkie/rhizome/releases), nhấp đúp vào `rhizome-launcher` (hoặc `rhizome-launcher.exe` trên Windows). Trình duyệt của bạn sẽ tự động mở tại `http://localhost:18800`.

**Tùy chọn 2: Dòng lệnh**

```bash
rhizome-launcher
# Mở http://localhost:18800 trong trình duyệt của bạn
```

> [!TIP]
> **Truy cập từ xa / Docker / VM:** Thêm cờ `-public` để lắng nghe trên tất cả các giao diện:
> ```bash
> rhizome-launcher -public
> ```

<p align="center">
<img src="../../assets/launcher-webui.jpg" alt="WebUI Launcher" width="600">
</p>

**Bắt đầu:**

Mở WebUI, sau đó: **1)** Cấu hình Provider (thêm API key LLM của bạn) → **2)** Cấu hình Channel (ví dụ: Telegram) → **3)** Khởi động Gateway → **4)** Trò chuyện!

Tài liệu chi tiết xem [thư mục docs/](https://github.com/stpinkie/rhizome/tree/main/docs) trong repo này.

<details>
<summary><b>Docker (thay thế)</b></summary>

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

> **Người dùng Docker / VM:** Gateway lắng nghe trên `127.0.0.1` theo mặc định. Đặt `RHIZOME_GATEWAY_HOST=0.0.0.0` hoặc dùng cờ `-public` để có thể truy cập từ host.

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
<summary><b>macOS — Cảnh báo bảo mật khi khởi chạy lần đầu</b></summary>

macOS có thể chặn `rhizome-launcher` khi khởi chạy lần đầu vì nó được tải từ internet và chưa được công chứng qua Mac App Store.

**Bước 1:** Nhấp đúp vào `rhizome-launcher`. Bạn sẽ thấy cảnh báo bảo mật:

<p align="center">
<img src="../../assets/macos-gatekeeper-warning.jpg" alt="Cảnh báo macOS Gatekeeper" width="400">
</p>

> *"rhizome-launcher" Không Mở Được — Apple không thể xác minh "rhizome-launcher" không chứa phần mềm độc hại có thể gây hại cho Mac hoặc xâm phạm quyền riêng tư của bạn.*

**Bước 2:** Mở **Cài đặt Hệ thống** → **Quyền riêng tư & Bảo mật** → cuộn xuống phần **Bảo mật** → nhấp **Vẫn Mở** → xác nhận bằng cách nhấp **Vẫn Mở** trong hộp thoại.

<p align="center">
<img src="../../assets/macos-gatekeeper-allow.jpg" alt="macOS Quyền riêng tư & Bảo mật — Vẫn Mở" width="600">
</p>

Sau bước này, `rhizome-launcher` sẽ mở bình thường trong các lần khởi chạy tiếp theo.

</details>

<a id="-run-on-old-android-phones"></a>
### 📱 Android

Hãy cho chiếc điện thoại cũ của bạn một cuộc sống mới! Biến nó thành Trợ lý AI thông minh với Rhizome.

**Tùy chọn 1: Cài đặt APK**

Xem trước:

<table>
  <tr>
    <td><img src="../../assets/fui_main_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_web_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_log_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_setting_page.jpg" width="200"></td>
  </tr>
</table>

Android APK hiện chưa được phát hành từ fork này; hãy xây dựng từ mã nguồn hoặc kiểm tra [GitHub Releases](https://github.com/stpinkie/rhizome/releases) để có phiên bản APK trong tương lai.

**Tùy chọn 2: Termux**

Để có danh sách kiểm tra đầy đủ khi thiết lập dòng lệnh, xem [Hướng dẫn Android Termux](../guides/android-termux.md).

<details>
<summary><b>Terminal Launcher (cho môi trường hạn chế tài nguyên)</b></summary>

1. Cài đặt [Termux](https://github.com/termux/termux-app) (tải từ [GitHub Releases](https://github.com/termux/termux-app/releases), hoặc tìm kiếm trong F-Droid / Google Play)
2. Chạy các lệnh sau:

```bash
# Tải bản Release mới nhất
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz
pkg install proot
termux-chroot ./rhizome onboard   # chroot cung cấp layout hệ thống tệp Linux chuẩn
```

Sau đó làm theo phần Terminal Launcher bên dưới để hoàn tất cấu hình.

<img src="../../assets/termux.jpg" alt="Rhizome on Termux" width="512">

Đối với các môi trường tối giản chỉ có binary lõi `rhizome` (không có Launcher UI), bạn có thể cấu hình mọi thứ qua dòng lệnh và tệp cấu hình JSON.

**1. Khởi tạo**

```bash
rhizome onboard
```

Lệnh này tạo `~/.rhizome/config.json` và thư mục workspace.

**2. Cấu hình** (`~/.rhizome/config.json`)

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
      // api_key hiện được tải từ .security.yml
    }
  ]
}
```

> Xem `config/config.example.json` trong repo để có mẫu cấu hình đầy đủ với tất cả các tùy chọn có sẵn.
>
> Lưu ý: config.example.json có định dạng version 0, chứa mã nhạy cảm và sẽ tự động migrate lên version 1+; sau đó config.json chỉ lưu dữ liệu không nhạy cảm, còn mã nhạy cảm sẽ lưu trong .security.yml. Nếu cần chỉnh sửa mã thủ công, xem `docs/security/security_configuration.md`.

**3. Trò chuyện**

```bash
# Một câu hỏi duy nhất
rhizome agent -m "What is 2+2?"

# Chế độ tương tác
rhizome agent

# Khởi động gateway để tích hợp ứng dụng chat
rhizome gateway
```

</details>

## 🔌 Providers (LLM)

Rhizome hỗ trợ 30+ Provider LLM thông qua cấu hình `model_list`. Sử dụng định dạng `protocol/model`:

| Provider | Protocol | API Key | Ghi chú |
|----------|----------|---------|---------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | Bắt buộc | GPT-5.4, GPT-4o, o3, v.v. |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | Bắt buộc | Claude Opus 4.6, Sonnet 4.6, v.v. |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | Bắt buộc | Gemini 3 Flash, 2.5 Pro, v.v. |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | Bắt buộc | 200+ mô hình, API thống nhất |
| [Zhipu (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | Bắt buộc | GLM-4.7, GLM-5, v.v. |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | Bắt buộc | DeepSeek-V3, DeepSeek-R1 |
| [Volcengine](https://console.volcengine.com) | `volcengine/` | Bắt buộc | Doubao, Ark models |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | Bắt buộc | Qwen3, Qwen-Max, v.v. |
| [Groq](https://console.groq.com/keys) | `groq/` | Bắt buộc | Suy luận nhanh (Llama, Mixtral) |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | Bắt buộc | Kimi models |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | Bắt buộc | MiniMax models |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | Bắt buộc | Mistral Large, Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | Bắt buộc | Mô hình do NVIDIA lưu trữ |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | Bắt buộc | Suy luận nhanh |
| [Novita AI](https://novita.ai/) | `novita/` | Bắt buộc | Nhiều mô hình mở |
| [Xiaomi MiMo](https://platform.xiaomimimo.com/) | `mimo/` | Bắt buộc | Mô hình MiMo |
| [Ollama](https://ollama.com/) | `ollama/` | Không cần | Mô hình cục bộ, tự lưu trữ |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | Không cần | Triển khai cục bộ, tương thích OpenAI |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | Tùy | Proxy cho 100+ provider |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | Bắt buộc | Triển khai Azure doanh nghiệp |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | Đăng nhập bằng device code |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |

<details>
<summary><b>Triển khai cục bộ (Ollama, vLLM, v.v.)</b></summary>

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

Để biết chi tiết cấu hình provider đầy đủ, xem [Providers & Models](../guides/providers.vi.md).

</details>

## 💬 Channels (Ứng dụng Chat)

Trò chuyện với Rhizome của bạn qua 17+ nền tảng nhắn tin:

| Channel | Thiết lập | Protocol | Tài liệu |
|---------|-----------|----------|----------|
| **Telegram** | Dễ (bot token) | Long polling | [Hướng dẫn](../channels/telegram/README.vi.md) |
| **Discord** | Dễ (bot token + intents) | WebSocket | [Hướng dẫn](../channels/discord/README.vi.md) |
| **WhatsApp** | Dễ (quét QR hoặc bridge URL) | Native / Bridge | [Hướng dẫn](../guides/chat-apps.vi.md#whatsapp) |
| **Weixin** | Dễ (quét QR gốc) | iLink API | [Hướng dẫn](../guides/chat-apps.vi.md#weixin) |
| **QQ** | Dễ (AppID + AppSecret) | WebSocket | [Hướng dẫn](../channels/qq/README.vi.md) |
| **Slack** | Dễ (bot + app token) | Socket Mode | [Hướng dẫn](../channels/slack/README.vi.md) |
| **Matrix** | Trung bình (homeserver + token) | Sync API | [Hướng dẫn](../channels/matrix/README.vi.md) |
| **DingTalk** | Trung bình (client credentials) | Stream | [Hướng dẫn](../channels/dingtalk/README.vi.md) |
| **Feishu / Lark** | Trung bình (App ID + Secret) | WebSocket/SDK | [Hướng dẫn](../channels/feishu/README.vi.md) |
| **LINE** | Trung bình (credentials + webhook) | Webhook | [Hướng dẫn](../channels/line/README.vi.md) |
| **WeCom** | Dễ (đăng nhập QR hoặc thủ công) | WebSocket | [Hướng dẫn](../channels/wecom/README.vi.md) |
| **IRC** | Trung bình (server + nick) | IRC protocol | [Hướng dẫn](../guides/chat-apps.vi.md#irc) |
| **OneBot** | Trung bình (WebSocket URL) | OneBot v11 | [Hướng dẫn](../channels/onebot/README.vi.md) |
| **MaixCam** | Dễ (bật) | TCP socket | [Hướng dẫn](../channels/maixcam/README.vi.md) |
| **Pico** | Dễ (bật) | Native protocol | Tích hợp sẵn |
| **Pico Client** | Dễ (WebSocket URL) | WebSocket | Tích hợp sẵn |

> Tất cả các Channel dựa trên webhook dùng chung một Gateway HTTP server (`gateway.host`:`gateway.port`, mặc định `127.0.0.1:18790`). Feishu sử dụng chế độ WebSocket/SDK và không dùng HTTP server chung.

> Mức độ chi tiết log được kiểm soát bởi `gateway.log_level` (mặc định: `warn`). Các giá trị được hỗ trợ: `debug`, `info`, `warn`, `error`, `fatal`. Cũng có thể đặt qua `RHIZOME_LOG_LEVEL`. Xem [Cấu hình](../guides/configuration.vi.md#mức-log-của-gateway) để biết thêm chi tiết.

Để biết hướng dẫn thiết lập Channel chi tiết, xem [Cấu hình Ứng dụng Chat](../guides/chat-apps.vi.md).

## 🔧 Tools

### 🔍 Tìm kiếm Web

Rhizome có thể tìm kiếm web để cung cấp thông tin cập nhật. Cấu hình trong `tools.web`:

| Công cụ Tìm kiếm | API Key | Gói miễn phí | Liên kết |
|------------------|---------|--------------|----------|
| DuckDuckGo | Không cần | Không giới hạn | Dự phòng tích hợp sẵn |
| [Baidu Search](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | Bắt buộc | 1500 truy vấn/tháng (phân bổ hàng ngày) | AI, tối ưu cho tiếng Trung |
| [Tavily](https://tavily.com) | Bắt buộc | 1000 truy vấn/tháng | Tối ưu cho AI Agent |
| [Brave Search](https://brave.com/search/api) | Bắt buộc | 2000 truy vấn/tháng | Nhanh và riêng tư |
| [Perplexity](https://www.perplexity.ai) | Bắt buộc | Trả phí | Tìm kiếm hỗ trợ AI |
| [SearXNG](https://github.com/searxng/searxng) | Không cần | Tự lưu trữ | Metasearch engine miễn phí |
| [GLM Search](https://open.bigmodel.cn/) | Bắt buộc | Tùy | Tìm kiếm web Zhipu |

### ⚙️ Các Tools Khác

Rhizome bao gồm các tool tích hợp sẵn cho thao tác tệp, thực thi mã, lên lịch và nhiều hơn nữa. Xem [Cấu hình Tools](../reference/tools_configuration.vi.md) để biết chi tiết.

## 🎯 Skills

Skills là các khả năng mô-đun mở rộng Agent của bạn. Chúng được tải từ các tệp `SKILL.md` trong workspace của bạn.

**Cài đặt Skills từ ClawHub:**

```bash
rhizome skills search "web scraping"
rhizome skills install <skill-name>
```

**Cấu hình token ClawHub** (tùy chọn, để có giới hạn tốc độ cao hơn):

Thêm vào `config.json` của bạn:
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

Để biết thêm chi tiết, xem [Cấu hình Tools - Skills](../reference/tools_configuration.vi.md#skills-tool).

## 🔗 MCP (Model Context Protocol)

Rhizome hỗ trợ [MCP](https://modelcontextprotocol.io/) gốc — kết nối bất kỳ MCP server nào để mở rộng khả năng Agent của bạn với các tool và nguồn dữ liệu bên ngoài.

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

Để biết cấu hình MCP đầy đủ (stdio, SSE, HTTP transports, Tool Discovery), xem [Cấu hình Tools - MCP](../reference/tools_configuration.vi.md#mcp-tool).

## <img src="../../assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> Tham gia Mạng xã hội Agent

Kết nối Rhizome với Mạng xã hội Agent chỉ bằng cách gửi một tin nhắn duy nhất qua CLI hoặc bất kỳ Ứng dụng Chat nào đã tích hợp.

**Đọc `https://clawdchat.ai/skill.md` và làm theo hướng dẫn để tham gia [ClawdChat.ai](https://clawdchat.ai)**

## 🖥️ Tham chiếu CLI

| Lệnh                      | Mô tả                                    |
| ------------------------- | ---------------------------------------- |
| `rhizome onboard`        | Khởi tạo cấu hình & workspace           |
| `rhizome auth weixin` | Kết nối tài khoản WeChat qua QR |
| `rhizome agent -m "..."` | Trò chuyện với agent                     |
| `rhizome agent`          | Chế độ trò chuyện tương tác             |
| `rhizome gateway`        | Khởi động gateway                        |
| `rhizome status`         | Hiển thị trạng thái                      |
| `rhizome version`        | Hiển thị thông tin phiên bản            |
| `rhizome model`          | Xem hoặc chuyển đổi mô hình mặc định   |
| `rhizome cron list`      | Liệt kê tất cả công việc đã lên lịch   |
| `rhizome cron add ...`   | Thêm công việc đã lên lịch             |
| `rhizome cron disable`   | Vô hiệu hóa công việc đã lên lịch      |
| `rhizome cron remove`    | Xóa công việc đã lên lịch              |
| `rhizome skills list`    | Liệt kê các Skill đã cài đặt           |
| `rhizome skills install` | Cài đặt một Skill                       |
| `rhizome migrate`        | Di chuyển dữ liệu từ các phiên bản cũ  |
| `rhizome auth login`     | Xác thực với các provider               |

### ⏰ Tác vụ Đã lên lịch / Nhắc nhở

Rhizome hỗ trợ nhắc nhở đã lên lịch và tác vụ định kỳ thông qua tool `cron`:

* **Nhắc nhở một lần**: "Nhắc tôi sau 10 phút" -> kích hoạt một lần sau 10 phút
* **Tác vụ định kỳ**: "Nhắc tôi mỗi 2 giờ" -> kích hoạt mỗi 2 giờ
* **Biểu thức Cron**: "Nhắc tôi lúc 9 giờ sáng hàng ngày" -> sử dụng biểu thức cron

## 📚 Tài liệu

Để biết các hướng dẫn chi tiết ngoài README này:

| Chủ đề | Mô tả |
|--------|-------|
| [Docker & Khởi động Nhanh](../guides/docker.vi.md) | Thiết lập Docker Compose, chế độ Launcher/Agent |
| [Ứng dụng Chat](../guides/chat-apps.vi.md) | Hướng dẫn thiết lập 17+ Channel |
| [Cấu hình](../guides/configuration.vi.md) | Biến môi trường, bố cục workspace, sandbox bảo mật |
| [Providers & Models](../guides/providers.vi.md) | 30+ Provider LLM, định tuyến mô hình, cấu hình model_list |
| [Spawn & Tác vụ Bất đồng bộ](../guides/spawn-tasks.vi.md) | Tác vụ nhanh, tác vụ dài với spawn, điều phối sub-agent bất đồng bộ |
| [Hooks](../architecture/hooks/README.md) | Hệ thống hook hướng sự kiện: observer, interceptor, approval hook |
| [Steering](../architecture/steering.md) | Chèn tin nhắn vào vòng lặp agent đang chạy |
| [SubTurn](../architecture/subturn.md) | Điều phối subagent, kiểm soát đồng thời, vòng đời |
| [Khắc phục sự cố](../operations/troubleshooting.vi.md) | Các vấn đề thường gặp và giải pháp |
| [Cấu hình Tools](../reference/tools_configuration.vi.md) | Bật/tắt từng tool, chính sách exec, MCP, Skills |
| [Tương thích Phần cứng](../guides/hardware-compatibility.vi.md) | Các board đã kiểm tra, yêu cầu tối thiểu |

## 🤝 Đóng góp & Lộ trình

PR luôn được chào đón! Codebase được thiết kế nhỏ gọn và dễ đọc.

Xem [Lộ trình Cộng đồng](https://github.com/stpinkie/rhizome/issues/988) và [CONTRIBUTING.md](../../CONTRIBUTING.md) để biết hướng dẫn.

Nhóm nhà phát triển đang được xây dựng, tham gia sau khi PR đầu tiên của bạn được merge!

Nhóm Người dùng:

Discord: <https://discord.gg/V4sAZ9XWpN>

WeChat:
<img src="../../assets/wechat.png" alt="WeChat group QR code" width="512">