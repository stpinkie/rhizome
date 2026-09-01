<div align="center">
<img src="../../assets/logo.webp" alt="Rhizome" width="512">

<h1>Rhizome : Assistant AI Ultra-Efficace en Go</h1>

<h3>Matériel $10 · 10 Mo de RAM · Boot en ms · Let's Go, Rhizome!</h3>
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

[中文](README.zh.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | **Français** | [Italiano](README.it.md) | [Bahasa Indonesia](README.id.md) | [Malay](README.ms.md) | [English](../../README.md)

</div>

---

> **Rhizome** est un hard fork maintenu par la communauté de [PicoClaw](https://github.com/sipeed/picoclaw). Il est entièrement écrit en **Go** et poursuit l'objectif d'être un assistant AI personnel ultra-léger.

**Rhizome** est un assistant AI personnel inspiré par [NanoBot](https://github.com/HKUDS/nanobot). Il ajoute une mesh P2P native Go, la synchronisation de workspace et une passerelle agent sur l'idée originale de PicoClaw.

**Un seul binaire Go, sans dépendance d'exécution** — s'exécute nativement sous Linux, Windows, macOS, FreeBSD/NetBSD et Android. Voir la [Liste de Compatibilité Matérielle](../guides/hardware-compatibility.fr.md) pour les cartes vérifiées et les exigences actuelles à deux niveaux.

<p align="center">
<img src="../../assets/rhizome_mem.gif" width="360" height="240">
</p>

> [!CAUTION]
> **Avis de Sécurité**
>
> * **PAS DE CRYPTO :** Rhizome **n'a émis** aucun token officiel ou cryptomonnaie. Toute affirmation sur `pump.fun` ou d'autres plateformes d'échange est une **arnaque**.
> * **SOURCE CANONIQUE (CANONICAL SOURCE) :** La source et le lieu de publication officiels sont **<https://github.com/stpinkie/rhizome>** ; les publications sont sur GitHub Releases. Méfiez-vous des domaines tiers prétendant être officiels.
> * **Attention :** De nombreux domaines `.ai/.org/.com/.net/...` ont été enregistrés par des tiers. Ne leur faites pas confiance.
> * **Remarque :** Rhizome est en phase de développement rapide initial. Il peut rester des problèmes de sécurité non résolus. Ne le déployez pas en production avant la v1.0.
> * **Remarque :** Le binaire `rhizome` complet est d'environ 98 Mo et le daemon utilise environ 60 Mo de mémoire privée. Nous prévoyons un build `nonetwork` pour réduire encore l'empreinte sur les cartes ultra-petites. L'optimisation des ressources est prévue après la stabilisation des fonctionnalités.

## 📢 Actualités

2026-05-28 🚀 **v0.2.9 publié !** Gestion des serveurs MCP dans la Web UI, recherche Web Sogou configurable, animation de retour d'outil sur les channels, valeurs par défaut `pretty_print` et `disable_escape_html`, et diverses corrections de bugs pour les providers et les channels.

2026-05-14 🚀 **v0.2.8 publié !** Commandes MCP CLI (`show`, `add`, `list`, `remove`, `test`, `edit`), objet vide au lieu de null pour les paramètres d'outils MCP, et corrections de build.

2026-05-07 🚀 **v0.2.7 publié !** Recherche Web Sogou configurable, animation de retour d'outil sur les channels, corrections du linter.

2026-04-23 🚀 **v0.2.6 publié !** Hooks avec action de réponse et documentation complète, support de l'isolement, correction de la bannière d'aide.

2026-04-11 🚀 **v0.2.5 publié !** Zoneinfo depuis les variables d'environnement TZ/ZONEINFO, alignement du rendu Matrix CommonMark, `read_file` par ligne.

2026-03-31 📱 **Support Android !** Rhizome fonctionne maintenant sur Android ! L'APK Android n'est pas distribué depuis ce fork ; compilez depuis les sources ou consultez les [GitHub Releases](https://github.com/stpinkie/rhizome/releases) pour un APK futur.

2026-03-25 🚀 **v0.2.4 publié !** Refonte complète de l'architecture Agent (SubTurn, Hook, Steering, EventBus), intégration approfondie WeChat/WeCom, renforcement de la sécurité (.security.yml, filtrage des données sensibles), nouveaux providers (AWS Bedrock, Azure, Xiaomi MiMo) et 35 corrections de bugs. Rhizome a atteint **26K Stars** !

2026-03-17 🚀 **v0.2.3 publié !** UI dans la barre d'état système (Windows & Linux), requête d'état des sous-agents (`spawn_status`), rechargement à chaud expérimental du Gateway, sécurité Cron, et 2 corrections de sécurité. Rhizome a atteint **25K Stars** !

2026-03-09 🎉 **v0.2.1 — La plus grande mise à jour à ce jour !** Support du protocole MCP, 4 nouveaux channels (Matrix/IRC/WeCom/Discord Proxy), 3 nouveaux providers (Kimi/Minimax/Avian), pipeline de vision, stockage de mémoire JSONL, routage de modèles.

2026-02-28 📦 **v0.2.0** publié avec support de Docker Compose et du Web UI Launcher.

<details>
<summary>Actualités antérieures...</summary>

2026-02-26 🎉 Rhizome atteint **20K Stars** en seulement 17 jours ! L'orchestration automatique des channels et l'interface des capacités sont disponibles.

2026-02-16 🎉 Rhizome dépasse 12K Stars en une semaine ! Le rôle de mainteneur communautaire et la [Roadmap](../../ROADMAP.md) officiels sont publiés.

2026-02-13 🎉 Rhizome dépasse 5000 Stars en 4 jours ! La roadmap du projet et le groupe de développeurs sont en construction.

2026-02-09 🎉 **Rhizome lancé !** Construit en 1 jour pour explorer les agents AI ultra-légers. Let's Go, Rhizome!

</details>

## ✨ Fonctionnalités

🪶 **Binaire unique, sans dépendance d'exécution** : Un exécutable Go lié statiquement, s'exécutant sous Linux, Windows, macOS, FreeBSD/NetBSD et Android.*

💰 **Coût minimal** : Assez efficace pour fonctionner sur une large gamme de cartes ARM et RISC-V à bas coût ; voir la [Liste de Compatibilité Matérielle](../guides/hardware-compatibility.fr.md).

⚡️ **Démarrage éclair** : Démarre en moins d'une seconde sur les cartes à bas coût vérifiées.

🌍 **Véritablement portable** : Un seul binaire à travers les architectures RISC-V, ARM, MIPS et x86. Un binaire, fonctionne partout !

🤖 **Amorcé par l'IA** : Implémentation native pure en Go — 95 % du code principal généré par un agent et affiné via une revue humaine.

🔌 **Support MCP** : Intégration native du [Model Context Protocol](https://modelcontextprotocol.io/) — connectez n'importe quel serveur MCP pour étendre les capacités de l'agent.

👁️ **Pipeline de vision** : Envoyez directement des images et des fichiers à l'agent — encodage base64 automatique pour les LLM multimodaux.

🧠 **Routage intelligent** : Routage de modèles basé sur des règles — les requêtes simples sont envoyées vers des modèles légers, réduisant les coûts API.

_*La mesure d'empreinte a été effectuée sous Windows avec `CGO_ENABLED=0`, les tags `goolm,stdjson` et `-ldflags "-s -w"` ; le binaire strip est d'environ 98 Mo. Un build `nonetwork` est prévu pour réduire encore sur les cartes ultra-petites._

<div align="center">

### Empreinte du Build Actuel

| Mode | Cas d'usage | RAM totale | RAM libre | Stockage |
|------|-------------|------------|-----------|----------|
| **Base** | `rhizome agent`, `rhizome onboard` one-shot | 256 Mo | 128 Mo | 128 Mo |
| **Complet** | `rhizome daemon` avec P2P, syncer et gateway | 512 Mo | 256 Mo | 128 Mo |

</div>

> **[Liste de Compatibilité Matérielle](../guides/hardware-compatibility.fr.md)** — Voir toutes les cartes testées, du Raspberry Pi aux téléphones Android. Votre carte n'est pas listée ? Envoyez une PR !

<p align="center">
<img src="../../assets/hardware-banner.jpg" alt="Rhizome Hardware Compatibility" width="100%">
</p>

## 🦾 Démonstration

### 🛠️ Flux de travail standard de l'assistant

<table align="center">
<tr align="center">
<th><p align="center">Mode Ingénieur Full-Stack</p></th>
<th><p align="center">Journalisation et Planification</p></th>
<th><p align="center">Recherche Web et Apprentissage</p></th>
</tr>
<tr>
<td align="center"><p align="center"><img src="../../assets/rhizome_code.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_memory.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/rhizome_search.gif" width="240" height="180"></p></td>
</tr>
<tr>
<td align="center">Développer · Déployer · Passer à l'échelle</td>
<td align="center">Planifier · Automatiser · Mémoriser</td>
<td align="center">Découvrir · Perspectives · Tendances</td>
</tr>
</table>

### 🐜 Déploiement innovant à faible empreinte

Rhizome peut être déployé sur un large éventail de périphériques Linux et embarqués !

- $15 [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) (ou [Zero 2 W](https://www.raspberrypi.com/products/raspberry-pi-zero-2-w/)), pour un assistant domestique minimal
- $50~70 [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/), pour une utilisation embarquée basée sur RISC-V
- $100 [NanoKVM-Pro](https://www.aliexpress.com/item/1005010048471263.html), pour des opérations serveur automatisées
- $100 [MaixCAM2](https://www.kickstarter.com/projects/zepan/maixcam2-build-your-next-gen-4k-ai-camera), pour la surveillance intelligente

> Voir la [Liste de Compatibilité Matérielle](../guides/hardware-compatibility.fr.md) pour la liste complète des cartes vérifiées et les exigences actuelles à deux niveaux.

<https://private-user-images.githubusercontent.com/83055338/547056448-e7b031ff-d6f5-4468-bcca-5726b6fecb5c.mp4>

🌟 Plus de cas de déploiement à venir !

## 📦 Installation

### Télécharger depuis GitHub Releases (Recommandé)

Visitez la page [GitHub Releases](https://github.com/stpinkie/rhizome/releases) et téléchargez le binaire pour votre plateforme.

### Télécharger le binaire précompilé

Sinon, téléchargez le binaire pour votre plateforme depuis la page [GitHub Releases](https://github.com/stpinkie/rhizome/releases).

### Compiler depuis les sources (pour le développement)

Prérequis :

- Go 1.26+
- Node.js 22+ et pnpm 10.33.0+ pour les builds Web UI / launcher

```bash
git clone https://github.com/stpinkie/rhizome.git

cd rhizome
make deps

# Installer les dépendances frontend
(cd web/frontend && pnpm install --frozen-lockfile)

# Build du binaire core pour la plateforme actuelle
make build

# Build du Web UI Launcher (nécessaire pour le mode WebUI)
make build-launcher

# Build des binaires core pour toutes les plateformes gérées par le Makefile
make build-all

# Build pour Raspberry Pi Zero 2 W
# 32 bits : make build-linux-arm
# 64 bits : make build-linux-arm64
make build-pi-zero

# Build et installation
make install
```

**Raspberry Pi Zero 2 W :** Utilisez le binaire correspondant à votre OS : Raspberry Pi OS 32 bits -> `make build-linux-arm` ; 64 bits -> `make build-linux-arm64`. Ou exécutez `make build-pi-zero` pour builder les deux.

## 🚀 Guide de démarrage rapide

### 🌐 WebUI Launcher (Recommandé pour le bureau)

WebUI Launcher fournit une interface basée sur navigateur pour la configuration et le chat. C'est le moyen le plus simple de commencer — aucune connaissance de la ligne de commande requise.

**Option 1 : Double-clic (Bureau)**

Après avoir téléchargé depuis [GitHub Releases](https://github.com/stpinkie/rhizome/releases), double-cliquez sur `rhizome-launcher` (ou `rhizome-launcher.exe` sous Windows). Votre navigateur s'ouvrira automatiquement à `http://localhost:18800`.

**Option 2 : Ligne de commande**

```bash
rhizome-launcher
# Ouvrez http://localhost:18800 dans votre navigateur
```

> [!TIP]
> **Accès distant / Docker / VM :** Ajoutez le flag `-public` pour écouter sur toutes les interfaces :
> ```bash
> rhizome-launcher -public
> ```

<p align="center">
<img src="../../assets/launcher-webui.jpg" alt="WebUI Launcher" width="600">
</p>

**Pour commencer :**

Ouvrez le WebUI, puis : **1)** Configurez un provider (ajoutez votre clé API LLM) → **2)** Configurez un channel (ex. Telegram) → **3)** Démarrez la Gateway → **4)** Discutez !

Pour la documentation détaillée, voir le [dossier docs/](https://github.com/stpinkie/rhizome/tree/main/docs) de ce repo.

<details>
<summary><b>Docker (alternative)</b></summary>

```bash
# 1. Cloner ce repo
git clone https://github.com/stpinkie/rhizome.git
cd rhizome

# 2. Premier lancement — génère automatiquement docker/data/config.json puis s'arrête
#    (ne se déclenche que lorsque config.json et workspace/ sont tous deux absents)
docker compose -f docker/docker-compose.yml --profile launcher up
# Le container affiche "First-run setup complete." puis s'arrête.

# 3. Configurez vos clés API
vim docker/data/config.json

# 4. Démarrez
docker compose -f docker/docker-compose.yml --profile launcher up -d
# Ouvrez http://localhost:18800
```

> **Utilisateurs Docker / VM :** La Gateway écoute sur `127.0.0.1` par défaut. Définissez `RHIZOME_GATEWAY_HOST=0.0.0.0` ou utilisez le flag `-public` pour la rendre accessible depuis l'hôte.

```bash
# Voir les logs
docker compose -f docker/docker-compose.yml logs -f

# Arrêter
docker compose -f docker/docker-compose.yml --profile launcher down

# Mettre à jour
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

</details>

<details>
<summary><b>macOS — Avertissement de sécurité au premier lancement</b></summary>

macOS peut bloquer `rhizome-launcher` au premier lancement car il est téléchargé depuis Internet et n'est pas notarisé via le Mac App Store.

**Étape 1 :** Double-cliquez sur `rhizome-launcher`. Un avertissement de sécurité s'affiche :

<p align="center">
<img src="../../assets/macos-gatekeeper-warning.jpg" alt="Avertissement macOS Gatekeeper" width="400">
</p>

> *« rhizome-launcher » non ouvert — Apple n'a pas pu vérifier que « rhizome-launcher » est exempt de logiciels malveillants susceptibles d'endommager votre Mac ou de compromettre votre confidentialité.*

**Étape 2 :** Ouvrez **Paramètres Système** → **Confidentialité et Sécurité** → faites défiler jusqu'à la section **Sécurité** → cliquez sur **Ouvrir quand même** → confirmez en cliquant **Ouvrir quand même** dans la boîte de dialogue.

<p align="center">
<img src="../../assets/macos-gatekeeper-allow.jpg" alt="macOS Confidentialité et Sécurité — Ouvrir quand même" width="600">
</p>

Après cette opération unique, `rhizome-launcher` s'ouvrira normalement lors des lancements suivants.

</details>

<a id="-run-on-old-android-phones"></a>
### 📱 Android

Donnez une seconde vie à votre vieux téléphone ! Transformez-le en assistant AI intelligent avec Rhizome.

**Option 1 : Installer l'APK**

Aperçu :

<table>
  <tr>
    <td><img src="../../assets/fui_main_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_web_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_log_page.jpg" width="200"></td>
    <td><img src="../../assets/fui_setting_page.jpg" width="200"></td>
  </tr>
</table>

L'APK Android n'est actuellement pas publié depuis ce fork ; compilez depuis les sources ou consultez les [GitHub Releases](https://github.com/stpinkie/rhizome/releases) pour un APK futur.

**Option 2 : Termux**

Pour une liste de vérification complète de la configuration en ligne de commande, voir le [Guide Android Termux](../guides/android-termux.md).

<details>
<summary><b>Terminal Launcher (pour environnements à ressources limitées)</b></summary>

1. Installez [Termux](https://github.com/termux/termux-app) (téléchargez depuis [GitHub Releases](https://github.com/termux/termux-app/releases), ou recherchez dans F-Droid / Google Play)
2. Exécutez les commandes suivantes :

```bash
# Télécharger la dernière release
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz
pkg install proot
termux-chroot ./rhizome onboard   # chroot fournit une disposition standard du système de fichiers Linux
```

Puis suivez la section Terminal Launcher ci-dessous pour terminer la configuration.

<img src="../../assets/termux.jpg" alt="Rhizome on Termux" width="512">

Pour les environnements minimaux où seul le binaire core `rhizome` est disponible (sans Launcher UI), vous pouvez tout configurer via la ligne de commande et un fichier de configuration JSON.

**1. Initialiser**

```bash
rhizome onboard
```

Cela crée `~/.rhizome/config.json` et le répertoire workspace.

**2. Configurer** (`~/.rhizome/config.json`)

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
      // api_key est maintenant chargée depuis .security.yml
    }
  ]
}
```

> Pour un modèle de configuration complet avec toutes les options disponibles, voir `config/config.example.json` dans le repo.
>
> Remarque : config.example.json est au format version 0, contient des codes sensibles, et sera automatiquement migré vers la version 1+ ; ensuite config.json ne stockera que des données non sensibles, tandis que les codes sensibles seront dans .security.yml. Si vous devez modifier manuellement les codes, voir `docs/security/security_configuration.md`.

**3. Discuter**

```bash
# Une question unique
rhizome agent -m "What is 2+2?"

# Mode interactif
rhizome agent

# Démarrer la gateway pour l'intégration d'applications de chat
rhizome gateway
```

</details>

## 🔌 Providers (LLM)

Rhizome supporte plus de 30 providers LLM via la configuration `model_list`. Utilisez le format `protocole/modèle` :

| Provider | Protocole | Clé API | Notes |
|----------|-----------|---------|-------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | Requise | GPT-5.4, GPT-4o, o3, etc. |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | Requise | Claude Opus 4.6, Sonnet 4.6, etc. |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | Requise | Gemini 3 Flash, 2.5 Pro, etc. |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | Requise | 200+ modèles, API unifiée |
| [Zhipu (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | Requise | GLM-4.7, GLM-5, etc. |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | Requise | DeepSeek-V3, DeepSeek-R1 |
| [Volcengine](https://console.volcengine.com) | `volcengine/` | Requise | Modèles Doubao, Ark |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | Requise | Qwen3, Qwen-Max, etc. |
| [Groq](https://console.groq.com/keys) | `groq/` | Requise | Inférence rapide (Llama, Mixtral) |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | Requise | Modèles Kimi |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | Requise | Modèles MiniMax |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | Requise | Mistral Large, Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | Requise | Modèles hébergés NVIDIA |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | Requise | Inférence rapide |
| [Novita AI](https://novita.ai/) | `novita/` | Requise | Divers modèles open |
| [Xiaomi MiMo](https://platform.xiaomimimo.com/) | `mimo/` | Requise | Modèles MiMo |
| [Ollama](https://ollama.com/) | `ollama/` | Non requise | Modèles locaux, auto-hébergé |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | Non requise | Déploiement local, compatible OpenAI |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | Variable | Proxy pour 100+ providers |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | Requise | Déploiement Azure entreprise |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | Connexion par code appareil |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |

<details>
<summary><b>Déploiement local (Ollama, vLLM, etc.)</b></summary>

**Ollama :**
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

**vLLM :**
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

Pour les détails complets de configuration des providers, voir [Providers & Modèles](../guides/providers.fr.md).

</details>

## 💬 Channels (Applications de chat)

Parlez à votre Rhizome via plus de 17 plateformes de messagerie :

| Channel | Configuration | Protocole | Docs |
|---------|---------------|-----------|------|
| **Telegram** | Facile (token bot) | Long polling | [Guide](../channels/telegram/README.fr.md) |
| **Discord** | Facile (token bot + intents) | WebSocket | [Guide](../channels/discord/README.fr.md) |
| **WhatsApp** | Facile (scan QR ou URL bridge) | Natif / Bridge | [Guide](../guides/chat-apps.fr.md#whatsapp) |
| **Weixin** | Facile (scan QR natif) | iLink API | [Guide](../guides/chat-apps.fr.md#weixin) |
| **QQ** | Facile (AppID + AppSecret) | WebSocket | [Guide](../channels/qq/README.fr.md) |
| **Slack** | Facile (token bot + app) | Socket Mode | [Guide](../channels/slack/README.fr.md) |
| **Matrix** | Moyen (homeserver + token) | Sync API | [Guide](../channels/matrix/README.fr.md) |
| **DingTalk** | Moyen (identifiants client) | Stream | [Guide](../channels/dingtalk/README.fr.md) |
| **Feishu / Lark** | Moyen (App ID + Secret) | WebSocket/SDK | [Guide](../channels/feishu/README.fr.md) |
| **LINE** | Moyen (identifiants + webhook) | Webhook | [Guide](../channels/line/README.fr.md) |
| **WeCom** | Facile (QR login ou manuel) | WebSocket | [Guide](../channels/wecom/README.fr.md) |
| **IRC** | Moyen (serveur + pseudo) | Protocole IRC | [Guide](../guides/chat-apps.fr.md#irc) |
| **OneBot** | Moyen (URL WebSocket) | OneBot v11 | [Guide](../channels/onebot/README.fr.md) |
| **MaixCam** | Facile (activer) | Socket TCP | [Guide](../channels/maixcam/README.fr.md) |
| **Pico** | Facile (activer) | Protocole natif | Intégré |
| **Pico Client** | Facile (URL WebSocket) | WebSocket | Intégré |

> Tous les channels basés sur webhook partagent un seul serveur HTTP Gateway (`gateway.host`:`gateway.port`, par défaut `127.0.0.1:18790`). Feishu utilise le mode WebSocket/SDK et n'utilise pas le serveur HTTP partagé.

> La verbosité des logs est contrôlée par `gateway.log_level` (par défaut : `warn`). Valeurs supportées : `debug`, `info`, `warn`, `error`, `fatal`. Peut aussi être défini via `RHIZOME_LOG_LEVEL`. Voir [Configuration](../guides/configuration.fr.md#niveau-de-log-du-gateway) pour plus de détails.

Pour les instructions détaillées de configuration des channels, voir [Configuration des applications de chat](../guides/chat-apps.fr.md).

## 🔧 Outils

### 🔍 Recherche Web

Rhizome peut effectuer des recherches sur le web pour fournir des informations à jour. Configurez dans `tools.web` :

| Moteur de recherche | Clé API | Niveau gratuit | Lien |
|--------------------|---------|----------------|------|
| DuckDuckGo | Non requise | Illimité | Fallback intégré |
| [Baidu Search](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | Requise | 1500 requêtes/mois (allocation journalière) | IA, optimisé pour le chinois |
| [Tavily](https://tavily.com) | Requise | 1000 requêtes/mois | Optimisé pour les Agents IA |
| [Brave Search](https://brave.com/search/api) | Requise | 2000 requêtes/mois | Rapide et privé |
| [Perplexity](https://www.perplexity.ai) | Requise | Payant | Recherche propulsée par IA |
| [SearXNG](https://github.com/searxng/searxng) | Non requise | Auto-hébergé | Métamoteur de recherche gratuit |
| [GLM Search](https://open.bigmodel.cn/) | Requise | Variable | Recherche web Zhipu |

### ⚙️ Autres outils

Rhizome inclut des outils intégrés pour les opérations sur fichiers, l'exécution de code, la planification et plus encore. Voir [Configuration des outils](../reference/tools_configuration.fr.md) pour les détails.

## 🎯 Skills

Les Skills sont des capacités modulaires qui étendent votre Agent. Elles sont chargées depuis les fichiers `SKILL.md` dans votre workspace.

**Installer des Skills depuis ClawHub :**

```bash
rhizome skills search "web scraping"
rhizome skills install <skill-name>
```

**Configurer le token ClawHub** (optionnel, pour des limites de débit plus élevées) :

Ajoutez à votre `config.json` :
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

Pour plus de détails, voir [Configuration des outils - Skills](../reference/tools_configuration.fr.md#skills-tool).

## 🔗 MCP (Model Context Protocol)

Rhizome supporte nativement [MCP](https://modelcontextprotocol.io/) — connectez n'importe quel serveur MCP pour étendre les capacités de votre Agent avec des outils et sources de données externes.

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

Pour la configuration MCP complète (transports stdio, SSE, HTTP, Tool Discovery), voir [Configuration des outils - MCP](../reference/tools_configuration.fr.md#mcp-tool).

## <img src="../../assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> Rejoignez le réseau social des Agents

Connectez Rhizome au réseau social des Agents simplement en envoyant un seul message via le CLI ou n'importe quelle application de chat intégrée.

**Lisez `https://clawdchat.ai/skill.md` et suivez les instructions pour rejoindre [ClawdChat.ai](https://clawdchat.ai)**

## 🖥️ Référence CLI

| Commande                  | Description                              |
| ------------------------- | ---------------------------------------- |
| `rhizome onboard`        | Initialiser la config & le workspace     |
| `rhizome auth weixin` | Connecter un compte WeChat via QR |
| `rhizome agent -m "..."` | Chatter avec l'agent                     |
| `rhizome agent`          | Mode chat interactif                     |
| `rhizome gateway`        | Démarrer le gateway                      |
| `rhizome status`         | Afficher le statut                       |
| `rhizome version`        | Afficher les informations de version     |
| `rhizome model`          | Voir ou changer le modèle par défaut     |
| `rhizome cron list`      | Lister toutes les tâches planifiées      |
| `rhizome cron add ...`   | Ajouter une tâche planifiée              |
| `rhizome cron disable`   | Désactiver une tâche planifiée           |
| `rhizome cron remove`    | Supprimer une tâche planifiée            |
| `rhizome skills list`    | Lister les Skills installées             |
| `rhizome skills install` | Installer une Skill                      |
| `rhizome migrate`        | Migrer les données depuis d'anciennes versions |
| `rhizome auth login`     | S'authentifier auprès des providers      |

### ⏰ Tâches planifiées / Rappels

Rhizome supporte les rappels planifiés et les tâches récurrentes via l'outil `cron` :

* **Rappels ponctuels** : "Rappelle-moi dans 10 minutes" -> se déclenche une fois après 10 min
* **Tâches récurrentes** : "Rappelle-moi toutes les 2 heures" -> se déclenche toutes les 2 heures
* **Expressions cron** : "Rappelle-moi à 9h chaque jour" -> utilise une expression cron

## 📚 Documentation

Pour des guides détaillés au-delà de ce README :

| Sujet | Description |
|-------|-------------|
| [Docker & Démarrage rapide](../guides/docker.fr.md) | Configuration Docker Compose, modes Launcher/Agent |
| [Applications de chat](../guides/chat-apps.fr.md) | Guides de configuration pour les 17+ channels |
| [Configuration](../guides/configuration.fr.md) | Variables d'environnement, structure du workspace, sandbox de sécurité |
| [Providers & Modèles](../guides/providers.fr.md) | 30+ providers LLM, routage de modèles, configuration model_list |
| [Spawn & Tâches asynchrones](../guides/spawn-tasks.fr.md) | Tâches rapides, tâches longues avec spawn, orchestration de sous-agents asynchrones |
| [Hooks](../architecture/hooks/README.md) | Système de hooks événementiels : observateurs, intercepteurs, hooks d'approbation |
| [Steering](../architecture/steering.md) | Injecter des messages dans une boucle agent en cours d'exécution |
| [SubTurn](../architecture/subturn.md) | Coordination de subagents, contrôle de concurrence, cycle de vie |
| [Dépannage](../operations/troubleshooting.fr.md) | Problèmes courants et solutions |
| [Configuration des outils](../reference/tools_configuration.fr.md) | Activation/désactivation par outil, politiques d'exécution, MCP, Skills |
| [Compatibilité matérielle](../guides/hardware-compatibility.fr.md) | Cartes testées, exigences minimales |

## 🤝 Contribuer & Roadmap

Les PRs sont les bienvenues ! Le code source est intentionnellement petit et lisible.

Consultez notre [Roadmap communautaire](https://github.com/stpinkie/rhizome/issues/988) et [CONTRIBUTING.md](../../CONTRIBUTING.md) pour les directives.

Groupe de développeurs en construction, rejoignez-le après votre première PR fusionnée !

Groupes d'utilisateurs :

Discord : <https://discord.gg/V4sAZ9XWpN>

WeChat :
<img src="../../assets/wechat.png" alt="WeChat group QR code" width="512">