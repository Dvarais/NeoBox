# Design Specification: NeoBox Linux Port and Distribution

- **Date:** 2026-09-25
- **Author:** Antigravity & Dvarais
- **Status:** Approved / Ready for Implementation Planning

---

## 1. Executive Summary

NeoBox is a high-performance proxy and VPN client built with Go 1.24, Wails v2 (WebKit-based desktop GUI), and an embedded sing-box core. Currently, the codebase relies on Windows-specific APIs across 14 files (DPAPI encryption, WinINet system proxy, Windows Firewall `netsh`, `wintun.dll`, Win32 `IcmpSendEcho`, Win32 `RegisterHotKey`, and UAC elevation tokens).

This specification defines the architecture, platform abstraction layer (PAL), Linux system implementations, and multi-distribution packaging pipeline to enable NeoBox to run natively on Linux distros (Ubuntu/Debian, Arch Linux, Fedora, openSUSE, etc.) alongside Windows with zero regressions to the existing Windows experience.

---

## 2. Requirements & Constraints

### 2.1 Functional Requirements
1. **Full Linux Support:** All proxy protocols (VLESS, VMess, Trojan, Shadowsocks, TUIC, Hysteria, Hysteria 2, WireGuard) and routing features (TUN mode, rules, split tunneling, GeoIP/Geosite) must work identically on Linux.
2. **Dedicated Linux Folder Layout:** All Linux-specific implementations must reside in dedicated, isolated files and directories (`backend/platform/linux/` and `build/linux/`), cleanly separating Linux logic from Windows and core application logic.
3. **Privilege Model:** Follow the Linux Principle of Least Privilege. The GUI application runs as a standard unprivileged user. Network privileges (`CAP_NET_ADMIN` and `CAP_NET_BIND_SERVICE`) are granted to the binary via Linux Capabilities (`setcap`) set by package managers, eliminating the need for periodic `sudo` or Polkit password popups.
4. **Kill Switch:** Modern `nftables` packet filtering with fallback to `iptables` to block non-tunnel outbound WAN traffic, accompanied by crash-resilient marker file recovery.
5. **System Proxy:** Automatic desktop environment proxy configuration for GNOME, Cinnamon, MATE (via `gsettings`) and KDE Plasma (via `kwriteconfig5/6`), saving and restoring user settings cleanly.
6. **Data Storage & XDG:** Strict compliance with the XDG Base Directory specification (`~/.config/neobox/` for configuration and encrypted secrets with POSIX `0700`/`0600` permissions, `~/.local/share/neobox/` for runtime logs and markers).
7. **Packaging & Distribution:**
   - Universal standalone `AppImage` for one-click execution across any modern Linux distro.
   - Native `.deb` package for Ubuntu/Debian/Mint with automatic capability and desktop integration.
   - `PKGBUILD` and install hooks for Arch Linux / Manjaro / EndeavourOS (AUR package `neobox-bin`).
8. **Automated CI/CD:** GitHub Actions workflow with an `ubuntu-latest` runner building, testing, and generating all Linux artifacts automatically on commit and release tags.

### 2.2 Constraints & Non-Goals
- **Zero Windows Regressions:** Windows build and functionality must remain completely unaffected.
- **No GUI Root Execution:** The Wails/WebKitGTK process must never be launched as root or via `sudo`/`pkexec`, preventing file permission corruption and Wayland/X11 socket security issues.
- **No External Daemon (YAGNI):** NeoBox remains a self-contained single binary; a split daemon/client IPC architecture is out of scope.

---

## 3. Architecture & File Structure

### 3.1 Package Hierarchy

