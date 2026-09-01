<div align="center">
<img src="../../assets/logo.webp" alt="Rhizome" width="512">

<h1>Rhizome: Go 기반 초고효율 AI 어시스턴트</h1>

<h3>$10 하드웨어 · 10MB RAM · ms 부팅 · Let's Go, Rhizome!</h3>
  <p>
    <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/Arch-x86__64%2C%20ARM64%2C%20MIPS%2C%20RISC--V%2C%20LoongArch-blue" alt="Hardware">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
    <br>
    <a href="https://github.com/stpinkie/rhizome"><img src="https://img.shields.io/badge/GitHub-stpinkie/rhizome-181717?style=flat&logo=github&logoColor=white" alt="GitHub"></a>
    <a href="https://github.com/stpinkie/rhizome/tree/main/docs"><img src="https://img.shields.io/badge/Docs-007acc?style=flat&logo=read-the-docs&logoColor=white" alt="Docs"></a>
    <a href="https://discord.gg/V4sAZ9XWpN"><img src="https://img.shields.io/badge/Discord-Community-4c60eb?style=flat&logo=discord&logoColor=white" alt="Discord"></a>
    <br>
    <a href="../../assets/wechat.png"><img src="https://img.shields.io/badge/WeChat-Group-41d56b?style=flat&logo=wechat&logoColor=white"></a>
  </p>

[中文](README.zh.md) | [日本語](README.ja.md) | **한국어** | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Italiano](README.it.md) | [Bahasa Indonesia](README.id.md) | [Malay](README.ms.md) | [English](../../README.md)

</div>

---

