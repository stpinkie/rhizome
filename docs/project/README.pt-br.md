<div align="center">
<img src="../../assets/logo.webp" alt="Rhizome" width="512">

<h1>Rhizome: Assistente de IA Ultra-Eficiente em Go</h1>

<h3>Hardware de $10 · 10MB de RAM · Boot em ms · Let's Go, Rhizome!</h3>
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

[中文](README.zh.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | **Português** | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Italiano](README.it.md) | [Bahasa Indonesia](README.id.md) | [Malay](README.ms.md) | [English](../../README.md)

</div>

---

> **Rhizome** é um hard fork mantido pela comunidade do [PicoClaw](https://github.com/sipeed/picoclaw). É escrito inteiramente em **Go** e continua com o objetivo de ser um assistente de IA pessoal ultra-leve.

**Rhizome** é um assistente de IA pessoal ultra-leve inspirado no [NanoBot](https://github.com/HKUDS/nanobot). Ele adiciona uma mesh P2P Go, sincronização de workspace e um gateway de agentes por cima da ideia original do PicoClaw.

**Um único binário Go, sem dependências de runtime** — roda nativamente em Linux, Windows, macOS, FreeBSD/NetBSD e Android. Veja a [Lista de Compatibilidade de Hardware](../guides/hardware-compatibility.pt-br.md) para placas verificadas e os requisitos de recursos atuais dos dois níveis.

<p align="center">
<img src="../../assets/rhizome_mem.gif" width="360" height="240">
</p>

> [!CAUTION]
> **Aviso de Segurança**
>
> * **SEM CRIPTO:** O Rhizome **não** emitiu nenhum token oficial ou criptomoeda. Todas as alegações no `pump.fun` ou outras plataformas de negociação são **golpes**.
> * **FONTE CANÔNICA (CANONICAL SOURCE):** A fonte e o local de lançamento canônicos são **<https://github.com/stpinkie/rhizome>**; os releases são publicados no GitHub Releases. Cuidado com domínios de terceiros que afirmam ser oficiais.
> * **ATENÇÃO:** Muitos domínios `.ai/.org/.com/.net/...` foram registrados por terceiros. Não confie neles.
> * **NOTA:** O Rhizome está em desenvolvimento rápido inicial. Podem existir problemas de segurança não resolvidos. Não implante em produção antes da v1.0.
> * **NOTA:** O binário `rhizome` completo tem cerca de 98 MB e o daemon usa cerca de 60 MB de memória privada. Um build `nonetwork` está planejado para reduzir ainda mais o footprint em placas muito pequenas. A otimização de recursos está planejada após a estabilização de funcionalidades.

## 📢 Novidades

2026-05-28 🚀 **v0.2.9 Lançada!** Gerenciamento de MCP server na Web UI, busca na web via Sogou configurável, animação de feedback de ferramenta nos channels, padrões `pretty_print` e `disable_escape_html`, e diversas correções de bugs em providers e channels.

2026-05-14 🚀 **v0.2.8 Lançada!** Comandos MCP CLI (`show`, `add`, `list`, `remove`, `test`, `edit`), objeto vazio ao invés de null para parâmetros de ferramentas MCP, e correções de build.

2026-05-07 🚀 **v0.2.7 Lançada!** Busca na web via Sogou configurável, animação de feedback de ferramenta nos channels, correções de linter.

2026-04-23 🚀 **v0.2.6 Lançada!** Hooks com ação de respond e documentação abrangente, suporte a isolamento, correção de banner de ajuda.

2026-04-11 🚀 **v0.2.5 Lançada!** Zoneinfo a partir das variáveis de ambiente TZ/ZONEINFO, alinhamento de renderização Matrix CommonMark, `read_file` por linhas.

2026-03-31 📱 **Suporte Android!** Rhizome agora roda no Android! O APK Android não é publicado neste fork; compile a partir do código-fonte ou verifique os [GitHub Releases](https://github.com/stpinkie/rhizome/releases) para uma APK futura.

2026-03-25 🚀 **v0.2.4 Lançada!** Reformulação da arquitetura Agent (SubTurn, Hooks, Steering, EventBus), integração WeChat/WeCom, fortalecimento de segurança (.security.yml, filtragem de dados sensíveis), novos providers (AWS Bedrock, Azure, Xiaomi MiMo) e 35 correções de bugs. O Rhizome atingiu **26K Stars**!

2026-03-17 🚀 **v0.2.3 Lançada!** UI na bandeja do sistema (Windows e Linux), consulta de status de sub-agent (`spawn_status`), hot-reload experimental do Gateway, controle de segurança do Cron e 2 correções de segurança. O Rhizome atingiu **25K Stars**!

2026-03-09 🎉 **v0.2.1 — Maior atualização até agora!** Suporte ao protocolo MCP, 4 novos channels (Matrix/IRC/WeCom/Discord Proxy), 3 novos providers (Kimi/Minimax/Avian), pipeline de visão, armazenamento de memória JSONL, roteamento de modelos.

2026-02-28 📦 **v0.2.0** lançada com suporte a Docker Compose e Web UI Launcher.

<details>
<summary>Notícias anteriores...</summary>

2026-02-26 🎉 O Rhizome atinge **20K Stars** em apenas 17 dias! Orquestração automática de channels e interfaces de capacidade estão disponíveis.

2026-02-16 🎉 O Rhizome ultrapassa 12K Stars em uma semana! Funções de mantenedor da comunidade e [Roadmap](../../ROADMAP.md) lançados oficialmente.

2026-02-13 🎉 O Rhizome ultrapassa 5000 Stars em 4 dias! Roadmap do projeto e grupos de desenvolvedores em andamento.

2026-02-09 🎉 **Rhizome Lançado!** Construído em 1 dia para explorar AI Agents ultra-leves. Let's Go, Rhizome!

</details>

## ✨ Funcionalidades

🪶 **Binário único, sem dependências de runtime**: Um executável Go estaticamente vinculado que roda em Linux, Windows, macOS, FreeBSD/NetBSD e Android.*

💰 **Custo mínimo**: Eficiente o suficiente para rodar em uma ampla gama de placas ARM e RISC-V de baixo custo; veja a [Lista de Compatibilidade de Hardware](../guides/hardware-compatibility.pt-br.md).

⚡️ **Boot ultrarrápido**: Inicializa em menos de 1s nas placas de baixo custo verificadas.

🌍 **Verdadeiramente portátil**: Binário único para arquiteturas RISC-V, ARM, MIPS e x86. Um binário, roda em qualquer lugar!

🤖 **Bootstrapped por IA**: Implementação nativa pura em Go — 95% do código principal foi gerado por um Agent e refinado por revisão humana.

🔌 **Suporte a MCP**: Integração nativa com o [Model Context Protocol](https://modelcontextprotocol.io/) — conecte qualquer servidor MCP para estender as capacidades do Agent.

👁️ **Pipeline de visão**: Envie imagens e arquivos diretamente ao Agent — codificação base64 automática para LLMs multimodais.

🧠 **Roteamento inteligente**: Roteamento de modelos baseado em regras — consultas simples vão para modelos leves, economizando custos de API.

_*A medição de footprint foi feita no Windows com `CGO_ENABLED=0`, tags `goolm,stdjson` e `-ldflags "-s -w"`; o binário stripado tem cerca de 98 MB. Um build `nonetwork` está planejado para reduzir ainda mais em placas muito pequenas._

<div align="center">

### Footprint Atual da Build

| Modo | Caso de uso | RAM Total | RAM Livre | Armazenamento |
|------|-------------|-----------|-----------|---------------|
| **Base** | `rhizome agent`, `rhizome onboard` one-shot | 256 MB | 128 MB | 128 MB |
| **Completo** | `rhizome daemon` com P2P, syncer e gateway | 512 MB | 256 MB | 128 MB |

</div>

> **[Lista de Compatibilidade de Hardware](../guides/hardware-compatibility.pt-br.md)** — Veja todas as placas testadas, do Raspberry Pi a celulares Android. Sua placa não está listada? Envie um PR!

<p align="center">
<img src="../../assets/hardware-banner.jpg" alt="Rhizome Hardware Compatibility" width="100%">
</p>

## 🦾 Demonstração

### 🛠️ Fluxos de Trabalho Padrão do Assistente

<table align="center">
<tr align="center">
<th><p align="center">Modo Engenheiro Full-Stack</p></th>
<th><p align="center">Registro e Planejamento</p></th>
<th><p align="center">Busca na Web e Aprendizado</p></th>
</tr>
<tr>
<td align="center"><p align="center"><img src="../../assets/rhizome_code.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_memory.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_search.gif" width="240" height="180"></p></td>
</tr>
<tr>
<td align="center">Desenvolver · Implantar · Escalar</td>
<td align="center">Agendar · Automatizar · Lembrar</td>
<td align="center">Descobrir · Insights · Tendências</td>
</tr>
</table>

### 🐜 Implantação Inovadora de Baixo Consumo

O Rhizome pode ser implantado em uma ampla gama de dispositivos Linux e embarcados!

- $15 [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) (ou [Zero 2 W](https://www.raspberrypi.com/products/raspberry-pi-zero-2-w/)), para um assistente doméstico mínimo
- $50~70 [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/), para uso embarcado baseado em RISC-V
- $100 [NanoKVM-Pro](https://www.aliexpress.com/item/1005010048471263.html), para operações automatizadas de servidor
- $100 [MaixCAM2](https://www.kickstarter.com/projects/zepan/maixcam2-build-your-next-gen-4k-ai-camera), para vigilância inteligente

> Veja a [Lista de Compatibilidade de Hardware](../guides/hardware-compatibility.pt-br.md) para a lista completa de placas verificadas e os requisitos atuais dos dois níveis.

<https://private-user-images.githubusercontent.com/83055338/547056448-e7b031ff-d6f5-4468-bcca-5726b6fecb5c.mp4>

🌟 Mais Casos de Implantação Aguardam!

## 📦 Instalação

### Download pelo GitHub Releases (Recomendado)

Visite a página [GitHub Releases](https://github.com/stpinkie/rhizome/releases) e baixe o binário para sua plataforma.

### Download do binário pré-compilado

Alternativamente, baixe o binário para sua plataforma na página de [GitHub Releases](https://github.com/stpinkie/rhizome/releases).

### Compilar a partir do código-fonte (para desenvolvimento)

Pré-requisitos:

- Go 1.25+
- Node.js 22+ e pnpm 10.33.0+ para builds do Web UI / launcher

```bash
git clone https://github.com/stpinkie/rhizome.git

cd rhizome
make deps

# Instalar dependências do frontend
(cd web/frontend && pnpm install --frozen-lockfile)

# Build o binário principal para a plataforma atual
make build

# Build o Web UI Launcher (necessário para o modo WebUI)
make build-launcher

# Build os binários principais para todas as plataformas gerenciadas pelo Makefile
make build-all

# Build para Raspberry Pi Zero 2 W
# 32-bit: make build-linux-arm
# 64-bit: make build-linux-arm64
make build-pi-zero

# Build e instalação
make install
```

**Raspberry Pi Zero 2 W:** Use o binário que corresponde ao seu SO: Raspberry Pi OS 32-bit -> `make build-linux-arm`; 64-bit -> `make build-linux-arm64`. Ou execute `make build-pi-zero` para buildar ambos.

## 🚀 Guia de Início Rápido

### 🌐 WebUI Launcher (Recomendado para Desktop)

O WebUI Launcher fornece uma interface baseada em navegador para configuração e chat. Esta é a maneira mais fácil de começar — sem necessidade de conhecimento de linha de comando.

**Opção 1: Duplo clique (Desktop)**

Após baixar do [GitHub Releases](https://github.com/stpinkie/rhizome/releases), dê duplo clique em `rhizome-launcher` (ou `rhizome-launcher.exe` no Windows). Seu navegador abrirá automaticamente em `http://localhost:18800`.

**Opção 2: Linha de comando**

```bash
rhizome-launcher
# Abra http://localhost:18800 no seu navegador
```

> [!TIP]
> **Acesso remoto / Docker / VM:** Adicione a flag `-public` para escutar em todas as interfaces:
> ```bash
> rhizome-launcher -public
> ```

<p align="center">
<img src="../../assets/launcher-webui.jpg" alt="WebUI Launcher" width="600">
</p>

**Primeiros passos:**

Abra o WebUI e então: **1)** Configure um Provider (adicione sua API key de LLM) → **2)** Configure um Channel (ex.: Telegram) → **3)** Inicie o Gateway → **4)** Converse!

Para documentação detalhada, veja a [pasta docs/](https://github.com/stpinkie/rhizome/tree/main/docs) neste repo.

<details>
<summary><b>Docker (alternativa)</b></summary>

```bash
# 1. Clone este repositório
git clone https://github.com/stpinkie/rhizome.git
cd rhizome

# 2. Primeira execução — gera docker/data/config.json automaticamente e sai
#    (só dispara quando ambos config.json e workspace/ estão ausentes)
docker compose -f docker/docker-compose.yml --profile launcher up
# O container imprime "First-run setup complete." e para.

# 3. Configure suas API keys
vim docker/data/config.json

# 4. Inicie
docker compose -f docker/docker-compose.yml --profile launcher up -d
# Abra http://localhost:18800
```

> **Usuários de Docker / VM:** O Gateway escuta em `127.0.0.1` por padrão. Defina `RHIZOME_GATEWAY_HOST=0.0.0.0` ou use a flag `-public` para torná-lo acessível a partir do host.

```bash
# Ver logs
docker compose -f docker/docker-compose.yml logs -f

# Parar
docker compose -f docker/docker-compose.yml --profile launcher down

# Atualizar
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

</details>

<details>
<summary><b>macOS — Aviso de segurança no primeiro lançamento</b></summary>

O macOS pode bloquear o `rhizome-launcher` no primeiro lançamento porque ele é baixado da internet e não é notarizado pela Mac App Store.

**Passo 1:** Dê duplo clique em `rhizome-launcher`. Você verá um aviso de segurança:

<p align="center">
<img src="../../assets/macos-gatekeeper-warning.jpg" alt="Aviso do macOS Gatekeeper" width="400">
</p>

> *"rhizome-launcher" Não Aberto — A Apple não pôde verificar se "rhizome-launcher" está livre de malware que pode prejudicar o seu Mac ou comprometer sua privacidade.*

**Passo 2:** Abra **Configurações do Sistema** → **Privacidade & Segurança** → role para baixo até a seção **Segurança** → clique em **Abrir Mesmo Assim** → confirme clicando em **Abrir Mesmo Assim** na caixa de diálogo.

<p align="center">
<img src="../../assets/macos-gatekeeper-allow.jpg" alt="macOS Privacidade & Segurança — Abrir Mesmo Assim" width="600">
</p>

Após este passo único, o `rhizome-launcher` abrirá normalmente em lançamentos subsequentes.

</details>

<a id="-run-on-old-android-phones"></a>
### 📱 Android

Dê uma segunda vida ao seu celular de uma década! Transforme-o em um Assistente de IA inteligente com o Rhizome.

**Opção 1: Instalação via APK**

Pré-visualização:

<table>
  <tr>
    <td><img src="../../assets/fui_main_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_web_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_log_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_setting_page.jpg" width="200"></td>
  </tr>
</table>

O APK Android ainda não é publicado neste fork; compile a partir do código-fonte ou verifique os [GitHub Releases](https://github.com/stpinkie/rhizome/releases) para uma APK futura.

**Opção 2: Termux**

Para uma lista de verificação completa de configuração via linha de comando, veja o [Guia Android Termux](../guides/android-termux.md).

<details>
<summary><b>Terminal Launcher (para ambientes com recursos limitados)</b></summary>

1. Instale o [Termux](https://github.com/termux/termux-app) (baixe do [GitHub Releases](https://github.com/termux/termux-app/releases), ou procure em F-Droid / Google Play)
2. Execute os seguintes comandos:

```bash
# Baixe o release mais recente
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz
pkg install proot
termux-chroot ./rhizome onboard   # chroot fornece um layout padrão de sistema de arquivos Linux
```

Depois siga a seção Terminal Launcher abaixo para completar a configuração.

<img src="../../assets/termux.jpg" alt="Rhizome on Termux" width="512">

Para ambientes mínimos onde apenas o binário principal `rhizome` está disponível (sem Launcher UI), você pode configurar tudo via linha de comando e um arquivo de configuração JSON.

**1. Inicializar**

```bash
rhizome onboard
```

Isso cria `~/.rhizome/config.json` e o diretório de workspace.

**2. Configurar** (`~/.rhizome/config.json`)

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
      // api_key agora é carregado de .security.yml
    }
  ]
}
```

> Veja `config/config.example.json` no repo para um modelo de configuração completo com todas as opções disponíveis.
>
> Observe: config.example.json está no formato version 0, com códigos sensíveis, e será migrado automaticamente para version 1+; então config.json armazenará apenas dados não sensíveis, e os códigos sensíveis ficarão em .security.yml. Se precisar modificar os códigos manualmente, veja `docs/security/security_configuration.md`.

**3. Conversar**

```bash
# Uma pergunta única
rhizome agent -m "What is 2+2?"

# Modo interativo
rhizome agent

# Inicie o gateway para integração com app de chat
rhizome gateway
```

</details>

## 🔌 Providers (LLM)

O Rhizome suporta mais de 30 providers de LLM através da configuração `model_list`. Use o formato `protocolo/modelo`:

| Provider | Protocolo | API Key | Notas |
|----------|-----------|---------|-------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | Obrigatória | GPT-5.4, GPT-4o, o3, etc. |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | Obrigatória | Claude Opus 4.6, Sonnet 4.6, etc. |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | Obrigatória | Gemini 3 Flash, 2.5 Pro, etc. |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | Obrigatória | 200+ modelos, API unificada |
| [Zhipu (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | Obrigatória | GLM-4.7, GLM-5, etc. |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | Obrigatória | DeepSeek-V3, DeepSeek-R1 |
| [Volcengine](https://console.volcengine.com) | `volcengine/` | Obrigatória | Modelos Doubao, Ark |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | Obrigatória | Qwen3, Qwen-Max, etc. |
| [Groq](https://console.groq.com/keys) | `groq/` | Obrigatória | Inferência rápida (Llama, Mixtral) |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | Obrigatória | Modelos Kimi |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | Obrigatória | Modelos MiniMax |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | Obrigatória | Mistral Large, Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | Obrigatória | Modelos hospedados pela NVIDIA |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | Obrigatória | Inferência rápida |
| [Novita AI](https://novita.ai/) | `novita/` | Obrigatória | Vários modelos abertos |
| [Xiaomi MiMo](https://platform.xiaomimimo.com/) | `mimo/` | Obrigatória | Modelos MiMo |
| [Ollama](https://ollama.com/) | `ollama/` | Não necessária | Modelos locais, self-hosted |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | Não necessária | Implantação local, compatível com OpenAI |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | Varia | Proxy para 100+ providers |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | Obrigatória | Implantação Azure Enterprise |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | Login por código de dispositivo |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |

<details>
<summary><b>Implantação local (Ollama, vLLM, etc.)</b></summary>

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

Para detalhes completos de configuração de providers, veja [Providers & Models](../guides/providers.pt-br.md).

</details>

## 💬 Channels (Apps de Chat)

Converse com seu Rhizome por meio de mais de 17 plataformas de mensagens:

| Channel | Configuração | Protocolo | Docs |
|---------|--------------|-----------|------|
| **Telegram** | Fácil (bot token) | Long polling | [Guia](../channels/telegram/README.pt-br.md) |
| **Discord** | Fácil (bot token + intents) | WebSocket | [Guia](../channels/discord/README.pt-br.md) |
| **WhatsApp** | Fácil (QR scan ou bridge URL) | Nativo / Bridge | [Guia](../guides/chat-apps.pt-br.md#whatsapp) |
| **Weixin** | Fácil (scan QR nativo) | iLink API | [Guia](../guides/chat-apps.pt-br.md#weixin) |
| **QQ** | Fácil (AppID + AppSecret) | WebSocket | [Guia](../channels/qq/README.pt-br.md) |
| **Slack** | Fácil (bot + app token) | Socket Mode | [Guia](../channels/slack/README.pt-br.md) |
| **Matrix** | Médio (homeserver + token) | Sync API | [Guia](../channels/matrix/README.pt-br.md) |
| **DingTalk** | Médio (credenciais do cliente) | Stream | [Guia](../channels/dingtalk/README.pt-br.md) |
| **Feishu / Lark** | Médio (App ID + Secret) | WebSocket/SDK | [Guia](../channels/feishu/README.pt-br.md) |
| **LINE** | Médio (credenciais + webhook) | Webhook | [Guia](../channels/line/README.pt-br.md) |
| **WeCom** | Fácil (login QR ou manual) | WebSocket | [Guia](../channels/wecom/README.pt-br.md) |
| **IRC** | Médio (servidor + nick) | Protocolo IRC | [Guia](../guides/chat-apps.pt-br.md#irc) |
| **OneBot** | Médio (WebSocket URL) | OneBot v11 | [Guia](../channels/onebot/README.pt-br.md) |
| **MaixCam** | Fácil (habilitar) | TCP socket | [Guia](../channels/maixcam/README.pt-br.md) |
| **Pico** | Fácil (habilitar) | Protocolo nativo | Integrado |
| **Pico Client** | Fácil (WebSocket URL) | WebSocket | Integrado |

> Todos os channels baseados em webhook compartilham um único servidor HTTP do Gateway (`gateway.host`:`gateway.port`, padrão `127.0.0.1:18790`). O Feishu usa modo WebSocket/SDK e não utiliza o servidor HTTP compartilhado.

> A verbosidade dos logs é controlada por `gateway.log_level` (padrão: `warn`). Valores suportados: `debug`, `info`, `warn`, `error`, `fatal`. Também pode ser definido via `RHIZOME_LOG_LEVEL`. Veja [Configuração](../guides/configuration.pt-br.md#nível-de-log-do-gateway) para detalhes.

Para instruções detalhadas de configuração de channels, veja [Configuração de Apps de Chat](../guides/chat-apps.pt-br.md).

## 🔧 Ferramentas

### 🔍 Busca na Web

O Rhizome pode pesquisar na web para fornecer informações atualizadas. Configure em `tools.web`:

| Motor de Busca | API Key | Nível Gratuito | Link |
|----------------|---------|----------------|------|
| DuckDuckGo | Não necessária | Ilimitado | Fallback integrado |
| [Baidu Search](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | Obrigatória | 1500 consultas/mês (alocação diária) | IA, otimizado para chinês |
| [Tavily](https://tavily.com) | Obrigatória | 1000 consultas/mês | Otimizado para AI Agents |
| [Brave Search](https://brave.com/search/api) | Obrigatória | 2000 consultas/mês | Rápido e privado |
| [Perplexity](https://www.perplexity.ai) | Obrigatória | Pago | Busca com IA |
| [SearXNG](https://github.com/searxng/searxng) | Não necessária | Self-hosted | Metabuscador gratuito |
| [GLM Search](https://open.bigmodel.cn/) | Obrigatória | Varia | Busca web Zhipu |

### ⚙️ Outras Ferramentas

O Rhizome inclui ferramentas integradas para operações de arquivo, execução de código, agendamento e mais. Veja [Configuração de Ferramentas](../reference/tools_configuration.pt-br.md) para detalhes.

## 🎯 Skills

Skills são capacidades modulares que estendem seu Agent. Elas são carregadas a partir de arquivos `SKILL.md` no seu workspace.

**Instalar skills do ClawHub:**

```bash
rhizome skills search "web scraping"
rhizome skills install <skill-name>
```

**Configurar token do ClawHub** (opcional, para limites de taxa mais altos):

Adicione ao seu `config.json`:
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

Para mais detalhes, veja [Configuração de Ferramentas - Skills](../reference/tools_configuration.pt-br.md#skills-tool).

## 🔗 MCP (Model Context Protocol)

O Rhizome suporta nativamente o [MCP](https://modelcontextprotocol.io/) — conecte qualquer servidor MCP para estender as capacidades do seu Agent com ferramentas externas e fontes de dados.

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

Para configuração completa de MCP (transportes stdio, SSE, HTTP, Tool Discovery), veja [Configuração de Ferramentas - MCP](../reference/tools_configuration.pt-br.md#mcp-tool).

## <img src="../../assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> Junte-se à Rede Social de Agents

Conecte o Rhizome à Rede Social de Agents simplesmente enviando uma única mensagem via CLI ou qualquer App de Chat integrado.

**Leia `https://clawdchat.ai/skill.md` e siga as instruções para entrar no [ClawdChat.ai](https://clawdchat.ai)**

## 🖥️ Referência CLI

| Comando                   | Descrição                              |
| ------------------------- | -------------------------------------- |
| `rhizome onboard`        | Inicializar config e workspace         |
| `rhizome auth weixin` | Conectar conta WeChat via QR |
| `rhizome agent -m "..."` | Conversar com o agent                  |
| `rhizome agent`          | Modo de chat interativo                |
| `rhizome gateway`        | Iniciar o gateway                      |
| `rhizome status`         | Exibir status                          |
| `rhizome version`        | Exibir informações de versão           |
| `rhizome model`          | Ver ou trocar o modelo padrão          |
| `rhizome cron list`      | Listar todos os jobs agendados         |
| `rhizome cron add ...`   | Adicionar um job agendado              |
| `rhizome cron disable`   | Desabilitar um job agendado            |
| `rhizome cron remove`    | Remover um job agendado                |
| `rhizome skills list`    | Listar skills instaladas               |
| `rhizome skills install` | Instalar uma skill                     |
| `rhizome migrate`        | Migrar dados de versões anteriores     |
| `rhizome auth login`     | Autenticar com providers               |

### ⏰ Tarefas Agendadas / Lembretes

O Rhizome suporta lembretes agendados e tarefas recorrentes através da ferramenta `cron`:

* **Lembretes únicos**: "Lembre-me em 10 minutos" -> dispara uma vez após 10min
* **Tarefas recorrentes**: "Lembre-me a cada 2 horas" -> dispara a cada 2 horas
* **Expressões cron**: "Lembre-me às 9h diariamente" -> usa expressão cron

## 📚 Documentação

Para guias detalhados além deste README:

| Tópico | Descrição |
|--------|-----------|
| [Docker & Início Rápido](../guides/docker.pt-br.md) | Configuração do Docker Compose, modos Launcher/Agent |
| [Apps de Chat](../guides/chat-apps.pt-br.md) | Guias de configuração para todos os 17+ channels |
| [Configuração](../guides/configuration.pt-br.md) | Variáveis de ambiente, layout do workspace, sandbox de segurança |
| [Providers & Models](../guides/providers.pt-br.md) | 30+ providers de LLM, roteamento de modelos, configuração de model_list |
| [Spawn & Tarefas Assíncronas](../guides/spawn-tasks.pt-br.md) | Tarefas rápidas, tarefas longas com spawn, orquestração assíncrona de sub-agents |
| [Hooks](../architecture/hooks/README.md) | Sistema de hooks orientado a eventos: observadores, interceptores, hooks de aprovação |
| [Steering](../architecture/steering.md) | Injetar mensagens em um loop de agente em execução |
| [SubTurn](../architecture/subturn.md) | Coordenação de subagentes, controle de concorrência, ciclo de vida |
| [Solução de Problemas](../operations/troubleshooting.pt-br.md) | Problemas comuns e soluções |
| [Configuração de Ferramentas](../reference/tools_configuration.pt-br.md) | Habilitar/desabilitar por ferramenta, políticas de exec, MCP, Skills |
| [Compatibilidade de Hardware](../guides/hardware-compatibility.pt-br.md) | Placas testadas, requisitos mínimos |

## 🤝 Contribuir & Roadmap

PRs são bem-vindos! O código-fonte é intencionalmente pequeno e legível.

Veja nosso [Roadmap da Comunidade](https://github.com/stpinkie/rhizome/issues/988) e [CONTRIBUTING.md](../../CONTRIBUTING.md) para diretrizes.

Grupo de desenvolvedores em formação, entre após seu primeiro PR mesclado!

Grupos de Usuários:

Discord: <https://discord.gg/V4sAZ9XWpN>

WeChat:
<img src="../../assets/wechat.png" alt="WeChat group QR code" width="512">