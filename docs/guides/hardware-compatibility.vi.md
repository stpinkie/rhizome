> Quay lại [README](../project/README.vi.md)

# 🖥️ Rhizome Danh sách tương thích phần cứng

Rhizome chạy được trên nhiều loại thiết bị Linux. Trang này ghi nhận các chip, sản phẩm và bo mạch phát triển đã được xác minh. Bản dựng đầy đủ hiện tại có kích thước ~98 MB và daemon sử dụng ~60 MB bộ nhớ riêng, do đó các bo mạch có tổng RAM dưới 256 MB chưa được xác minh. Xem bảng [Yêu cầu tối thiểu](#5-yêu-cầu-tối-thiểu) cho dấu chân hai tầng.

**Phần cứng của bạn chưa có trong danh sách?** Gửi PR để thêm vào! Các nhà sản xuất phần cứng được hoan nghênh đóng góp và đồng quảng bá.

---

## 1. Hỗ trợ chip đã xác minh

### x86

|| Nhà sản xuất | Chip | Ghi chú |
||--------------|------|---------|
|| Intel | Any x86 CPU (i386+) | Tất cả bộ xử lý desktop/server/laptop |
|| AMD | Any x86 CPU | Tất cả bộ xử lý desktop/server/laptop |

### ARM

|| Kiến trúc phụ | Chip tiêu biểu | Ghi chú |
||----------------|----------------|---------|
|| ARMv6 | [BCM2835](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2835) (Raspberry Pi 1/Zero) | Đơn nhân ARM1176JZF-S; cần bo mạch 512 MB+ cho daemon đầy đủ |
|| ARM64 | [Allwinner H618](https://linux-sunxi.org/H618) | Bốn nhân Cortex-A53, dùng trong Orange Pi Zero 3 |
|| ARM64 | [BCM2711](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2711) (Raspberry Pi 4) | Bốn nhân Cortex-A72 |
|| ARM64 | [BCM2712](https://www.raspberrypi.com/documentation/computers/processors.html#bcm2712) (Raspberry Pi 5) | Bốn nhân Cortex-A76 |
|| ARM64 | [AX630C](https://www.axera-tech.com/) (爱芯元智) | Hai nhân Cortex-A53 + NPU, dùng trong NanoKVM-Pro / MaixCAM2 |

### RISC-V (riscv64)

|| Nhà sản xuất | Chip | Lõi | Ghi chú |
||--------------|------|-----|---------|
|| [SOPHGO (算能)](https://www.sophgo.com/) | SG2002 | C906 @ 1GHz | 256 MB DDR3 tích hợp; cần bo mạch 512 MB+ cho daemon đầy đủ |
|| [SpacemiT (进迭)](https://www.spacemit.com/) | K1 | 8x X60 @ 1.8GHz | Dùng trong Milk-V Jupiter, BananaPi BPI-F3 |
|| [SpacemiT (进迭)](https://www.spacemit.com/) | K3 | 8x X100 @ 2.5GHz | Tuân thủ RVA23, RVV 1024-bit, suy luận AI FP8 |
|| [Zhihe (知合)](https://www.zhihe-tech.com/) | A210 | High-perf RISC-V | 8 lõi, 16MB cache L3, cấp desktop |
|| [Canaan (嘉楠)](https://www.canaan-creative.com/) | K230 | Dual C908 @ 1.6GHz | 6 TOPS KPU; bo mạch CanMV-K230 bổ sung 512 MB RAM ngoài |

### MIPS

|| Nhà sản xuất | Chip | Ghi chú |
||--------------|------|---------|
|| MediaTek | [MT7620](https://www.mediatek.com/products/home-networking/mt7620) | MIPS24KEc @ 580MHz; các router OpenWrt thông thường có 256 MB hoặc ít hơn và chưa được xác minh cho daemon đầy đủ |

### LoongArch (loong64)

|| Nhà sản xuất | Chip | Ghi chú |
||--------------|------|---------|
|| [Loongson (龙芯)](https://www.loongson.cn/) | 3A5000 | Bốn nhân LA464 @ 2.5GHz, desktop/máy trạm |
|| [Loongson (龙芯)](https://www.loongson.cn/) | 3A6000 | Bốn nhân 4C/8T @ 2.5GHz, IPC tương đương Intel thế hệ 10 |
|| [Loongson (龙芯)](https://www.loongson.cn/) | 2K1000LA | Hai nhân @ 1GHz, ứng dụng công nghiệp/IoT |

---

## 2. Sản phẩm đã xác minh (theo ngày phát hành)

Sản phẩm tiêu dùng, router và thiết bị công nghiệp đã được kiểm thử với Rhizome.

|| Năm | Sản phẩm | Kiến trúc | SoC | RAM | Danh mục |
||-----|----------|-----------|-----|-----|----------|
|| 2012 | Samsung Galaxy Note 10.1 (N8000) | ARM (A9) | Exynos 4412 | 2GB | Máy tính bảng |
|| 2018 | Phicomm N1 (斐讯N1) | ARM64 (A53) | S905D | 2GB | TV Box / Máy chủ gia đình |
|| 2025 | [NanoKVM-Pro](https://wiki.sipeed.com/hardware/en/kvm/NanoKVM_Pro/introduction.html) | ARM64 (A53) | AX630C | 1GB | IP-KVM Pro |
|| 2026 | [MaixCAM2](https://wiki.sipeed.com/hardware/en/maixcam/index.html) | ARM64 (A53) | AX630C | 1/4GB | Camera AI 4K |

---

## 3. Bo mạch phát triển đã xác minh (theo ngày phát hành)

|| Năm | Bo mạch | Kiến trúc | SoC | RAM | Liên kết mua |
||-----|---------|-----------|-----|-----|--------------|
|| 2012 | [Raspberry Pi 1 Model B](https://www.raspberrypi.com/products/) | ARMv6 | BCM2835 | 512MB | — |
|| 2015 | [Raspberry Pi 2 Model B](https://www.raspberrypi.com/products/raspberry-pi-2-model-b/) | ARMv7 (A7) | BCM2836 | 1GB | — |
|| 2015 | [Raspberry Pi Zero](https://www.raspberrypi.com/products/raspberry-pi-zero/) | ARMv6 | BCM2835 | 512MB | — |
|| 2016 | [Raspberry Pi 3 Model B](https://www.raspberrypi.com/products/raspberry-pi-3-model-b/) | ARM64 (A53) | BCM2837 | 1GB | — |
|| 2019 | [Raspberry Pi 4 Model B](https://www.raspberrypi.com/products/raspberry-pi-4-model-b/) | ARM64 (A72) | BCM2711 | 1~8GB | [RPi](https://www.raspberrypi.com/) |
|| 2023 | [Raspberry Pi 5](https://www.raspberrypi.com/products/raspberry-pi-5/) | ARM64 (A76) | BCM2712 | 2~8GB | [RPi](https://www.raspberrypi.com/) |
|| 2024 | [CanMV-K230](https://developer.canaan-creative.com/k230_canmv/en/main/) | RISC-V | K230 | 512MB | [Canaan](https://www.canaan-creative.com/) |

---

## 4. Cũng hoạt động trên

### Điện thoại Android (qua Termux)

Bất kỳ điện thoại Android ARM64 nào (2015+) với 1GB+ RAM. Cài đặt [Termux](https://github.com/termux/termux-app), sử dụng `proot` để chạy Rhizome.

> Xem [README: Chạy trên điện thoại Android cũ](../project/README.vi.md#-run-on-old-android-phones) để biết hướng dẫn cài đặt.

### Desktop / Máy chủ / Đám mây

|| Nền tảng | Ghi chú |
||----------|---------|
|| x86_64 Linux | Binary gốc, không phụ thuộc |
|| x86_64 Windows | Binary gốc |
|| macOS (Intel / Apple Silicon) | Binary gốc |
|| Docker (any platform) | `docker compose` một dòng lệnh, xem [Hướng dẫn Docker](docker.md) |
|| OpenWrt routers | Bản dựng MIPS/ARM; yêu cầu 256 MB+ RAM trống và 128 MB+ lưu trữ cho daemon đầy đủ. Nhiều router tiêu dùng không đáp ứng các yêu cầu này. |
|| FreeBSD / NetBSD | Có bản dựng x86_64 và arm64 |

---

## 5. Yêu cầu tối thiểu

Các bản dựng phát hành hiện tại lớn hơn mục tiêu PicoClaw ban đầu vì chúng bao gồm hỗ trợ P2P/mesh (libp2p, DHT, git sync). Hãy sử dụng hai tầng dưới đây như một hướng dẫn thực tế.

|| Chế độ | Trường hợp sử dụng | Tổng RAM | RAM trống | Lưu trữ |
||--------|-------------------|----------|-----------|---------|
|| **Cơ bản** | `rhizome agent`, `rhizome onboard` one-shot | 256 MB | 128 MB | 128 MB |
|| **Đầy đủ** | `rhizome daemon` với P2P, syncer và gateway | 512 MB | 256 MB | 128 MB |

|| Tài nguyên | Tối thiểu | Khuyến nghị |
||------------|-----------|-------------|
|| CPU | Bất kỳ (đơn nhân 0.6GHz+) | Bốn nhân 1 GHz+ |
|| OS | Linux (kernel 3.x+) | Linux 5.x+ |
|| Mạng | Bắt buộc (cho các lệnh gọi API LLM) | Ethernet hoặc WiFi |

---

## 6. Cách kiểm thử và đóng góp

```bash
# 1. Tải xuống cho kiến trúc của bạn
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz

# 2. Khởi tạo
./rhizome onboard

# 3. Kiểm thử
./rhizome agent -m "Hello, what board am I running on?"
```

Các bản dựng có sẵn: `linux-amd64`, `linux-arm64`, `linux-arm`, `linux-riscv64`, `linux-loong64`, `linux-mipsle`

### Thêm phần cứng của bạn

1. Fork kho lưu trữ này
2. Thêm chip / sản phẩm / bo mạch của bạn vào bảng tương ứng
3. Bao gồm: tên, kiến trúc, SoC, RAM, năm và liên kết nếu có
4. Gửi PR

Nhà sản xuất phần cứng: muốn thêm hỗ trợ chính thức hoặc đồng quảng bá? Mở issue hoặc liên hệ qua [Discord](https://discord.gg/V4sAZ9XWpN).
