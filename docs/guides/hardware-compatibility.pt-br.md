> Voltar ao [README](../project/README.pt-br.md)

# 🖥️ Rhizome Lista de compatibilidade de hardware

O Rhizome roda em uma ampla gama de dispositivos Linux. Esta página registra chips, produtos e placas de desenvolvimento verificados. A build completa atual tem ~98 MB e o daemon usa ~60 MB de memória privada, então placas com menos de 256 MB de RAM total não são verificadas. Veja a tabela de [Requisitos mínimos](#5-requisitos-mínimos) para a pegada em dois níveis.

**Seu hardware não está na lista?** Envie um PR para adicioná-lo! Fabricantes de hardware são bem-vindos para contribuir e co-promover.

---

## 1. Suporte a chips verificado

### x86

|| Fabricante | Chip | Notas |
||------------|------|-------|
|| Intel | Any x86 CPU (i386+) | Todos os processadores desktop/servidor/notebook |
|| AMD | Any x86 CPU | Todos os processadores desktop/servidor/notebook |

### ARM

|| Sub-arq | Chips típicos | Notas |
||---------|---------------|-------|
|| ARMv6 | [BCM2835](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2835) (Raspberry Pi 1/Zero) | Single-core ARM1176JZF-S; precisa de uma placa com 512 MB+ para o daemon completo |
|| ARM64 | [Allwinner H618](https://linux-sunxi.org/H618) | Quad-core Cortex-A53, usado no Orange Pi Zero 3 |
|| ARM64 | [BCM2711](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2711) (Raspberry Pi 4) | Quad-core Cortex-A72 |
|| ARM64 | [BCM2712](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2712) (Raspberry Pi 5) | Quad-core Cortex-A76 |
|| ARM64 | [AX630C](https://www.axera-tech.com/) (爱芯元智) | Dual-core Cortex-A53 + NPU, usado no NanoKVM-Pro / MaixCAM2 |

### RISC-V (riscv64)

|| Fabricante | Chip | Núcleo | Notas |
||------------|------|--------|-------|
|| [SOPHGO (算能)](https://www.sophgo.com/) | SG2002 | C906 @ 1GHz | 256 MB DDR3 integrado; precisa de uma placa com 512 MB+ para o daemon completo |
|| [SpacemiT (进迭)](https://www.spacemit.com/) | K1 | 8x X60 @ 1.8GHz | Usado no Milk-V Jupiter, BananaPi BPI-F3 |
|| [SpacemiT (进迭)](https://www.spacemit.com/) | K3 | 8x X100 @ 2.5GHz | Compatível com RVA23, RVV de 1024 bits, inferência AI FP8 |
|| [Zhihe (知合)](https://www.zhihe-tech.com/) | A210 | High-perf RISC-V | 8 núcleos, 16MB cache L3, classe desktop |
|| [Canaan (嘉楠)](https://www.canaan-creative.com/) | K230 | Dual C908 @ 1.6GHz | 6 TOPS KPU; a placa CanMV-K230 adiciona 512 MB de RAM externa |

### MIPS

|| Fabricante | Chip | Notas |
||------------|------|-------|
|| MediaTek | [MT7620](https://www.mediatek.com/products/home-networking/mt7620) | MIPS24KEc @ 580MHz; roteadores OpenWrt típicos têm 256 MB ou menos e não são verificados para o daemon completo |

### LoongArch (loong64)

|| Fabricante | Chip | Notas |
||------------|------|-------|
|| [Loongson (龙芯)](https://www.loongson.cn/) | 3A5000 | Quad-core LA464 @ 2.5GHz, desktop/estação de trabalho |
|| [Loongson (龙芯)](https://www.loongson.cn/) | 3A6000 | Quad-core 4C/8T @ 2.5GHz, IPC comparável ao Intel 10ª geração |
|| [Loongson (龙芯)](https://www.loongson.cn/) | 2K1000LA | Dual-core @ 1GHz, aplicações industriais/IoT |

---

## 2. Produtos verificados (por data de lançamento)

Produtos de consumo, roteadores e dispositivos industriais testados com o Rhizome.

|| Ano | Produto | Arq | SoC | RAM | Categoria |
||-----|---------|-----|-----|-----|-----------|
|| 2012 | Samsung Galaxy Note 10.1 (N8000) | ARM (A9) | Exynos 4412 | 2GB | Tablet |
|| 2018 | Phicomm N1 (斐讯N1) | ARM64 (A53) | S905D | 2GB | TV Box / Servidor doméstico |
|| 2025 | [NanoKVM-Pro](https://wiki.sipeed.com/hardware/en/kvm/NanoKVM_Pro/introduction.html) | ARM64 (A53) | AX630C | 1GB | IP-KVM Pro |
|| 2026 | [MaixCAM2](https://wiki.sipeed.com/hardware/en/maixcam/index.html) | ARM64 (A53) | AX630C | 1/4GB | Câmera AI 4K |

---

## 3. Placas de desenvolvimento verificadas (por data de lançamento)

|| Ano | Placa | Arq | SoC | RAM | Link de compra |
||-----|-------|-----|-----|-----|----------------|
|| 2012 | [Raspberry Pi 1 Model B](https://www.raspberrypi.com/products/) | ARMv6 | BCM2835 | 512MB | — |
|| 2015 | [Raspberry Pi 2 Model B](https://www.raspberrypi.com/products/raspberry-pi-2-model-b/) | ARMv7 (A7) | BCM2836 | 1GB | — |
|| 2015 | [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) | ARMv6 | BCM2835 | 512MB | — |
|| 2016 | [Raspberry Pi 3 Model B](https://www.raspberrypi.com/products/raspberry-pi-3-model-b/) | ARM64 (A53) | BCM2837 | 1GB | — |
|| 2019 | [Raspberry Pi 4 Model B](https://www.raspberrypi.com/products/raspberry-pi-4-model-b/) | ARM64 (A72) | BCM2711 | 1~8GB | [RPi](https://www.raspberrypi.com/) |
|| 2023 | [Raspberry Pi 5](https://www.raspberrypi.com/products/raspberry-pi-5/) | ARM64 (A76) | BCM2712 | 2~8GB | [RPi](https://www.raspberrypi.com/) |
|| 2024 | [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/) | RISC-V | K230 | 512MB | [Canaan](https://www.canaan-creative.com/) |

---

## 4. Também funciona em

### Celulares Android (via Termux)

Qualquer celular Android ARM64 (2015+) com 1GB+ de RAM. Instale o [Termux](https://github.com/termux/termux-app), use `proot` para rodar o Rhizome.

> Veja [README: Rodar em celulares Android antigos](../project/README.pt-br.md#-run-on-old-android-phones) para instruções de configuração.

### Desktop / Servidor / Nuvem

|| Plataforma | Notas |
||------------|-------|
|| x86_64 Linux | Binário nativo, sem dependências |
|| x86_64 Windows | Binário nativo |
|| macOS (Intel / Apple Silicon) | Binário nativo |
|| Docker (any platform) | `docker compose` em uma linha, veja [Guia Docker](docker.md) |
|| OpenWrt routers | Builds MIPS/ARM; requer 256 MB+ de RAM livre e 128 MB+ de armazenamento para o daemon completo. Muitos roteadores domésticos não atendem a esses requisitos. |
|| FreeBSD / NetBSD | Builds x86_64 e arm64 disponíveis |

---

## 5. Requisitos mínimos

As builds atuais são maiores que o alvo original do PicoClaw porque incluem suporte a P2P/mesh (libp2p, DHT, git sync). Use os dois níveis abaixo como guia prático.

|| Modo | Caso de uso | RAM total | RAM livre | Armazenamento |
||------|-------------|-----------|-----------|---------------|
|| **Base** | `rhizome agent`, `rhizome onboard` one-shot | 256 MB | 128 MB | 128 MB |
|| **Completo** | `rhizome daemon` com P2P, syncer e gateway | 512 MB | 256 MB | 128 MB |

|| Recurso | Mínimo | Recomendado |
||---------|--------|-------------|
|| CPU | Qualquer (single-core 0,6GHz+) | Quad-core 1 GHz+ |
|| OS | Linux (kernel 3.x+) | Linux 5.x+ |
|| Rede | Necessária (para chamadas de API LLM) | Ethernet ou WiFi |

---

## 6. Como testar e contribuir

```bash
# 1. Baixar para sua arquitetura
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz

# 2. Inicializar
./rhizome onboard

# 3. Testar
./rhizome agent -m "Hello, what board am I running on?"
```

Builds disponíveis: `linux-amd64`, `linux-arm64`, `linux-arm`, `linux-riscv64`, `linux-loong64`, `linux-mipsle`

### Adicionar seu hardware

1. Faça fork deste repositório
2. Adicione seu chip / produto / placa na tabela apropriada
3. Inclua: nome, arquitetura, SoC, RAM, ano e um link se disponível
4. Envie um PR

Fabricantes de hardware: deseja adicionar suporte oficial ou co-promover? Abra uma issue ou entre em contato via [Discord](https://discord.gg/V4sAZ9XWpN).
