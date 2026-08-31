# Android Termux Guide

> Back to [Guides](README.md)

This guide covers running the Rhizome terminal binary on an ARM64 Android phone with Termux. The Android APK is not currently distributed from this fork; use Termux when you want a command-line install, or check [GitHub Releases](https://github.com/stpinkie/rhizome/releases) for a future APK.

## Requirements

- ARM64 Android device. Run `uname -m` in Termux and use this guide when it prints `aarch64`.
- Termux installed from [Termux GitHub Releases](https://github.com/termux/termux-app/releases) or F-Droid.
- Network access for downloading the release and calling your LLM provider.
- An API key for at least one configured model provider.

## Install Rhizome

Open Termux and install the packages used by the release archive and chroot wrapper:

```bash
pkg update
pkg install -y wget tar proot
```

Download and unpack the ARM64 Linux release:

```bash
mkdir -p ~/rhizome
cd ~/rhizome
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz
chmod +x ./rhizome
```

Start first-run setup through `termux-chroot`, which gives the Linux binary a more standard filesystem layout than a raw Android userspace:

```bash
termux-chroot ./rhizome onboard
```

## Configure

Edit the generated config and add at least one model provider API key:

```bash
vim ~/.rhizome/config.json
```

The default workspace is `~/.rhizome/workspace`. If you want Rhizome to read or write Android shared storage, run `termux-setup-storage` first and then point the workspace or any file paths at the mounted storage directory.

See [Configuration Guide](configuration.md) and [Providers & Model Configuration](providers.md) for the available config fields and provider examples.

## Run

Use one-shot agent mode to confirm the installation:

```bash
termux-chroot ./rhizome agent -m "Hello from Termux"
```

For long-running use, start the gateway:

```bash
termux-chroot ./rhizome gateway
```

Keep the Termux session open while Rhizome is running. Android battery optimization can stop background work, so disable battery optimization for Termux if you expect Rhizome to keep running after the screen locks.

## Update

Your config and workspace live under `~/.rhizome`, so updating the binary does not remove them:

```bash
cd ~/rhizome
rm -f rhizome_Linux_arm64.tar.gz
wget https://github.com/stpinkie/rhizome/releases/latest/download/rhizome_Linux_arm64.tar.gz
tar xzf rhizome_Linux_arm64.tar.gz
chmod +x ./rhizome
termux-chroot ./rhizome version
```

## Troubleshooting

| Symptom | Check |
|---------|-------|
| `permission denied` | Run `chmod +x ./rhizome` after unpacking the archive. |
| `not found` after running `./rhizome` | Confirm `uname -m` prints `aarch64` and that you downloaded `rhizome_Linux_arm64.tar.gz`. |
| Files or paths behave differently than Linux | Run Rhizome through `termux-chroot` instead of calling the binary directly. |
| Provider requests fail | Check the API key and network access in `~/.rhizome/config.json`. |
| Rhizome stops when the phone sleeps | Disable Android battery optimization for Termux and keep a foreground Termux session active. |