```text
NeoBox/
├── backend/
│   ├── core/                        # Cross-platform proxy engine (sing-box, parsing, rules)
│   ├── storage/                     # Cross-platform JSON file persistence
│   ├── i18n/                        # Multi-language translation tables
│   ├── service/                     # Application business logic (sessions, subscriptions, history)
│   └── platform/                    # Platform Abstraction Layer (PAL)
│       ├── platform.go              # Abstract interfaces definition
│       ├── factory_windows.go       # Windows PAL factory (//go:build windows)
│       ├── factory_linux.go         # Linux PAL factory (//go:build linux)
│       ├── windows/                 # Windows-only implementation
│       │   ├── encryption.go        # Windows DPAPI
│       │   ├── firewall.go          # Windows Firewall netsh
│       │   ├── proxy.go             # WinINet / registry
│       │   ├── elevation.go         # Token inspection & TerminateProcess
│       │   ├── icmp.go              # iphlpapi IcmpSendEcho
│       │   └── hotkey.go            # Win32 RegisterHotKey
│       └── linux/                   # Linux-only implementation (isolated)
│           ├── paths.go             # XDG paths & directory initialization
│           ├── encryption.go        # AES-256-GCM + file key (mode 0600) + machine-id
│           ├── firewall.go          # nftables / iptables killswitch
│           ├── proxy.go             # gsettings & KDE proxy manager
│           ├── capabilities.go      # Linux Capabilities & privilege checker
│           ├── icmp.go              # Linux unprivileged ICMP socket ping
│           └── hotkey.go            # Linux shortcut stub / portal integration
├── build/
│   ├── windows/                     # Existing Windows icons and manifests
│   └── linux/                       # Linux packaging assets & templates
│       ├── neobox.desktop           # XDG desktop shortcut
│       ├── icon.png                 # 512x512 application icon
│       ├── build_linux.sh           # Local Linux build script
│       ├── debian/                  # Debian package templates
│       │   ├── control              # Dependencies & metadata
│       │   ├── postinst             # Capabilities & desktop database hook
│       │   └── postrm               # Cleanup hooks
│       ├── arch/                    # Arch Linux AUR templates
│       │   ├── PKGBUILD             # AUR build script
│       │   └── neobox.install       # Post-install capability hook
│       └── appimage/                # AppImage packaging templates
│           └── AppRun               # Entrypoint wrapper
└── main.go                          # Entrypoint with platform-specific window options
```

### 3.2 Platform Interfaces (`backend/platform/platform.go`)

```go
package platform

import "time"

// Platform defines the unified interface implemented for each OS.
type Platform interface {
    Paths() PathManager
    Security() SecurityManager
    Firewall() FirewallManager
    Proxy() ProxyManager
    Privileges() PrivilegeManager
    Prober() NetworkProber
    Lifecycle() LifecycleManager
}

type PathManager interface {
    UserDataDir() string
    ConfigDir() string
    LogDir() string
    ResolvePath(elem ...string) string
}

type SecurityManager interface {
    Init(userDataDir string) error
    Encrypt(plain []byte) ([]byte, error)
    Decrypt(cipher []byte) ([]byte, error)
}

type FirewallManager interface {
    EnableKillSwitch(serverHost string) error
    DisableKillSwitch() error
    RecoverKillSwitch(userDataDir string)
}

type ProxyManager interface {
    SetSystemProxy(addr string) error
    RestoreSystemProxy() error
    RecoverSystemProxy(userDataDir string)
}

type PrivilegeManager interface {
    CanRunTUN() bool
    CanConfigureFirewall() bool
    PrivilegeHelpMessage() string
}

type NetworkProber interface {
    Ping(host string, timeout time.Duration) int
}

type LifecycleManager interface {
    HideConsoleIfNeeded()
    TerminateProcess(code int)
}

// Global accessor
var current Platform

func Current() Platform {
    return current
}

func SetCurrent(p Platform) {
    current = p
}
```

---

## 4. Subsystem Implementations for Linux

### 4.1 Paths & Storage (`backend/platform/linux/paths.go`)
- `ConfigDir()` returns `filepath.Join(os.UserConfigDir(), "neobox")` (typically `~/.config/neobox`).
- `LogDir()` returns `filepath.Join(os.UserHomeDir(), ".local", "share", "neobox")`.
- All created directories enforce POSIX mode `0700` (`rwx------`).

### 4.2 Encryption & Secret Storage (`backend/platform/linux/encryption.go`)
- On startup, checks for `~/.config/neobox/key.dat`.
- If absent, generates 32 cryptographically secure random bytes via `crypto/rand`.
- Persists `key.dat` with strict permissions `0600` (`rw-------`).
- Encrypts and decrypts subscriptions and credentials using AES-256-GCM with a 12-byte random nonce prefixed to the ciphertext, completely compatible with NeoBox's payload structure.
- Optional machine-binding: mixes `/etc/machine-id` or `/var/lib/dbus/machine-id` into an auxiliary key derivation step (PBKDF2/HKDF) so that moving `key.dat` across machines renders it invalid.

### 4.3 Privileges & TUN Network Stack (`backend/platform/linux/capabilities.go`)
- **sing-box integration:** Unlike Windows which loads `wintun.dll`, sing-box on Linux natively utilizes `/dev/net/tun` via `tun` inbound in the kernel.
- **Capability Check:** Inspects process capabilities using Linux `capget` syscall or by testing `/dev/net/tun` accessibility.
- `CanRunTUN()` reports `true` if `CAP_NET_ADMIN` is effective.
- If missing, `PrivilegeHelpMessage()` returns:
  `"TUN-режим требует сетевых привилегий. Выполните в терминале:\nsudo setcap cap_net_admin,cap_net_bind_service+ep <path-to-binary>"`

