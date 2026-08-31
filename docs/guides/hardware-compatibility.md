# 🖥️ Rhizome Hardware Compatibility List

Rhizome runs on a wide range of Linux devices. This page tracks verified chips, products, and development boards. Because the current full build is ~98 MB and the daemon uses ~60 MB private memory, boards with less than 256 MB total RAM are not verified. See the [Minimum Requirements](#5-minimum-requirements) table for the two-tier footprint.

**Your hardware not listed?** Submit a PR to add it! Hardware vendors are welcome to contribute and co-promote.

---

## 1. Verified Chip Support

### x86

| Vendor | Chip | Notes |
|--------|------|-------|
| Intel | Any x86 CPU (i386+) | All desktop/server/laptop processors |
| AMD | Any x86 CPU | All desktop/server/laptop processors |

### ARM

| Sub-arch | Typical Chips | Notes |
|----------|--------------|-------|
| ARMv6 | [BCM2835](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2835) (Raspberry Pi 1/Zero) | Single-core ARM1176JZF-S; needs 512 MB+ board for the full daemon |
| ARM64 | [Allwinner H618](https://linux-sunxi.org/H618) | Quad-core Cortex-A53, used in Orange Pi Zero 3 |
| ARM64 | [BCM2711](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2711) (Raspberry Pi 4) | Quad-core Cortex-A72 |
| ARM64 | [BCM2712](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2712) (Raspberry Pi 5) | Quad-core Cortex-A76 |
| ARM64 | [AX630C](https://www.axera-tech.com/) (爱芯元智) | Dual-core Cortex-A53 + NPU, used in NanoKVM-Pro / MaixCAM2 |

### RISC-V (riscv64)

| Vendor | Chip | Core | Notes |
|--------|------|------|-------|
| [SOPHGO (算能)](https://www.sophgo.com/) | SG2002 | C906 @ 1GHz | 256MB DDR3 on-chip; needs a 512MB+ board for the full daemon |
| [SpacemiT (进迭)](https://www.spacemit.com/) | K1 | 8x X60 @ 1.8GHz | Used in Milk-V Jupiter, BananaPi BPI-F3 |
| [SpacemiT (进迭)](https://www.spacemit.com/) | K3 | 8x X100 @ 2.5GHz | RVA23 compliant, 1024-bit RVV, FP8 AI inference |
| [Zhihe (知合)](https://www.zhihe-tech.com/) | A210 | High-perf RISC-V | 8-core, 16MB L3 cache, desktop-class |
| [Canaan (嘉楠)](https://www.canaan-creative.com/) | K230 | Dual C908 @ 1.6GHz | 6 TOPS KPU; the CanMV-K230 board adds 512MB external RAM |

### MIPS

| Vendor | Chip | Notes |
|--------|------|-------|
| MediaTek | [MT7620](https://www.mediatek.com/products/home-networking/mt7620) | MIPS24KEc @ 580MHz; typical OpenWrt routers have 256 MB or less and are not verified for the full daemon |

### LoongArch (loong64)

| Vendor | Chip | Notes |
|--------|------|-------|
| [Loongson (龙芯)](https://www.loongson.cn/) | 3A5000 | Quad-core LA464 @ 2.5GHz, desktop/workstation |
| [Loongson (龙芯)](https://www.loongson.cn/) | 3A6000 | Quad-core 4C/8T @ 2.5GHz, IPC comparable to Intel 10th gen |
| [Loongson (龙芯)](https://www.loongson.cn/) | 2K1000LA | Dual-core @ 1GHz, industrial/IoT applications |

---

## 2. Verified Products (by release date)

Consumer products, routers, and industrial devices that have been tested with Rhizome.

| Year | Product | Arch | SoC | RAM | Category |
|------|---------|------|-----|-----|----------|
| 2012 | Samsung Galaxy Note 10.1 (N8000) | ARM (A9) | Exynos 4412 | 2GB | Tablet |
| 2018 | Phicomm N1 (斐讯N1) | ARM64 (A53) | S905D | 2GB | TV Box / Home Server |
| 2025 | [NanoKVM-Pro](https://wiki.sipeed.com/hardware/en/kvm/NanoKVM_Pro/introduction.html) | ARM64 (A53) | AX630C | 1GB | Pro IP-KVM |
| 2026 | [MaixCAM2](https://wiki.sipeed.com/hardware/en/maixcam/index.html) | ARM64 (A53) | AX630C | 1/4GB | 4K AI Camera |

---

## 3. Verified Development Boards (by release date)

| Year | Board | Arch | SoC | RAM | Buy Link |
|------|-------|------|-----|-----|----------|
| 2012 | [Raspberry Pi 1 Model B](https://www.raspberrypi.com/products/) | ARMv6 | BCM2835 | 512MB | — |
| 2015 | [Raspberry Pi 2 Model B](https://www.raspberrypi.com/products/raspberry-pi-2-model-b/) | ARMv7 (A7) | BCM2836 | 1GB | — |
| 2015 | [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) | ARMv6 | BCM2835 | 512MB | — |
| 2016 | [Raspberry Pi 3 Model B](https://www.raspberrypi.com/products/raspberry-pi-3-model-b/) | ARM64 (A53) | BCM2837 | 1GB | — |
| 2019 | [Raspberry Pi 4 Model B](https://www.raspberrypi.com/products/raspberry-pi-4-model-b/) | ARM64 (A72) | BCM2711 | 1~8GB | [RPi](https://www.raspberrypi.com/) |
| 2023 | [Raspberry Pi 5](https://www.raspberrypi.com/products/raspberry-pi-5/) | ARM64 (A76) | BCM2712 | 2~8GB | [RPi](https://www.raspberrypi.com/) |
| 2024 | [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/) | RISC-V | K230 | 512MB | [Canaan](https://www.canaan-creative.com/) |

---

## 4. Also Works On

### Android Phones (via Termux)

Any ARM64 Android phone (2015+) with 1GB+ RAM. Install [Termux](https://github.com/termux/termux-app), use `proot` to run Rhizome.

> See the [Android Termux Guide](android-termux.md) for setup instructions.

### Desktop / Server / Cloud

| Platform | Notes |
|----------|-------|
| x86_64 Linux | Native binary, no dependencies |
| x86_64 Windows | Native binary |
| macOS (Intel / Apple Silicon) | Native binary |
| Docker (any platform) | `docker compose` one-liner, see [Docker Guide](docker.md) |
| OpenWrt routers | MIPS/ARM builds; requires 256 MB+ free RAM and 128 MB+ storage for the full daemon. Many consumer routers do not meet these requirements. |
| FreeBSD / NetBSD | x86_64 and arm64 builds available |

---

## 5. Minimum Requirements

Current release builds are larger than the original PicoClaw target because they include P2P/mesh support (libp2p, DHT, git sync). Use the two tiers below as a practical guide.

| Mode | Use case | Total RAM | Free RAM | Storage |
|------|----------|-----------|----------|---------|
| **Base** | One-shot `rhizome agent`, `rhizome onboard` | 256 MB | 128 MB | 128 MB |
| **Full** | `rhizome daemon` with P2P, syncer, and gateway | 512 MB | 256 MB | 128 MB |

| Resource | Minimum | Recommended |
|----------|---------|-------------|
| CPU | Any (single core 0.6GHz+) | Quad-core 1 GHz+ |
| OS | Linux (kernel 3.x+) | Linux 5.x+ |
| Network | Required (for LLM API calls) | Ethernet or WiFi |

---

## 6. How to Test & Contribute

```bash
# 1. Download for your architecture
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz

# 2. Initialize
./rhizome onboard

# 3. Test
./rhizome agent -m "Hello, what board am I running on?"
```

Available builds: `linux-amd64`, `linux-arm64`, `linux-arm`, `linux-riscv64`, `linux-loong64`, `linux-mipsle`

### Add Your Hardware

1. Fork this repo
2. Add your chip / product / board to the appropriate table
3. Include: name, arch, SoC, RAM, year, and a link if available
4. Submit a PR

Hardware vendors: want to add official support or co-promote? Open an issue or reach out via [Discord](https://discord.gg/V4sAZ9XWpN).
