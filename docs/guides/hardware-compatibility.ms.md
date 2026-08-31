> Kembali ke [README](../project/README.ms.md)

# 🖥️ Senarai Keserasian Perkakasan Rhizome

Rhizome boleh berjalan di pelbagai peranti Linux. Halaman ini merekodkan cip, produk, dan papan pembangunan yang telah disahkan. Memandangkan binaan penuh semasa adalah ~98 MB dan daemon menggunakan ~60 MB memori peribadi, papan dengan jumlah RAM kurang daripada 256 MB tidak disahkan. Lihat jadual [Keperluan Minimum](#5-keperluan-minimum) untuk jejak dua tahap.

**Perkakasan anda tidak tersenarai?** Hantar PR untuk menambahnya! Pengeluar perkakasan dialu-alukan untuk menyumbang dan mempromosi bersama.

---

## 1. Sokongan Cip yang Disahkan

### x86

|| Pengeluar | Cip | Nota |
||-----------|-----|------|
|| Intel | Any x86 CPU (i386+) | Semua pemproses desktop/pelayan/laptop |
|| AMD | Any x86 CPU | Semua pemproses desktop/pelayan/laptop |

### ARM

|| Sub-arkitektur | Cip Tipikal | Nota |
||----------------|-------------|------|
|| ARMv6 | [BCM2835](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2835) (Raspberry Pi 1/Zero) | Tunggal inti ARM1176JZF-S; memerlukan papan 512 MB+ untuk daemon penuh |
|| ARM64 | [Allwinner H618](https://linux-sunxi.org/H618) | Empat inti Cortex-A53, digunakan dalam Orange Pi Zero 3 |
|| ARM64 | [BCM2711](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2711) (Raspberry Pi 4) | Empat inti Cortex-A72 |
|| ARM64 | [BCM2712](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2712) (Raspberry Pi 5) | Empat inti Cortex-A76 |
|| ARM64 | [AX630C](https://www.axera-tech.com/) (爱芯元智) | Dwi inti Cortex-A53 + NPU, digunakan dalam NanoKVM-Pro / MaixCAM2 |

### RISC-V (riscv64)

|| Pengeluar | Cip | Teras | Nota |
||-----------|-----|-------|------|
|| [SOPHGO (算能)](https://www.sophgo.com/) | SG2002 | C906 @ 1GHz | 256 MB DDR3 on-chip; memerlukan papan 512 MB+ untuk daemon penuh |
|| [SpacemiT (进迭)](https://www.spacemit.com/) | K1 | 8x X60 @ 1.8GHz | Digunakan dalam Milk-V Jupiter, BananaPi BPI-F3 |
|| [SpacemiT (进迭)](https://www.spacemit.com/) | K3 | 8x X100 @ 2.5GHz | Mematuhi RVA23, RVV 1024-bit, inferens AI FP8 |
|| [Zhihe (知合)](https://www.zhihe-tech.com/) | A210 | High-perf RISC-V | 8 teras, 16MB cache L3, kelas desktop |
|| [Canaan (嘉楠)](https://www.canaan-creative.com/) | K230 | Dual C908 @ 1.6GHz | 6 TOPS KPU; papan CanMV-K230 menambah 512 MB RAM luaran |

### MIPS

|| Pengeluar | Cip | Nota |
||-----------|-----|------|
|| MediaTek | [MT7620](https://www.mediatek.com/products/home-networking/mt7620) | MIPS24KEc @ 580MHz; penghala OpenWrt biasa mempunyai 256 MB atau kurang dan tidak disahkan untuk daemon penuh |

### LoongArch (loong64)

|| Pengeluar | Cip | Nota |
||-----------|-----|------|
|| [Loongson (龙芯)](https://www.loongson.cn/) | 3A5000 | Empat inti LA464 @ 2.5GHz, desktop/stesen kerja |
|| [Loongson (龙芯)](https://www.loongson.cn/) | 3A6000 | Empat inti 4C/8T @ 2.5GHz, setanding dengan Intel generasi ke-10 |
|| [Loongson (龙芯)](https://www.loongson.cn/) | 2K1000LA | Dwi inti @ 1GHz, aplikasi industri/IoT |

---

## 2. Produk yang Disahkan (mengikut tarikh keluaran)

Produk pengguna, penghala, dan peranti industri yang telah diuji dengan Rhizome.

|| Tahun | Produk | Ark | SoC | RAM | Kategori |
||-------|--------|-----|-----|-----|----------|
|| 2012 | Samsung Galaxy Note 10.1 (N8000) | ARM (A9) | Exynos 4412 | 2GB | Tablet |
|| 2018 | Phicomm N1 (斐讯N1) | ARM64 (A53) | S905D | 2GB | TV Box / Pelayan Rumah |
|| 2025 | [NanoKVM-Pro](https://wiki.sipeed.com/hardware/en/kvm/NanoKVM_Pro/introduction.html) | ARM64 (A53) | AX630C | 1GB | IP-KVM Pro |
|| 2026 | [MaixCAM2](https://wiki.sipeed.com/hardware/en/maixcam/index.html) | ARM64 (A53) | AX630C | 1/4GB | Kamera AI 4K |

---

## 3. Papan Pembangunan yang Disahkan (mengikut tarikh keluaran)

|| Tahun | Papan | Ark | SoC | RAM | Pautan Belian |
||-------|-------|-----|-----|-----|---------------|
|| 2012 | [Raspberry Pi 1 Model B](https://www.raspberrypi.com/products/) | ARMv6 | BCM2835 | 512MB | — |
|| 2015 | [Raspberry Pi 2 Model B](https://www.raspberrypi.com/products/raspberry-pi-2-model-b/) | ARMv7 (A7) | BCM2836 | 1GB | — |
|| 2015 | [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) | ARMv6 | BCM2835 | 512MB | — |
|| 2016 | [Raspberry Pi 3 Model B](https://www.raspberrypi.com/products/raspberry-pi-3-model-b/) | ARM64 (A53) | BCM2837 | 1GB | — |
|| 2019 | [Raspberry Pi 4 Model B](https://www.raspberrypi.com/products/raspberry-pi-4-model-b/) | ARM64 (A72) | BCM2711 | 1~8GB | [RPi](https://www.raspberrypi.com/) |
|| 2023 | [Raspberry Pi 5](https://www.raspberrypi.com/products/raspberry-pi-5/) | ARM64 (A76) | BCM2712 | 2~8GB | [RPi](https://www.raspberrypi.com/) |
|| 2024 | [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/) | RISC-V | K230 | 512MB | [Canaan](https://www.canaan-creative.com/) |

---

## 4. Juga Berfungsi Pada

### Telefon Android (melalui Termux)

Mana-mana telefon Android ARM64 (2015+) dengan 1GB+ RAM. Pasang [Termux](https://github.com/termux/termux-app), gunakan `proot` untuk menjalankan Rhizome.

> Lihat [README: Jalankan pada telefon Android lama](../project/README.ms.md#-run-on-old-android-phones) untuk arahan penyediaan.

### Desktop / Pelayan / Awan

|| Platform | Nota |
||----------|------|
|| x86_64 Linux | Binari asli, tiada kebergantungan |
|| x86_64 Windows | Binari asli |
|| macOS (Intel / Apple Silicon) | Binari asli |
|| Docker (any platform) | `docker compose` satu baris, lihat [Panduan Docker](docker.md) |
|| OpenWrt routers | Binaan MIPS/ARM; memerlukan 256 MB+ RAM percuma dan 128 MB+ storan untuk daemon penuh. Kebanyakan penghala pengguna tidak memenuhi keperluan ini. |
|| FreeBSD / NetBSD | Binaan x86_64 dan arm64 tersedia |

---

## 5. Keperluan Minimum

Binaan keluaran semasa lebih besar daripada sasaran PicoClaw asal kerana ia merangkumi sokongan P2P/mesh (libp2p, DHT, git sync). Gunakan dua tahap di bawah sebagai panduan praktikal.

|| Mod | Kes Penggunaan | Jumlah RAM | RAM Percuma | Storan |
||-----|----------------|------------|-------------|--------|
|| **Asas** | One-shot `rhizome agent`, `rhizome onboard` | 256 MB | 128 MB | 128 MB |
|| **Penuh** | `rhizome daemon` dengan P2P, syncer, dan gateway | 512 MB | 256 MB | 128 MB |

|| Sumber | Minimum | Disyorkan |
||--------|---------|-----------|
|| CPU | Mana-mana (satu teras 0.6GHz+) | Empat teras 1 GHz+ |
|| OS | Linux (kernel 3.x+) | Linux 5.x+ |
|| Rangkaian | Diperlukan (untuk panggilan API LLM) | Ethernet atau WiFi |

---

## 6. Cara Menguji & Menyumbang

```bash
# 1. Muat turun untuk arkitektur anda
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz

# 2. Inisialisasi
./rhizome onboard

# 3. Uji
./rhizome agent -m "Hello, what board am I running on?"
```

Binaan tersedia: `linux-amd64`, `linux-arm64`, `linux-arm`, `linux-riscv64`, `linux-loong64`, `linux-mipsle`

### Tambah Perkakasan Anda

1. Fork repositori ini
2. Tambah cip / produk / papan ke jadual yang sesuai
3. Sertakan: nama, arkitektur, SoC, RAM, tahun, dan pautan jika ada
4. Hantar PR

Pengeluar perkakasan: ingin menambah sokongan rasmi atau promosi bersama? Buka isu atau hubungi melalui [Discord](https://discord.gg/V4sAZ9XWpN).
