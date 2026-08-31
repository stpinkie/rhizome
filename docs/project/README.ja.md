<div align="center">
<img src="../../assets/logo.webp" alt="Rhizome" width="512">

<h1>Rhizome: Go ベースの超効率的 AI アシスタント</h1>

<h3>$10 ハードウェア · 10MB RAM · ms ブート · Let's Go, Rhizome!</h3>
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

[中文](README.zh.md) | **日本語** | [한국어](README.ko.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Italiano](README.it.md) | [Bahasa Indonesia](README.id.md) | [Malay](README.ms.md) | [English](../../README.md)

</div>

---

> **Rhizome** は [PicoClaw](https://github.com/sipeed/picoclaw) のコミュニティ維持ハードフォークです。完全に **Go** で書かれ、超軽量な個人 AI アシスタントという目標を引き継いでいます。

**Rhizome** は [NanoBot](https://github.com/HKUDS/nanobot) に着想を得た個人 AI アシスタントです。オリジナルの PicoClaw のアイデアに、Go ネイティブの P2P メッシュ、ワークスペース同期、エージェントゲートウェイを追加しました。

**単一の Go バイナリ、実行時依存関係なし** — Linux、Windows、macOS、FreeBSD/NetBSD、Android でネイティブに実行されます。検証済みボードと現在の 2 段階リソース要件については [ハードウェア互換性リスト](../guides/hardware-compatibility.md)を参照してください。

<p align="center">
<img src="../../assets/rhizome_mem.gif" width="360" height="240">
</p>

> [!CAUTION]
> **セキュリティに関する注意**
>
> * **NO CRYPTO:** Rhizome は公式トークンや暗号通貨を **発行していません**。`pump.fun` や他の取引プラットフォームでの関連する主張はすべて **詐欺**です。
> * **CANONICAL SOURCE:** 公式のソースおよびリリース先は **<https://github.com/stpinkie/rhizome>** で、リリースは GitHub Releases に公開されています。公式を称するサードパーティドメインに注意してください。
> * **警告:** `.ai/.org/.com/.net/...` などの多くのドメインがサードパーティに登録されています。信頼しないでください。
> * **注意:** Rhizome は初期の高速機能開発段階にあります。未解決のセキュリティ問題が存在する可能性があります。v1.0 正式リリース前に本番環境にデプロイしないでください。
> * **注意:** 完全な `rhizome` バイナリは約 98MB で、デーモンは約 60MB のプライベートメモリを使用します。超小型ボードのフットプリントをさらに削減するために `nonetwork` ビルドを計画しています。リソース最適化は機能が安定した後に行う予定です。

## 📢 ニュース

2026-05-28 🚀 **v0.2.9 リリース！** Web UI での MCP サーバー管理、設定可能な Sogou ウェブ検索、チャンネルツールフィードバックアニメーション、`pretty_print` および `disable_escape_html` のデフォルト値、プロバイダーとチャンネルのさまざまなバグ修正。

2026-05-14 🚀 **v0.2.8 リリース！** MCP CLI コマンド（`show`、`add`、`list`、`remove`、`test`、`edit`）、MCP ツールパラメーターの null の代わりの空オブジェクト、ビルド修正。

2026-05-07 🚀 **v0.2.7 リリース！** 設定可能な Sogou ウェブ検索、チャンネルツールフィードバックアニメーション、リンター修正。

2026-04-23 🚀 **v0.2.6 リリース！** 応答アクションを伴う Hook と包括的なドキュメント、分離サポート、ヘルプバナー修正。

2026-04-11 🚀 **v0.2.5 リリース！** TZ/ZONEINFO 環境変数からの Zoneinfo 取得、Matrix CommonMark レンダリングの調整、行単位 `read_file`。

2026-03-31 📱 **Android サポート！** Rhizome が Android で実行されるようになりました！Android APK はこのフォークからは現在配布されていません。ソースからビルドするか、将来の APK は [GitHub Releases](https://github.com/stpinkie/rhizome/releases) を確認してください。

2026-03-25 🚀 **v0.2.4 リリース！** エージェントアーキテクチャの全面再設計（SubTurn、Hook、Steering、EventBus）、WeChat/WeCom 深層統合、セキュリティ体制の強化（.security.yml、機密データフィルタリング）、新規プロバイダー（AWS Bedrock、Azure、小米 MiMo）および 35 のバグ修正。Rhizome は **26K Stars** を達成！

2026-03-17 🚀 **v0.2.3 リリース！** システムトレイ UI（Windows & Linux）、サブエージェント状態照会（`spawn_status`）、実験的 Gateway ホットリロード、Cron セーフティゲート、2 件のセキュリティ修正。Rhizome は **25K Stars** を達成！

2026-03-09 🎉 **v0.2.1 — これまでで最大の更新！** MCP プロトコルサポート、4 つの新チャンネル（Matrix/IRC/WeCom/Discord Proxy）、3 つの新プロバイダー（Kimi/Minimax/Avian）、ビジョンパイプライン、JSONL メモリストレージ、モデルルーティング。

2026-02-28 📦 **v0.2.0** が Docker Compose および Web UI Launcher サポートと共にリリースされました。

<details>
<summary>それ以前のニュース...</summary>

2026-02-26 🎉 Rhizome はわずか 17 日間で **20K Stars** を達成！チャンネル自動オーケストレーションと能力インターフェースが利用可能に。

2026-02-16 🎉 Rhizome は 1 週間で 12K Stars を突破！コミュニティメンテナー役割と [ロードマップ](../../ROADMAP.md) が正式発表。

2026-02-13 🎉 Rhizome は 4 日間で 5000 Stars を突破！プロジェクトロードマップと開発者グループ構築中。

2026-02-09 🎉 **Rhizome 公式リリース！** 超軽量 AI エージェントを 1 日で構築。Let's Go, Rhizome!

</details>

## ✨ 特徴

🪶 **単一バイナリ、実行時依存関係なし**: Linux、Windows、macOS、FreeBSD/NetBSD、Android で実行される、静的にリンクされた Go 実行ファイルです。*

💰 **最小コスト**: 幅広い低コスト ARM および RISC-V ボードで実行できるほど効率的です。[ハードウェア互換性リスト](../guides/hardware-compatibility.md)を参照してください。

⚡️ **高速ブート**: 検証済みの低コストボードで 1 秒未満で起動します。

🌍 **真の移植性**: RISC-V、ARM、MIPS、x86 アーキテクチャ間で単一バイナリ。1 つのバイナリでどこでも実行！

🤖 **AI ブートストラップ**: 純粋な Go ネイティブ実装 — コアコードの 95% がエージェントによって生成され、人間によるレビューを通じて微調整されています。

🔌 **MCP サポート**: ネイティブな [Model Context Protocol](https://modelcontextprotocol.io/) 統合 — 任意の MCP サーバーに接続してエージェント機能を拡張します。

👁️ **ビジョンパイプライン**: エージェントに直接画像とファイルを送信 — マルチモーダル LLM 用に自動で base64 エンコードされます。

🧠 **スマートルーティング**: ルールベースのモデルルーティング — 単純なクエリを軽量モデルにルーティングし、API コストを削減します。

_*フットプリントの測定は Windows で `CGO_ENABLED=0`、タグ `goolm,stdjson`、`-ldflags "-s -w"` を使用して行われました；strip 後のバイナリは約 98MB です。超小型ボードのフットプリントをさらに削減するために `nonetwork` ビルドを計画しています。_

<div align="center">

### 現在のビルドフットプリント

| モード | ユースケース | 合計 RAM | 空き RAM | ストレージ |
|--------|-------------|----------|----------|------------|
| **基本** | ワンショット `rhizome agent`、`rhizome onboard` | 256 MB | 128 MB | 128 MB |
| **完全** | P2P、同期、ゲートウェイを備えた `rhizome daemon` | 512 MB | 256 MB | 128 MB |

</div>

> **[ハードウェア互換性リスト](../guides/hardware-compatibility.md)** — Raspberry Pi から Android 携帯まで、検証済みのすべてのボードを参照してください。ボードがリストにない場合は PR を送信してください！

<p align="center">
<img src="../../assets/hardware-banner.jpg" alt="Rhizome Hardware Compatibility" width="100%">
</p>

## 🦾 デモンストレーション

### 🛠️ スタンダードアシスタントワークフロー

<table align="center">
<tr align="center">
<th><p align="center">フルスタックエンジニアモード</p></th>
<th><p align="center">ログと計画</p></th>
<th><p align="center">ウェブ検索と学習</p></th>
</tr>
<tr>
<td align="center"><p align="center"><img src="../../assets/rhizome_code.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_memory.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_search.gif" width="240" height="180"></p></td>
</tr>
<tr>
<td align="center">開発 · デプロイ · スケール</td>
<td align="center">スケジュール · 自動化 · 記憶</td>
<td align="center">発見 · 洞察 · トレンド</td>
</tr>
</table>

### 🐜 革新的な省フットプリントデプロイ

Rhizome は幅広い Linux および組み込みデバイスにデプロイできます！

- $15 [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/)（または [Zero 2 W](https://www.raspberrypi.com/products/raspberry-pi-zero-2-w/)）、最小限のホームアシスタント用
- $50~70 [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/)、RISC-V ベースの組み込み用途向け
- $100 [NanoKVM-Pro](https://www.aliexpress.com/item/1005010048471263.html)、自動化されたサーバー運用向け
- $100 [MaixCAM2](https://www.kickstarter.com/projects/zepan/maixcam2-build-your-next-gen-4k-ai-camera)、スマート監視向け

> 検証済みボードの完全なリストと現在の 2 段階要件については [ハードウェア互換性リスト](../guides/hardware-compatibility.md)を参照してください。

<https://private-user-images.githubusercontent.com/83055338/547056448-e7b031ff-d6f5-4468-bcca-5726b6fecb5c.mp4>

🌟 さらなるデプロイ事例をお楽しみに！

## 📦 インストール

### GitHub Releases からダウンロード（推奨）

[GitHub Releases](https://github.com/stpinkie/rhizome/releases) ページにアクセスし、自分のプラットフォーム用のバイナリをダウンロードしてください。

### プリコンパイル済みバイナリをダウンロード

または、[GitHub Releases](https://github.com/stpinkie/rhizome/releases) ページから対応プラットフォームのバイナリを手動でダウンロードできます。

### ソースからビルド（開発用）

前提条件:

- Go 1.25+
- Web UI / launcher ビルド用の Node.js 22+ および pnpm 10.33.0+

```bash
git clone https://github.com/stpinkie/rhizome.git

cd rhizome
make deps

# フロントエンドの依存関係をインストール
(cd web/frontend && pnpm install --frozen-lockfile)

# 現在のプラットフォーム用のコアバイナリをビルド
make build

# Web UI Launcher をビルド（WebUI モードに必要）
make build-launcher

# Makefile で管理されるすべてのプラットフォーム用のコアバイナリをビルド
make build-all

# Raspberry Pi Zero 2 W 用にビルド
# 32 ビット: make build-linux-arm
# 64 ビット: make build-linux-arm64
make build-pi-zero

# ビルドしてインストール
make install
```

**Raspberry Pi Zero 2 W:** OS に一致するバイナリを使用してください。32 ビット Raspberry Pi OS → `make build-linux-arm`；64 ビット → `make build-linux-arm64`。または `make build-pi-zero` を実行して両方をビルドします。

## 🚀 クイックスタートガイド

### 🌐 WebUI Launcher（デスクトップ向け推奨）

WebUI Launcher は、ブラウザベースの設定・チャットインターフェースを提供します。コマンドラインの知識がなくても最も簡単に始められます。

**オプション 1: ダブルクリック（デスクトップ）**

[GitHub Releases](https://github.com/stpinkie/rhizome/releases) からダウンロード後、`rhizome-launcher`（Windows では `rhizome-launcher.exe`）をダブルクリックします。ブラウザが自動的に `http://localhost:18800` を開きます。

**オプション 2: コマンドライン**

```bash
rhizome-launcher
# ブラウザで http://localhost:18800 を開く
```

> [!TIP]
> **リモートアクセス / Docker / VM:** すべてのインターフェースでリッスンするには `-public` フラグを追加します:
> ```bash
> rhizome-launcher -public
> ```

<p align="center">
<img src="../../assets/launcher-webui.jpg" alt="WebUI Launcher" width="600">
</p>

**はじめに:**

WebUI を開いてから: **1)** プロバイダーを設定（LLM API キーを追加） → **2)** チャンネルを設定（例: Telegram） → **3)** ゲートウェイを起動 → **4)** チャット！

詳細なドキュメントについては、このリポジトリの [docs/ フォルダ](https://github.com/stpinkie/rhizome/tree/main/docs)を参照してください。

<details>
<summary><b>Docker（代替）</b></summary>

```bash
# 1. このリポジトリをクローン
git clone https://github.com/stpinkie/rhizome.git
cd rhizome

# 2. 初回実行 — docker/data/config.json を自動生成して終了
#    （config.json と workspace/ の両方が存在しない場合にのみトリガー）
docker compose -f docker/docker-compose.yml --profile launcher up
# コンテナが "First-run setup complete." と出力して停止します。

# 3. API キーを設定
vim docker/data/config.json

# 4. 起動
docker compose -f docker/docker-compose.yml --profile launcher up -d
# http://localhost:18800 を開く
```

> **Docker / VM ユーザー:** ゲートウェイはデフォルトで `127.0.0.1` をリッスンします。ホストからアクセス可能にするには、`RHIZOME_GATEWAY_HOST=0.0.0.0` を設定するか `-public` フラグを使用してください。

```bash
# ログを確認
docker compose -f docker/docker-compose.yml logs -f

# 停止
docker compose -f docker/docker-compose.yml --profile launcher down

# 更新
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

</details>

<details>
<summary><b>macOS — 初回起動時のセキュリティ警告</b></summary>

macOS は、インターネットからダウンロードされ Mac App Store を通じて公証されていないため、初回起動時に `rhizome-launcher` をブロックする場合があります。

**ステップ 1:** `rhizome-launcher` をダブルクリックします。セキュリティ警告が表示されます:

<p align="center">
<img src="../../assets/macos-gatekeeper-warning.jpg" alt="macOS Gatekeeper 警告" width="400">
</p>

> *「rhizome-launcher」を開けません — Apple は「rhizome-launcher」が Mac を損傷させたりプライバシーを侵害する可能性のあるマルウェアを含んでいないことを確認できません。*

**ステップ 2:** **システム設定** → **プライバシーとセキュリティ** → **セキュリティ** セクションまでスクロール → **それでも開く** をクリック → ダイアログで **それでも開く** をクリックして確定します。

<p align="center">
<img src="../../assets/macos-gatekeeper-allow.jpg" alt="macOS プライバシーとセキュリティ — それでも開く" width="600">
</p>

この 1 回限りの操作後、以降の起動では `rhizome-launcher` が正常に開きます。

</details>

<a id="-run-on-old-android-phones"></a>
### 📱 Android

10年前の古い携帯に第二の人生を！Rhizome でスマート AI アシスタントに変身させましょう。

**オプション 1: APK インストール**

プレビュー:

<table>
  <tr>
    <td><img src="../../assets/fui_main_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_web_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_log_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_setting_page.jpg" width="200"></td>
  </tr>
</table>

Android APK はこのフォークから現在公開されていません。ソースからビルドするか、将来の APK は [GitHub Releases](https://github.com/stpinkie/rhizome/releases) を確認してください。

**オプション 2: Termux**

完全なコマンドライン設定チェックリストについては [Android Termux ガイド](../guides/android-termux.md)を参照してください。

<details>
<summary><b>ターミナル Launcher（リソース制約環境向け）</b></summary>

1. [Termux](https://github.com/termux/termux-app) をインストール（[GitHub Releases](https://github.com/termux/termux-app/releases) からダウンロードするか、F-Droid / Google Play で検索）
2. 次のコマンドを実行:

```bash
# 最新リリースをダウンロード
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz
pkg install proot
termux-chroot ./rhizome onboard   # chroot は標準の Linux ファイルシステムレイアウトを提供
```

次に、以下のターミナル Launcher セクションに従って設定を完了します。

<img src="../../assets/termux.jpg" alt="Rhizome on Termux" width="512">

`rhizome` コアバイナリのみが利用可能な最小限の環境（Launcher UI なし）では、コマンドラインと JSON 設定ファイルを介してすべてを設定できます。

**1. 初期化**

```bash
rhizome onboard
```

これにより `~/.rhizome/config.json` とワークスペースディレクトリが作成されます。

**2. 設定**（`~/.rhizome/config.json`）

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
      // api_key は現在 .security.yml から読み込まれます
    }
  ]
}
```

> 利用可能なすべてのオプションを含む完全な設定テンプレートは、リポジトリの `config/config.example.json` を参照してください。
>
> 注意: config.example.json は version 0 形式で機密コードを含み、version 1+ に自動マイグレーションされます。その後、config.json は非機密データのみを保存し、機密コードは .security.yml に保存されます。コードを手動で変更する必要がある場合は `docs/security/security_configuration.md` を参照してください。

**3. チャット**

```bash
# 1 回限りの質問
rhizome agent -m "What is 2+2?"

# 対話モード
rhizome agent

# チャットアプリ統合のためのゲートウェイを起動
rhizome gateway
```

</details>

## 🔌 Provider（LLM）

Rhizome は `model_list` 設定を通じて 30 以上の LLM Provider をサポートしています。`protocol/model` 形式を使用してください：

| Provider | Protocol | API キー | 備考 |
|----------|----------|---------|------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | 必須 | GPT-5.4、GPT-4o、o3 など |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | 必須 | Claude Opus 4.6、Sonnet 4.6 など |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | 必須 | Gemini 3 Flash、2.5 Pro など |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | 必須 | 200 以上のモデル、統合 API |
| [Zhipu (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | 必須 | GLM-4.7、GLM-5 など |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | 必須 | DeepSeek-V3、DeepSeek-R1 |
| [Volcengine](https://console.volcengine.com) | `volcengine/` | 必須 | Doubao、Ark モデル |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | 必須 | Qwen3、Qwen-Max など |
| [Groq](https://console.groq.com/keys) | `groq/` | 必須 | 高速推論（Llama、Mixtral） |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | 必須 | Kimi モデル |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | 必須 | MiniMax モデル |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | 必須 | Mistral Large、Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | 必須 | NVIDIA ホスティングモデル |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | 必須 | 高速推論 |
| [Novita AI](https://novita.ai/) | `novita/` | 必須 | 各種オープンモデル |
| [Xiaomi MiMo](https://platform.xiaomimimo.com/) | `mimo/` | 必須 | MiMo モデル |
| [Ollama](https://ollama.com/) | `ollama/` | 不要 | ローカルモデル、セルフホスト |
| [vLLM](https://docs.vllm.ai/) | `vLLM/` | 不要 | ローカルデプロイ、OpenAI 互換 |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | 場合による | 100 以上の Provider のプロキシ |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | 必須 | エンタープライズ Azure デプロイ |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | デバイスコードログイン |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |

<details>
<summary><b>ローカルデプロイ（Ollama、vLLM など）</b></summary>

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

Provider の完全な設定詳細は [Provider とモデル](../guides/providers.ja.md) を参照してください。

</details>

## 💬 Channel（チャットアプリ）

17 以上のメッセージングプラットフォームで Rhizome と会話できます：

| Channel | セットアップ | Protocol | ドキュメント |
|---------|------------|----------|------------|
| **Telegram** | 簡単（bot トークン） | Long polling | [ガイド](../channels/telegram/README.ja.md) |
| **Discord** | 簡単（bot トークン + intents） | WebSocket | [ガイド](../channels/discord/README.ja.md) |
| **WhatsApp** | 簡単（QR スキャンまたは bridge URL） | Native / Bridge | [ガイド](../guides/chat-apps.ja.md#whatsapp) |
| **微信 (Weixin)** | 簡単（QR スキャン） | iLink API | [ガイド](../guides/chat-apps.ja.md#weixin) |
| **QQ** | 簡単（AppID + AppSecret） | WebSocket | [ガイド](../channels/qq/README.ja.md) |
| **Slack** | 簡単（bot + app トークン） | Socket Mode | [ガイド](../channels/slack/README.ja.md) |
| **Matrix** | 中級（homeserver + トークン） | Sync API | [ガイド](../channels/matrix/README.ja.md) |
| **DingTalk** | 中級（クライアント認証情報） | Stream | [ガイド](../channels/dingtalk/README.ja.md) |
| **Feishu / Lark** | 中級（App ID + Secret） | WebSocket/SDK | [ガイド](../channels/feishu/README.ja.md) |
| **LINE** | 中級（認証情報 + webhook） | Webhook | [ガイド](../channels/line/README.ja.md) |
| **WeCom** | 簡単（QR ログインまたは手動） | WebSocket | [ガイド](../channels/wecom/README.ja.md) |
| **IRC** | 中級（サーバー + nick） | IRC protocol | [ガイド](../guides/chat-apps.ja.md#irc) |
| **OneBot** | 中級（WebSocket URL） | OneBot v11 | [ガイド](../channels/onebot/README.ja.md) |
| **MaixCam** | 簡単（有効化） | TCP socket | [ガイド](../channels/maixcam/README.ja.md) |
| **Pico** | 簡単（有効化） | Native protocol | 内蔵 |
| **Pico Client** | 簡単（WebSocket URL） | WebSocket | 内蔵 |

> webhook ベースのすべての Channel は単一の Gateway HTTP サーバー（`gateway.host`:`gateway.port`、デフォルト `127.0.0.1:18790`）を共有します。Feishu は WebSocket/SDK モードを使用し、共有 HTTP サーバーを使用しません。

> ログの詳細度は `gateway.log_level` で制御します（デフォルト：`warn`）。サポートされる値：`debug`、`info`、`warn`、`error`、`fatal`。`RHIZOME_LOG_LEVEL` 環境変数でも設定可能です。詳細は[設定ガイド](../guides/configuration.ja.md#gateway-ログレベル)を参照してください。

Channel の詳細なセットアップ手順は [チャットアプリ設定](../guides/chat-apps.ja.md) を参照してください。

## 🔧 ツール

### 🔍 Web 検索

Rhizome は最新情報を提供するために Web を検索できます。`tools.web` で設定してください：

| 検索エンジン | API キー | 無料枠 | リンク |
|------------|---------|--------|-------|
| DuckDuckGo | 不要 | 無制限 | 内蔵フォールバック |
| [Baidu Search](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | 必須 | 1500 クエリ/月（日次割り当て） | AI 搭載、中国語に最適化 |
| [Tavily](https://tavily.com) | 必須 | 1000 クエリ/月 | AI Agent 向けに最適化 |
| [Brave Search](https://brave.com/search/api) | 必須 | 2000 クエリ/月 | 高速でプライベート |
| [Perplexity](https://www.perplexity.ai) | 必須 | 有料 | AI 搭載検索 |
| [SearXNG](https://github.com/searxng/searxng) | 不要 | セルフホスト | 無料メタ検索エンジン |
| [GLM Search](https://open.bigmodel.cn/) | 必須 | 場合による | Zhipu Web 検索 |

### ⚙️ その他のツール

Rhizome にはファイル操作、コード実行、スケジューリングなどの組み込みツールが含まれています。詳細は [ツール設定](../reference/tools_configuration.ja.md) を参照してください。

## 🎯 Skill

Skill は Agent を拡張するモジュール型の機能です。ワークスペース内の `SKILL.md` ファイルから読み込まれます。

**ClawHub から Skill をインストール：**

```bash
rhizome skills search "web scraping"
rhizome skills install <skill-name>
```

**ClawHub トークンを設定**（オプション、レート制限を上げるため）：

`config.json` に追加：
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

詳細は [ツール設定 - Skill](../reference/tools_configuration.ja.md#skills-tool) を参照してください。

## 🔗 MCP（Model Context Protocol）

Rhizome は [MCP](https://modelcontextprotocol.io/) をネイティブサポートしています — 任意の MCP サーバーに接続して、外部ツールやデータソースで Agent の機能を拡張できます。

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

MCP の完全な設定（stdio、SSE、HTTP トランスポート、Tool Discovery）は [ツール設定 - MCP](../reference/tools_configuration.ja.md#mcp-tool) を参照してください。

## <img src="../../assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> エージェントソーシャルネットワークに参加

CLI または統合チャットアプリからメッセージを 1 つ送るだけで、Rhizome をエージェントソーシャルネットワークに接続できます。

**`https://clawdchat.ai/skill.md` を読み、指示に従って [ClawdChat.ai](https://clawdchat.ai) に参加してください**

## 🖥️ CLI リファレンス

| コマンド                  | 説明                           |
| ------------------------- | ------------------------------ |
| `rhizome onboard`        | 設定＆ワークスペースの初期化     |
| `rhizome auth weixin` | WeChat アカウントを QR で接続 |
| `rhizome agent -m "..."` | Agent とチャット                |
| `rhizome agent`          | インタラクティブチャットモード   |
| `rhizome gateway`        | Gateway を起動                  |
| `rhizome status`         | ステータスを表示                |
| `rhizome version`        | バージョン情報を表示            |
| `rhizome model`          | デフォルトモデルの表示・切替    |
| `rhizome cron list`      | スケジュールジョブ一覧          |
| `rhizome cron add ...`   | スケジュールジョブを追加         |
| `rhizome cron disable`   | スケジュールジョブを無効化       |
| `rhizome cron remove`    | スケジュールジョブを削除         |
| `rhizome skills list`    | インストール済み Skill 一覧      |
| `rhizome skills install` | Skill をインストール             |
| `rhizome migrate`        | 旧バージョンからデータを移行     |
| `rhizome auth login`     | Provider への認証               |

### ⏰ スケジュールタスク / リマインダー

Rhizome は `cron` ツールによるスケジュールリマインダーと定期タスクをサポートしています：

* **ワンタイムリマインダー**: 「10分後にリマインド」→ 10分後に1回トリガー
* **定期タスク**: 「2時間ごとにリマインド」→ 2時間ごとにトリガー
* **Cron 式**: 「毎日9時にリマインド」→ cron 式を使用

## 📚 ドキュメント

この README を超えた詳細なガイドについては：

| トピック | 説明 |
|---------|------|
| [Docker & クイックスタート](../guides/docker.ja.md) | Docker Compose セットアップ、Launcher/Agent モード |
| [チャットアプリ](../guides/chat-apps.ja.md) | 17 以上の Channel セットアップガイド |
| [設定](../guides/configuration.ja.md) | 環境変数、ワークスペース構成、セキュリティサンドボックス |
| [Provider とモデル](../guides/providers.ja.md) | 30 以上の LLM Provider、モデルルーティング、model_list 設定 |
| [Spawn & 非同期タスク](../guides/spawn-tasks.ja.md) | クイックタスク、spawn による長時間タスク、非同期サブエージェントオーケストレーション |
| [Hook システム](../architecture/hooks/README.md) | イベント駆動 Hook：オブザーバー、インターセプター、承認 Hook |
| [Steering](../architecture/steering.md) | 実行中の Agent ループにメッセージを注入 |
| [SubTurn](../architecture/subturn.md) | サブ Agent の調整、並行制御、ライフサイクル |
| [トラブルシューティング](../operations/troubleshooting.ja.md) | よくある問題と解決策 |
| [ツール設定](../reference/tools_configuration.ja.md) | ツールごとの有効/無効、exec ポリシー、MCP、Skill |
| [ハードウェア互換性](../guides/hardware-compatibility.ja.md) | テスト済みボード、最小要件 |

## 🤝 コントリビュート＆ロードマップ

PR 歓迎！コードベースは意図的に小さく読みやすくしています。

[コミュニティロードマップ](https://github.com/stpinkie/rhizome/issues/988)と[CONTRIBUTING.md](../../CONTRIBUTING.md)をご覧ください。

開発者グループ構築中、最初の PR がマージされたら参加できます！

ユーザーグループ:

Discord: <https://discord.gg/V4sAZ9XWpN>

WeChat:
<img src="../../assets/wechat.png" alt="WeChat group QR code" width="512">