> **Rhizome**는 [PicoClaw](https://github.com/sipeed/picoclaw)의 커뮤니티 유지 하드 포크입니다. 완전히 **Go**로 작성되었으며 초경량 개인 AI 어시스턴트라는 목표를 이어가고 있습니다.

**Rhizome**은 [NanoBot](https://github.com/HKUDS/nanobot)에서 영감을 받은 개인 AI 어시스턴트입니다. 원래 PicoClaw 아이디어 위에 Go P2P 메시, 워크스페이스 동기화, 에이전트 게이트웨이를 추가했습니다.

**단일 Go 바이너리, 런타임 의존성 없음** — Linux, Windows, macOS, FreeBSD/NetBSD, Android에서 네이티브로 실행됩니다. 검증된 보드와 현재 두 단계 리소스 요구사항은 [하드웨어 호환성 목록](../guides/hardware-compatibility.md)을 참고하세요.

<p align="center">
<img src="../../assets/rhizome_mem.gif" width="360" height="240">
</p>

> [!CAUTION]
> **보안 공지**
>
> * **NO CRYPTO:** Rhizome는 공식 토큰이나 암호화폐를 **발행하지 않았습니다**. `pump.fun`이나 다른 거래 플랫폼의 관련 주장은 모두 **사기**입니다.
> * **CANONICAL SOURCE:** 공식 소스 및 릴리스 위치는 **<https://github.com/stpinkie/rhizome>**이며, 릴리스는 GitHub Releases에 게시됩니다. 공식을 자처하는 제3자 도메인에 주의하세요.
> * **주의:** 많은 `.ai/.org/.com/.net/...` 도메인이 제3자에 의해 선점되었습니다. 믿지 마세요.
> * **참고:** Rhizome는 초기 고속 기능 개발 단계에 있습니다. 아직 해결되지 않은 보안 문제가 있을 수 있습니다. v1.0 정식 릴리스 전까지 프로덕션에 배포하지 마세요.
> * **참고:** 완전한 `rhizome` 바이너리는 약 98MB이며, 데몬은 약 60MB의 사설 메모리를 사용합니다. 초소형 보드의 사용량을 더 줄이기 위해 `nonetwork` 빌드를 계획 중입니다. 리소스 최적화는 기능 안정화 후 진행될 예정입니다.

## 📢 뉴스

2026-05-28 🚀 **v0.2.9 출시!** Web UI에서 MCP 서버 관리, 구성 가능한 Sogou 웹 검색, 채널 도구 피드백 애니메이션, `pretty_print` 및 `disable_escape_html` 기본값, 그리고 프로바이더와 채널의 다양한 버그 수정.

2026-05-14 🚀 **v0.2.8 출시!** MCP CLI 명령어(`show`, `add`, `list`, `remove`, `test`, `edit`), MCP 도구 매개변수 null 대신 빈 객체, 빌드 수정.

2026-05-07 🚀 **v0.2.7 출시!** 구성 가능한 Sogou 웹 검색, 채널 도구 피드백 애니메이션, 린터 수정.

2026-04-23 🚀 **v0.2.6 출시!** 응답 동작이 포함된 Hook과 종합 문서, 격리 지원, 도움말 배너 수정.

2026-04-11 🚀 **v0.2.5 출시!** TZ/ZONEINFO 환경 변수에서 Zoneinfo 가져오기, Matrix CommonMark 렌더링 정렬, 줄 단위 `read_file`.

2026-03-31 📱 **Android 지원!** Rhizome가 Android에서 실행됩니다! Android APK는 이 포크에서 현재 배포되지 않습니다. 소스에서 빌드하거나 향후 APK는 [GitHub Releases](https://github.com/stpinkie/rhizome/releases)를 확인하세요.

2026-03-25 🚀 **v0.2.4 출시!** 에이전트 아키텍처 전면 재설계(SubTurn, Hook, Steering, EventBus), WeChat/WeCom 심층 통합, 보안 체계 강화(.security.yml, 민감 데이터 필터링), 신규 프로바이더(AWS Bedrock, Azure, Xiaomi MiMo) 및 35개 버그 수정. Rhizome는 **26K Stars**를 달성했습니다!

2026-03-17 🚀 **v0.2.3 출시!** 시스템 트레이 UI(Windows & Linux), 하위 에이전트 상태 조회(`spawn_status`), 실험적 Gateway 핫 리로드, Cron 안전 게이트, 2건의 보안 수정. Rhizome는 **25K Stars**를 달성했습니다!

2026-03-09 🎉 **v0.2.1 — 지금까지 가장 큰 업데이트!** MCP 프로토콜 지원, 4개 신규 채널(Matrix/IRC/WeCom/Discord Proxy), 3개 신규 프로바이더(Kimi/Minimax/Avian), 비전 파이프라인, JSONL 메모리 저장, 모델 라우팅.

2026-02-28 📦 **v0.2.0**이 Docker Compose와 Web UI Launcher 지원과 함께 출시되었습니다.

<details>
<summary>이전 뉴스...</summary>

2026-02-26 🎉 Rhizome가 단 17일 만에 **20K Stars**를 돌파! 채널 자동 오케스트레이션과 능력 인터페이스가 출시되었습니다.

2026-02-16 🎉 Rhizome가 일주일 만에 12K Stars를 돌파! 커뮤니티 메인테이너 역할과 [로드맵](../../ROADMAP.md)이 공식 발표되었습니다.

2026-02-13 🎉 Rhizome가 4일 만에 5000 Stars를 돌파! 프로젝트 로드맵과 개발자 그룹이 구성 중입니다.

2026-02-09 🎉 **Rhizome 공식 출시!** 초경량 AI 에이전트를 탐구하기 위해 단 1일 만에 구축되었습니다. Let's Go, Rhizome!

</details>

## ✨ 기능

🪶 **단일 바이너리, 런타임 의존성 없음**: Linux, Windows, macOS, FreeBSD/NetBSD, Android에서 실행되는 정적으로 연결된 Go 실행 파일입니다.*

💰 **최소 비용**: 다양한 저비용 ARM 및 RISC-V 보드에서 실행할 만큼 효율적입니다. [하드웨어 호환성 목록](../guides/hardware-compatibility.md)을 참고하세요.

⚡️ **번개 부팅**: 검증된 저비용 보드에서 1초 미만으로 시작합니다.

🌍 **진정한 이식성**: RISC-V, ARM, MIPS, x86 아키텍처에서 단일 바이너리로 실행됩니다. 하나의 바이너리, 어디서나!

🤖 **AI 부트스트랩**: 순수 Go 네이티브 구현 — 핵심 코드의 95%가 에이전트에 의해 생성되고 인간-인-더-루프 검토를 통해 미세 조정되었습니다.

🔌 **MCP 지원**: 네이티브 [Model Context Protocol](https://modelcontextprotocol.io/) 통합 — 모든 MCP 서버에 연결하여 에이전트 기능을 확장하세요.

👁️ **비전 파이프라인**: 에이전트에게 직접 이미지와 파일을 보내세요 — 멀티모달 LLM을 위해 base64 인코딩이 자동으로 수행됩니다.

🧠 **스마트 라우팅**: 규칙 기반 모델 라우팅 — 간단한 쿼리를 경량 모델로 전송하여 API 비용을 절약합니다.

_*Windows에서 `CGO_ENABLED=0`, 태그 `goolm,stdjson`, `-ldflags "-s -w"`로 측정; 스트립된 바이너리는 약 98MB입니다. 초소형 보드의 사용량을 더 줄이기 위해 `nonetwork` 빌드를 계획 중입니다._

<div align="center">

### 현재 빌드 사용량

| 모드 | 사용 사례 | 총 메모리 | 여유 메모리 | 스토리지 |
|------|-----------|-----------|-------------|----------|
| **기본** | 일회성 `rhizome agent`, `rhizome onboard` | 256 MB | 128 MB | 128 MB |
| **전체** | P2P, 동기화, 게이트웨이를 갖춘 `rhizome daemon` | 512 MB | 256 MB | 128 MB |

</div>

> **[하드웨어 호환성 목록](../guides/hardware-compatibility.md)** — Raspberry Pi에서 Android 휴대폰까지 검증된 모든 보드를 확인하세요. 보드가 목록에 없다면 PR을 보내주세요!

<p align="center">
<img src="../../assets/hardware-banner.jpg" alt="Rhizome Hardware Compatibility" width="100%">
</p>

## 🦾 데모

### 🛠️ 표준 어시스턴트 워크플로

<table align="center">
<tr align="center">
<th><p align="center">풀스택 엔지니어 모드</p></th>
<th><p align="center">로깅 및 계획</p></th>
<th><p align="center">웹 검색 및 학습</p></th>
</tr>
<tr>
<td align="center"><p align="center"><img src="../../assets/rhizome_code.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_memory.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_search.gif" width="240" height="180"></p></td>
</tr>
<tr>
<td align="center">개발 · 배포 · 확장</td>
<td align="center">예약 · 자동화 · 기억</td>
<td align="center">발견 · 인사이트 · 트렌드</td>
</tr>
</table>

### 🐜 혁신적인 초저사양 배포

Rhizome는 다양한 Linux 및 임베디드 기기에 배포할 수 있습니다!

- $15 [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/)(또는 [Zero 2 W](https://www.raspberrypi.com/products/raspberry-pi-zero-2-w/)), 최소한의 홈 어시스턴트용
- $50~70 [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/), RISC-V 기반 임베디드 사용용
- $100 [NanoKVM-Pro](https://www.aliexpress.com/item/1005010048471263.html), 자동화된 서버 운영용
- $100 [MaixCAM2](https://www.kickstarter.com/projects/zepan/maixcam2-build-your-next-gen-4k-ai-camera), 스마트 감시용

> 검증된 보드 전체 목록과 현재 두 단계 요구사항은 [하드웨어 호환성 목록](../guides/hardware-compatibility.md)을 참고하세요.

<https://private-user-images.githubusercontent.com/83055338/547056448-e7b031ff-d6f5-4468-bcca-5726b6fecb5c.mp4>

🌟 더 많은 배포 사례가 기대됩니다!

## 📦 설치

### GitHub Releases에서 다운로드(권장)

[GitHub Releases](https://github.com/stpinkie/rhizome/releases) 페이지로 이동하여 자신의 플랫폼에 맞는 바이너리를 다운로드하세요.

### 사전 컴파일된 바이너리 다운로드

또는 [GitHub Releases](https://github.com/stpinkie/rhizome/releases) 페이지에서 해당 플랫폼의 바이너리를 수동으로 다운로드할 수 있습니다.

### 소스에서 빌드(개발용)

필수 조건:

- Go 1.26+
- Web UI / launcher 빌드를 위한 Node.js 22+ 및 pnpm 10.33.0+

```bash
git clone https://github.com/stpinkie/rhizome.git

cd rhizome
make deps

# 프론트엔드 의존성 설치
(cd web/frontend && pnpm install --frozen-lockfile)

# 현재 플랫폼용 코어 바이너리 빌드
make build

# Web UI Launcher 빌드(WebUI 모드에 필요)
make build-launcher

# Makefile로 관리하는 모든 플랫폼용 코어 바이너리 빌드
make build-all

# Raspberry Pi Zero 2 W용 빌드
# 32비트: make build-linux-arm
# 64비트: make build-linux-arm64
make build-pi-zero

# 빌드 및 설치
make install
```

**Raspberry Pi Zero 2 W:** OS에 맞는 바이너리를 사용하세요. 32비트 Raspberry Pi OS → `make build-linux-arm`; 64비트 → `make build-linux-arm64`. 또는 `make build-pi-zero`를 실행하여 둘 다 빌드하세요.

## 🚀 빠른 시작 가이드

### 🌐 WebUI Launcher(데스크톱 권장)

WebUI Launcher는 구성과 채팅을 위한 브라우저 기반 인터페이스를 제공합니다. 명령줄 지식 없이 쉽게 시작할 수 있습니다.

**옵션 1: 더블클릭(데스크톱)**

[GitHub Releases](https://github.com/stpinkie/rhizome/releases)에서 다운로드한 후 `rhizome-launcher`(Windows에서는 `rhizome-launcher.exe`)를 더블클릭하세요. 브라우저가 `http://localhost:18800`에서 자동으로 열립니다.

**옵션 2: 명령줄**

```bash
rhizome-launcher
# 브라우저에서 http://localhost:18800 열기
```

> [!TIP]
> **원격 접속 / Docker / VM:** 모든 인터페이스에서 수신하려면 `-public` 플래그를 추가하세요:
> ```bash
> rhizome-launcher -public
> ```

<p align="center">
<img src="../../assets/launcher-webui.jpg" alt="WebUI Launcher" width="600">
</p>

**시작하기:**

WebUI를 연 다음: **1)** 프로바이더 구성(LLM API 키 추가) → **2)** 채널 구성(예: Telegram) → **3)** Gateway 시작 → **4)** 채팅!

자세한 문서는 이 저장소의 [docs/ 폴더](https://github.com/stpinkie/rhizome/tree/main/docs)를 참고하세요.

<details>
<summary><b>Docker(대안)</b></summary>

```bash
# 1. 이 저장소 복제
git clone https://github.com/stpinkie/rhizome.git
cd rhizome

# 2. 첫 실행 — docker/data/config.json을 자동 생성한 후 종료
#    (config.json과 workspace/가 모두 없을 때만 트리거)
docker compose -f docker/docker-compose.yml --profile launcher up
# 컨테이너가 "First-run setup complete."를 출력하고 중지됩니다.

# 3. API 키 설정
vim docker/data/config.json

# 4. 시작
docker compose -f docker/docker-compose.yml --profile launcher up -d
# http://localhost:18800 열기
```

> **Docker / VM 사용자:** Gateway는 기본적으로 `127.0.0.1`에서 수신합니다. 호스트에서 접근하려면 `RHIZOME_GATEWAY_HOST=0.0.0.0`을 설정하거나 `-public` 플래그를 사용하세요.

```bash
# 로그 확인
docker compose -f docker/docker-compose.yml logs -f

# 중지
docker compose -f docker/docker-compose.yml --profile launcher down

# 업데이트
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

</details>

<details>
<summary><b>macOS — 첫 실행 보안 경고</b></summary>

macOS는 인터넷에서 다운로드되었고 Mac App Store를 통해 공증되지 않았기 때문에 첫 실행 시 `rhizome-launcher`를 차단할 수 있습니다.

**1단계:** `rhizome-launcher`를 더블클릭하세요. 보안 경고가 표시됩니다:

<p align="center">
<img src="../../assets/macos-gatekeeper-warning.jpg" alt="macOS Gatekeeper 경고" width="400">
</p>

> *"rhizome-launcher"를 열 수 없음 — Apple에서 "rhizome-launcher"가 Mac을 손상시키거나 개인정보를 침해할 수 있는 맬웨어가 없는지 확인할 수 없습니다.*

**2단계:** **시스템 설정** → **개인정보 보호 및 보안** → **보안** 섹션으로 스크롤 → **그래도 열기** 클릭 → 대화 상자에서 **그래도 열기** 클릭.

<p align="center">
<img src="../../assets/macos-gatekeeper-allow.jpg" alt="macOS 개인정보 보호 및 보안 — 그래도 열기" width="600">
</p>

이 일회성 단계 후에는 이후 실행 시 `rhizome-launcher`가 정상적으로 열립니다.

</details>

<a id="-run-on-old-android-phones"></a>
### 📱 Android

10년 된 휴대폰에 새 삶을 주세요! Rhizome로 스마트 AI 어시스턴트로 만들 수 있습니다.

**옵션 1: APK 설치**

미리보기:

<table>
  <tr>
    <td><img src="../../assets/fui_main_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_web_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_log_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_setting_page.jpg" width="200"></td>
  </tr>
</table>

Android APK는 이 포크에서 현재 배포되지 않습니다. 소스에서 빌드하거나 향후 APK는 [GitHub Releases](https://github.com/stpinkie/rhizome/releases)를 확인하세요.

**옵션 2: Termux**

전체 명령줄 설정 체크리스트는 [Android Termux 가이드](../guides/android-termux.md)를 참고하세요.

<details>
<summary><b>터미널 런처(리소스 제한 환경용)</b></summary>

1. [Termux](https://github.com/termux/termux-app) 설치([GitHub Releases](https://github.com/termux/termux-app/releases)에서 다운로드하거나 F-Droid / Google Play에서 검색)
2. 다음 명령어를 실행:

```bash
# 최신 릴리스 다운로드
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz
pkg install proot
termux-chroot ./rhizome onboard   # chroot는 표준 Linux 파일 시스템 레이아웃 제공
```

그런 다음 아래 터미널 런처 섹션에 따라 구성을 완료하세요.

<img src="../../assets/termux.jpg" alt="Rhizome on Termux" width="512">

`rhizome` 코어 바이너리만 사용할 수 있는 최소 환경(Launcher UI 없음)에서는 명령줄과 JSON 구성 파일로 모든 것을 구성할 수 있습니다.

**1. 초기화**

```bash
rhizome onboard
```

이 명령은 `~/.rhizome/config.json`과 워크스페이스 디렉터리를 생성합니다.

**2. 구성** (`~/.rhizome/config.json`)

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
      // api_key는 이제 .security.yml에서 로드됩니다
    }
  ]
}
```

> 사용 가능한 모든 옵션이 포함된 전체 구성 템플릿은 저장소의 `config/config.example.json`을 참고하세요.
>
> 참고: config.example.json은 version 0 형식이며 민감 코드를 포함합니다. version 1+로 자동 마이그레이션되면 config.json에는 비민감 데이터만 저장되고 민감 코드는 .security.yml에 저장됩니다. 코드를 수동으로 수정하려면 `docs/security/security_configuration.md`를 참고하세요.

**3. 채팅**

```bash
# 단발 질문
rhizome agent -m "What is 2+2?"

# 대화 모드
rhizome agent

# 채팅 앱 통합을 위한 게이트웨이 시작
rhizome gateway
```

</details>

## 🔌 프로바이더(LLM)

Rhizome는 `model_list` 설정을 통해 30개 이상의 LLM 프로바이더를 지원합니다. 형식은 `protocol/model`입니다.

| 프로바이더 | 프로토콜 | API Key | 비고 |
|----------|----------|---------|------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | 필수 | GPT-5.4, GPT-4o, o3 등 |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | 필수 | Claude Opus 4.6, Sonnet 4.6 등 |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | 필수 | Gemini 3 Flash, 2.5 Pro 등 |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | 필수 | 200개 이상의 모델, 통합 API |
| [Zhipu (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | 필수 | GLM-4.7, GLM-5 등 |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | 필수 | DeepSeek-V3, DeepSeek-R1 |
| [Volcengine](https://console.volcengine.com) | `volcengine/` | 필수 | Doubao, Ark 모델 |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | 필수 | Qwen3, Qwen-Max 등 |
| [Groq](https://console.groq.com/keys) | `groq/` | 필수 | 빠른 추론(Llama, Mixtral) |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | 필수 | Kimi 모델 |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | 필수 | MiniMax 모델 |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | 필수 | Mistral Large, Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | 필수 | NVIDIA 호스팅 모델 |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | 필수 | 빠른 추론 |
| [Novita AI](https://novita.ai/) | `novita/` | 필수 | 다양한 오픈 모델 |
| [Xiaomi MiMo](https://platform.xiaomimimo.com/) | `mimo/` | 필수 | MiMo 모델 |
| [Ollama](https://ollama.com/) | `ollama/` | 불필요 | 로컬 모델, 셀프 호스팅 |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | 불필요 | 로컬 배포, OpenAI 호환 |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | 환경에 따라 다름 | 100개 이상의 프로바이더를 위한 프록시 |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | 필수 | 엔터프라이즈 Azure 배포 |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | 디바이스 코드 로그인 |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |
| [AWS Bedrock](https://console.aws.amazon.com/bedrock)* | `bedrock/` | AWS 자격 증명 | AWS에서 Claude, Llama, Mistral 사용 |

> \* AWS Bedrock은 빌드 태그 `go build -tags bedrock`이 필요합니다. 모든 AWS 파티션(aws, aws-cn, aws-us-gov)에서 엔드포인트를 자동 해석하려면 `api_base`를 리전명(예: `us-east-1`)으로 설정하세요. 전체 엔드포인트 URL을 직접 사용할 경우에는 환경 변수 또는 AWS config/profile을 통해 `AWS_REGION`도 함께 설정해야 합니다.

<details>
<summary><b>로컬 배포(Ollama, vLLM 등)</b></summary>

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

프로바이더 전체 설정은 [프로바이더와 모델](../guides/providers.md)을 참고하세요.

</details>

## 💬 채널(채팅 앱)

18개 이상의 메시징 플랫폼을 통해 Rhizome와 대화할 수 있습니다.

| 채널 | 설정 | 프로토콜 | 문서 |
|---------|------|----------|------|
| **Telegram** | 쉬움(봇 토큰) | Long polling | [가이드](../channels/telegram/README.md) |
| **Discord** | 쉬움(봇 토큰 + intents) | WebSocket | [가이드](../channels/discord/README.md) |
| **WhatsApp** | 쉬움(QR 스캔 또는 브리지 URL) | Native / Bridge | [가이드](../guides/chat-apps.md#whatsapp) |
| **Weixin** | 쉬움(네이티브 QR 스캔) | iLink API | [가이드](../guides/chat-apps.md#weixin) |
| **QQ** | 쉬움(AppID + AppSecret) | WebSocket | [가이드](../channels/qq/README.md) |
| **Slack** | 쉬움(봇 + 앱 토큰) | Socket Mode | [가이드](../channels/slack/README.md) |
| **Matrix** | 중간(homeserver + 토큰) | Sync API | [가이드](../channels/matrix/README.md) |
| **DingTalk** | 중간(클라이언트 자격 증명) | Stream | [가이드](../channels/dingtalk/README.md) |
| **Feishu / Lark** | 중간(App ID + Secret) | WebSocket/SDK | [가이드](../channels/feishu/README.md) |
| **LINE** | 중간(인증 정보 + webhook) | Webhook | [가이드](../channels/line/README.md) |
| **WeCom** | 쉬움(QR 로그인 또는 수동 설정) | WebSocket | [가이드](../channels/wecom/README.md) |
| **VK** | 쉬움(그룹 토큰) | Long Poll | [가이드](../channels/vk/README.md) |
| **IRC** | 중간(서버 + 닉네임) | IRC protocol | [가이드](../guides/chat-apps.md#irc) |
| **OneBot** | 중간(WebSocket URL) | OneBot v11 | [가이드](../channels/onebot/README.md) |
| **MaixCam** | 쉬움(활성화) | TCP socket | [가이드](../channels/maixcam/README.md) |
| **Pico** | 쉬움(활성화) | 네이티브 프로토콜 | 내장 |
| **Pico Client** | 쉬움(WebSocket URL) | WebSocket | 내장 |

> webhook 기반 채널은 모두 하나의 게이트웨이 HTTP 서버(`gateway.host`:`gateway.port`, 기본값 `127.0.0.1:18790`)를 공유합니다. Feishu는 WebSocket/SDK 모드를 사용하며 이 공용 HTTP 서버를 사용하지 않습니다.

> 로그 상세도는 `gateway.log_level`(기본값: `warn`)로 제어됩니다. 지원 값은 `debug`, `info`, `warn`, `error`, `fatal`입니다. `RHIZOME_LOG_LEVEL` 환경 변수로도 설정할 수 있습니다. 자세한 내용은 [설정 문서](../guides/configuration.md#gateway-log-level)를 참고하세요.

자세한 채널 설정 방법은 [채팅 앱 설정 가이드](../guides/chat-apps.md)를 참고하세요.

## 🔧 도구

### 🔍 웹 검색

Rhizome는 최신 정보를 제공하기 위해 웹 검색을 수행할 수 있습니다. `tools.web`에서 설정하세요.

| 검색 엔진 | API Key | 무료 제공량 | 링크 |
|-----------|---------|-------------|------|
| DuckDuckGo | 불필요 | 무제한 | 내장 백업 검색 |
| [Baidu Search](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | 필수 | 월 1500회 쿼리(일할당) | AI 기반, 중국 시장 최적화 |
| [Tavily](https://tavily.com) | 필수 | 월 1000회 쿼리 | AI 에이전트에 최적화 |
| [Brave Search](https://brave.com/search/api) | 필수 | 월 2000회 쿼리 | 빠르고 프라이빗함 |
| [Perplexity](https://www.perplexity.ai) | 필수 | 유료 | AI 기반 검색 |
| [SearXNG](https://github.com/searxng/searxng) | 불필요 | 셀프 호스팅 | 무료 메타 검색 엔진 |
| [GLM Search](https://open.bigmodel.cn/) | 필수 | 상이함 | Zhipu 웹 검색 |

### ⚙️ 기타 도구

Rhizome에는 파일 작업, 코드 실행, 스케줄링 등을 위한 내장 도구가 포함되어 있습니다. 자세한 내용은 [도구 설정](../reference/tools_configuration.md)을 참고하세요.

## 🎯 스킬

스킬은 에이전트 기능을 확장하는 모듈형 구성 요소입니다. 워크스페이스 안의 `SKILL.md` 파일에서 로드됩니다.

**ClawHub에서 스킬 설치:**

```bash
rhizome skills search "web scraping"
rhizome skills install <skill-name>
```

**ClawHub 토큰 설정**(선택 사항, 더 높은 호출 한도용):

`config.json`에 다음을 추가하세요.
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

자세한 내용은 [도구 설정 - 스킬](../reference/tools_configuration.md#skills-tool)를 참고하세요.

## 🔗 MCP (Model Context Protocol)

Rhizome는 [MCP](https://modelcontextprotocol.io/)를 기본 지원합니다. 어떤 MCP 서버든 연결하여 외부 도구와 데이터 소스로 에이전트 기능을 확장할 수 있습니다.

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

MCP 전체 설정(stdio, SSE, HTTP 전송 방식, 도구 탐색)은 [도구 설정 - MCP](../reference/tools_configuration.md#mcp-tool)를 참고하세요.

## <img src="../../assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> 에이전트 소셜 네트워크 참여하기

CLI 또는 통합된 채팅 앱에서 메시지를 한 번만 보내면 Rhizome를 에이전트 소셜 네트워크에 연결할 수 있습니다.

**`https://clawdchat.ai/skill.md`를 읽고 안내에 따라 [ClawdChat.ai](https://clawdchat.ai)에 참여하세요**

## 🖥️ CLI 레퍼런스

| 명령어                    | 설명                           |
| ------------------------- | ------------------------------ |
| `rhizome onboard`        | 설정 및 워크스페이스 초기화    |
| `rhizome auth weixin`    | QR로 WeChat 계정 연결          |
| `rhizome agent -m "..."` | 에이전트와 채팅               |
| `rhizome agent`          | 대화형 채팅 모드               |
| `rhizome gateway`        | 게이트웨이 시작                |
| `rhizome status`         | 상태 표시                      |
| `rhizome version`        | 버전 정보 표시                 |
| `rhizome model`          | 기본 모델 조회 또는 변경       |
| `rhizome cron list`      | 모든 예약 작업 목록 표시       |
| `rhizome cron add ...`   | 예약 작업 추가                 |
| `rhizome cron disable`   | 예약 작업 비활성화             |
| `rhizome cron remove`    | 예약 작업 삭제                 |
| `rhizome skills list`    | 설치된 스킬 목록 표시          |
| `rhizome skills install` | 스킬 설치                      |
| `rhizome migrate`        | 이전 버전 데이터 마이그레이션  |
| `rhizome auth login`     | 프로바이더 인증                |

### ⏰ 예약 작업 / 리마인더

Rhizome는 `cron` 도구를 통해 예약 리마인더와 반복 작업을 지원합니다.

* **1회성 리마인더**: "10분 후에 알려줘" -> 10분 후 한 번 실행
* **반복 작업**: "2시간마다 알려줘" -> 2시간마다 실행
* **Cron 표현식**: "매일 오전 9시에 알려줘" -> cron 표현식 사용

현재 지원하는 스케줄 유형, 실행 모드, 명령 작업 게이트, 저장 방식은 [docs/reference/cron.md](../reference/cron.md)를 참고하세요.

## 📚 문서

이 README보다 더 자세한 가이드는 다음 문서를 참고하세요.

| 주제 | 설명 |
|------|------|
| [도커 & 빠른 시작](../guides/docker.md) | Docker Compose 설정, 런처/에이전트 모드 |
| [채팅 앱](../guides/chat-apps.md) | 17개 이상의 채널 설정 가이드 |
| [설정](../guides/configuration.md) | 환경 변수, 워크스페이스 레이아웃, 보안 샌드박스 |
| [예약 작업과 Cron](../reference/cron.md) | Cron 스케줄 유형, 전달 모드, 명령 게이트, 작업 저장 |
| [프로바이더와 모델](../guides/providers.md) | 30개 이상의 LLM 프로바이더, 모델 라우팅, model_list 설정 |
| [Spawn & 비동기 작업](../guides/spawn-tasks.md) | 빠른 작업, spawn을 이용한 장기 작업, 비동기 서브에이전트 오케스트레이션 |
| [Hooks](../architecture/hooks/README.md) | 이벤트 기반 Hook 시스템: 관찰자, 인터셉터, 승인 훅 |
| [Steering](../architecture/steering.md) | 실행 중인 에이전트 루프에서 도구 호출 사이에 메시지 주입 |
| [SubTurn](../architecture/subturn.md) | 서브에이전트 조정, 동시성 제어, 생명주기 |
| [문제 해결](../operations/troubleshooting.md) | 자주 발생하는 문제와 해결 방법 |
| [도구 설정](../reference/tools_configuration.md) | 도구별 활성화/비활성화, exec 정책, MCP, 스킬 |
| [하드웨어 호환성](../guides/hardware-compatibility.md) | 테스트된 보드, 최소 요구사항 |

## 🤝 기여 & 로드맵

PR은 언제든 환영합니다! 코드베이스는 의도적으로 작고 읽기 쉽게 유지하고 있습니다.

가이드라인은 [커뮤니티 로드맵](https://github.com/stpinkie/rhizome/issues/988)과 [CONTRIBUTING.md](../../CONTRIBUTING.md)를 참고하세요.

개발자 그룹도 준비 중입니다. 첫 PR이 머지되면 함께할 수 있습니다!

커뮤니티 그룹:

Discord: <https://discord.gg/V4sAZ9XWpN>

WeChat:
<img src="../../assets/wechat.png" alt="WeChat group QR code" width="512">