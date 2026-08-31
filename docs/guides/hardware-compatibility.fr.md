> Retour au [README](../project/README.fr.md)

# 🖥️ Rhizome Liste de compatibilité matérielle

Rhizome fonctionne sur un large éventail d'appareils Linux. Cette page répertorie les puces, produits et cartes de développement vérifiés. La build complète actuelle fait ~98 MB et le démon utilise ~60 MB de mémoire privée ; les cartes disposant de moins de 256 MB de RAM totale ne sont pas vérifiées. Consultez le tableau [Configuration minimale requise](#5-configuration-minimale-requise) pour l'empreinte à deux niveaux.

**Votre matériel n'est pas listé ?** Soumettez une PR pour l'ajouter ! Les fabricants de matériel sont invités à contribuer et à co-promouvoir.

---

## 1. Support de puces vérifié

### x86

|| Fabricant | Puce | Notes |
||-----------|------|-------|
|| Intel | Any x86 CPU (i386+) | Tous les processeurs de bureau/serveur/portable |
|| AMD | Any x86 CPU | Tous les processeurs de bureau/serveur/portable |

### ARM

|| Sous-arch | Puces typiques | Notes |
||-----------|----------------|-------|
|| ARMv6 | [BCM2835](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2835) (Raspberry Pi 1/Zero) | Monocœur ARM1176JZF-S ; besoin d'une carte avec 512 MB+ pour le démon complet |
|| ARM64 | [Allwinner H618](https://linux-sunxi.org/H618) | Quadricœur Cortex-A53, utilisé dans Orange Pi Zero 3 |
|| ARM64 | [BCM2711](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2711) (Raspberry Pi 4) | Quadricœur Cortex-A72 |
|| ARM64 | [BCM2712](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2712) (Raspberry Pi 5) | Quadricœur Cortex-A76 |
|| ARM64 | [AX630C](https://www.axera-tech.com/) (爱芯元智) | Bicœur Cortex-A53 + NPU, utilisé dans NanoKVM-Pro / MaixCAM2 |

### RISC-V (riscv64)

|| Fabricant | Puce | Cœur | Notes |
||-----------|------|------|-------|
|| [SOPHGO (算能)](https://www.sophgo.com/) | SG2002 | C906 @ 1GHz | 256 MB DDR3 intégré ; besoin d'une carte avec 512 MB+ pour le démon complet |
|| [SpacemiT (进迭)](https://www.spacemit.com/) | K1 | 8x X60 @ 1.8GHz | Utilisé dans Milk-V Jupiter, BananaPi BPI-F3 |
|| [SpacemiT (进迭)](https://www.spacemit.com/) | K3 | 8x X100 @ 2.5GHz | Conforme RVA23, RVV 1024 bits, inférence AI FP8 |
|| [Zhihe (知合)](https://www.zhihe-tech.com/) | A210 | High-perf RISC-V | 8 cœurs, 16MB cache L3, classe bureau |
|| [Canaan (嘉楠)](https://www.canaan-creative.com/) | K230 | Dual C908 @ 1.6GHz | 6 TOPS KPU ; la carte CanMV-K230 ajoute 512 MB de RAM externe |

### MIPS

|| Fabricant | Puce | Notes |
||-----------|------|-------|
|| MediaTek | [MT7620](https://www.mediatek.com/products/home-networking/mt7620) | MIPS24KEc @ 580MHz ; les routeurs OpenWrt typiques disposent de 256 MB ou moins et ne sont pas vérifiés pour le démon complet |

### LoongArch (loong64)

|| Fabricant | Puce | Notes |
||-----------|------|-------|
|| [Loongson (龙芯)](https://www.loongson.cn/) | 3A5000 | Quadricœur LA464 @ 2.5GHz, bureau/station de travail |
|| [Loongson (龙芯)](https://www.loongson.cn/) | 3A6000 | Quadricœur 4C/8T @ 2.5GHz, IPC comparable à Intel 10e génération |
|| [Loongson (龙芯)](https://www.loongson.cn/) | 2K1000LA | Bicœur @ 1GHz, applications industrielles/IoT |

---

## 2. Produits vérifiés (par date de sortie)

Produits grand public, routeurs et appareils industriels testés avec Rhizome.

|| Année | Produit | Arch | SoC | RAM | Catégorie |
||-------|---------|------|-----|-----|-----------|
|| 2012 | Samsung Galaxy Note 10.1 (N8000) | ARM (A9) | Exynos 4412 | 2GB | Tablette |
|| 2018 | Phicomm N1 (斐讯N1) | ARM64 (A53) | S905D | 2GB | Boîtier TV / Serveur domestique |
|| 2025 | [NanoKVM-Pro](https://wiki.sipeed.com/hardware/en/kvm/NanoKVM_Pro/introduction.html) | ARM64 (A53) | AX630C | 1GB | IP-KVM Pro |
|| 2026 | [MaixCAM2](https://wiki.sipeed.com/hardware/en/maixcam/index.html) | ARM64 (A53) | AX630C | 1/4GB | Caméra AI 4K |

---

## 3. Cartes de développement vérifiées (par date de sortie)

|| Année | Carte | Arch | SoC | RAM | Lien d'achat |
||-------|-------|------|-----|-----|--------------|
|| 2012 | [Raspberry Pi 1 Model B](https://www.raspberrypi.com/products/) | ARMv6 | BCM2835 | 512MB | — |
|| 2015 | [Raspberry Pi 2 Model B](https://www.raspberrypi.com/products/raspberry-pi-2-model-b/) | ARMv7 (A7) | BCM2836 | 1GB | — |
|| 2015 | [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) | ARMv6 | BCM2835 | 512MB | — |
|| 2016 | [Raspberry Pi 3 Model B](https://www.raspberrypi.com/products/raspberry-pi-3-model-b/) | ARM64 (A53) | BCM2837 | 1GB | — |
|| 2019 | [Raspberry Pi 4 Model B](https://www.raspberrypi.com/products/raspberry-pi-4-model-b/) | ARM64 (A72) | BCM2711 | 1~8GB | [RPi](https://www.raspberrypi.com/) |
|| 2023 | [Raspberry Pi 5](https://www.raspberrypi.com/products/raspberry-pi-5/) | ARM64 (A76) | BCM2712 | 2~8GB | [RPi](https://www.raspberrypi.com/) |
|| 2024 | [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/) | RISC-V | K230 | 512MB | [Canaan](https://www.canaan-creative.com/) |

---

## 4. Fonctionne également sur

### Téléphones Android (via Termux)

Tout téléphone Android ARM64 (2015+) avec 1 Go+ de RAM. Installez [Termux](https://github.com/termux/termux-app), utilisez `proot` pour exécuter Rhizome.

> Voir [README : Exécuter sur d'anciens téléphones Android](../project/README.fr.md#-run-on-old-android-phones) pour les instructions de configuration.

### Bureau / Serveur / Cloud

|| Plateforme | Notes |
||------------|-------|
|| x86_64 Linux | Binaire natif, aucune dépendance |
|| x86_64 Windows | Binaire natif |
|| macOS (Intel / Apple Silicon) | Binaire natif |
|| Docker (any platform) | `docker compose` en une ligne, voir [Guide Docker](docker.md) |
|| OpenWrt routers | Builds MIPS/ARM ; nécessitent 256 MB+ de RAM libre et 128 MB+ de stockage pour le démon complet. De nombreux routeurs grand public ne répondent pas à ces exigences. |
|| FreeBSD / NetBSD | Builds x86_64 et arm64 disponibles |

---

## 5. Configuration minimale requise

Les builds actuelles sont plus volumineuses que l'objectif PicoClaw d'origine car elles incluent le support P2P/mesh (libp2p, DHT, synchronisation git). Utilisez les deux niveaux ci-dessous comme guide pratique.

|| Mode | Cas d'usage | RAM totale | RAM libre | Stockage |
||------|-------------|------------|-----------|----------|
|| **Base** | `rhizome agent`, `rhizome onboard` en one-shot | 256 MB | 128 MB | 128 MB |
|| **Complet** | `rhizome daemon` avec P2P, syncer et gateway | 512 MB | 256 MB | 128 MB |

|| Ressource | Minimum | Recommandé |
||-----------|---------|------------|
|| CPU | N'importe lequel (monocœur 0,6 GHz+) | Quadricœur 1 GHz+ |
|| OS | Linux (kernel 3.x+) | Linux 5.x+ |
|| Réseau | Requis (pour les appels API LLM) | Ethernet ou WiFi |

---

## 6. Comment tester et contribuer

```bash
# 1. Télécharger pour votre architecture
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz

# 2. Initialiser
./rhizome onboard

# 3. Tester
./rhizome agent -m "Hello, what board am I running on?"
```

Builds disponibles : `linux-amd64`, `linux-arm64`, `linux-arm`, `linux-riscv64`, `linux-loong64`, `linux-mipsle`

### Ajouter votre matériel

1. Forkez ce dépôt
2. Ajoutez votre puce / produit / carte dans le tableau approprié
3. Incluez : nom, architecture, SoC, RAM, année et un lien si disponible
4. Soumettez une PR

Fabricants de matériel : vous souhaitez ajouter un support officiel ou co-promouvoir ? Ouvrez une issue ou contactez-nous via [Discord](https://discord.gg/V4sAZ9XWpN).