### 4.4 Kill Switch (`backend/platform/linux/firewall.go`)
- Utilizes `nftables` when available; falls back to `iptables` if `nft` is not installed.
- **nftables implementation:**
  - Creates dedicated table `inet neobox_killswitch`:
    ```text
    table inet neobox_killswitch {
        chain output {
            type filter hook output priority 0; policy drop;
            oifname "lo" accept
            ip daddr { 127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 } accept
            ip6 daddr { ::1, fe80::/10, fc00::/7 } accept
            ip daddr <vpn-server-ip> accept
            oifname "tun*" accept
        }
    }
    ```
- **Cleanup & Recovery:**
  - Disabling drops the table: `nft delete table inet neobox_killswitch`.
  - Crash recovery checks `killswitch.active` marker in `UserDataDir()` and executes table deletion on boot.

### 4.5 System Proxy (`backend/platform/linux/proxy.go`)
- Detects the desktop environment via `XDG_CURRENT_DESKTOP`:
  - **GNOME / Cinnamon / MATE:**
    - Enable: `gsettings set org.gnome.system.proxy mode 'manual'`, `gsettings set org.gnome.system.proxy.http host '127.0.0.1'`, port `20809` (and https/socks).
    - Disable: `gsettings set org.gnome.system.proxy mode 'none'`.
  - **KDE Plasma:**
    - Enable: `kwriteconfig5` / `kwriteconfig6 --file kioslaverc --group "Proxy Settings" --key "ProxyType" 1`, `--key "httpProxy" "http://127.0.0.1:20809"`.
    - Disable: `kwriteconfig5/6 --file kioslaverc --group "Proxy Settings" --key "ProxyType" 0`.
- Backs up previous values in `proxy-backup.json` to restore user configuration accurately upon disconnect.

### 4.6 Latency Prober (`backend/platform/linux/icmp.go`)
- Resolves server target.
- For TCP protocols: measures `net.DialTimeout("tcp", ...)`.
- For UDP protocols (WireGuard, TUIC, Hysteria): uses Linux unprivileged datagram ICMP socket (`net.DialPacket("udp4", ...)`) permitted by modern Linux kernels (`net.ipv4.ping_group_range`), with fallback to system `ping -c 1 -W <timeout> <host>`.

### 4.7 Windowing & Wails Integration (`main.go`)
- Separates `windows.Options` and `linux.Options`:
  - Windows: retain backdrop, DWM styling, border options.
  - Linux: configure WebKit2GTK settings, window icon via embedded PNG.
- Embedded assets: conditionally embed `icon.ico` on Windows and `icon.png` on Linux.

---

## 5. Packaging & Distribution Pipeline

### 5.1 Debian / Ubuntu (`.deb`)
- Target: Ubuntu 20.04+, Debian 11+, Linux Mint.
- Package name: `neobox_amd64.deb`.
- Dependencies: `libc6, libgtk-3-0, libwebkit2gtk-4.1-0 | libwebkit2gtk-4.0-37, libappindicator3-1, iproute2`.
- `postinst`:
  ```bash
  #!/bin/sh
  set -e
  setcap cap_net_admin,cap_net_bind_service+ep /usr/bin/neobox || true
  update-desktop-database -q || true
  ```

### 5.2 Arch Linux (AUR / `PKGBUILD`)
- Package name: `neobox-bin`.
- Architecture: `x86_64`.
- Dependencies: `gtk3`, `webkit2gtk-4.1`, `libappindicator-gtk3`, `iproute2`.
- Install hook: `neobox.install` runs `setcap` on install and update.

### 5.3 Universal Standalone (`AppImage`)
- Target: Fedora, openSUSE, RHEL, and any distribution without package manager installation.
- Built using `appimagetool` with standard `AppRun` and `.desktop` metadata.

### 5.4 GitHub Actions Workflow (`.github/workflows/ci.yml`)
- Adds an `ubuntu-latest` matrix job alongside Windows:
  1. Installs build tools: `libgtk-3-dev`, `libwebkit2gtk-4.1-dev`, `libappindicator3-dev`.
  2. Sets up Go 1.24 and Node.js.
  3. Runs `go vet` and `go test` with Linux build tags.
  4. Builds binary via Wails CLI.
  5. Packages `.deb`, `AppImage`, and Arch tarball.
  6. Attaches artifacts to GitHub Releases on tag pushes.

---

## 6. Verification and Testing Plan

1. **Compilation Verification:**
   - Verify Windows builds still compile cleanly with tags: `go test ./...`.
   - Verify Linux cross-compilation: `GOOS=linux go vet ./...`.
2. **Unit Tests:**
   - Platform interface unit tests using mock implementations.
   - Verification of XDG path resolution, AES-256-GCM encryption round-trip, and configuration loading.
3. **CI Run:**
   - Ensure the new GitHub Actions workflow succeeds on both `windows-latest` and `ubuntu-latest`.
