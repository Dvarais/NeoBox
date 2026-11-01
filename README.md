# NeoBox

> **Release:** v1.9.4 | **Build Code:** `NBX-194-GUARD`  
> Technical VPN and proxy client on sing-box core. Written in Go and Wails v2.

[English](#english) | [Русский](#русский)

---

## English

### System Overview

NeoBox is an open-source proxy and VPN client designed for high-throughput routing, zero-leak network security, and fine-grained traffic dispatching. The network core is built directly on [sing-box](https://github.com/SagerNet/sing-box) runtime libraries in Go. The graphical shell uses [Wails v2](https://wails.io/) with a custom frameless glass interface rendered via hardware acceleration.

### Architectural Modes

NeoBox supports two distinct operational modes:

1. **TUN Adapter Mode (Kernel-level)**: Creates a virtual network adapter via the `Wintun` driver on Windows or `/dev/net/tun` on Linux. All system IP traffic is routed through the sing-box userspace network stack (`gVisor` or `system` engine). Requires elevated privileges (`Administrator` on Windows, `CAP_NET_ADMIN` on Linux).
2. **System Proxy Mode (Socket-level)**: Runs an internal mixed HTTP and SOCKS5 inbound proxy bound to `127.0.0.1:20809`. Configures OS-level proxy settings for compatible applications without modifying virtual network interfaces.

### Supported Protocol Matrix

| Protocol | Core Transport | Cryptography / Masquerading | Multiplexing |
|---|---|---|---|
| **VLESS** | TCP, WebSocket, gRPC, HTTP/2 | Reality, Vision, TLS 1.3 | smux, yamux, h2mux |
| **VMess** | TCP, WebSocket, gRPC, HTTP/2 | AEAD, VMess-AEAD | smux, yamux |
| **Trojan** | TCP, WebSocket, gRPC | TLS 1.3, ALPN | smux, yamux |
| **Shadowsocks** | TCP, UDP | 2022-blake3, AEAD (ChaCha20-Poly1305, AES-GCM) | smux |
| **Hysteria 2** | UDP (QUIC) | Salamander obfuscation, BBR congestion control | Native QUIC streams |
| **TUIC** | UDP (QUIC v5) | Custom QUIC handshake, 0-RTT authentication | Native QUIC streams |
| **WireGuard** | UDP | Noise_IK handshake, Curve25519, ChaCha20-Poly1305 | Multichannel |

### Security Specification (`NBX-192-SEC`)

The `NBX-192-SEC` release hardens system security, process boundaries, and routing isolation:

* **Zero-Leak DNS Architecture**:
  * Dedicated DNS-over-HTTPS (DoH, RFC 8484) connection pool reusing persistent HTTP/2 sessions (`MaxIdleConnsPerHost: 10`, `MaxConnsPerHost: 20`).
  * Asynchronous, independent resolution for IPv4 (A) and IPv6 (AAAA) records.
  * Deterministic routing for DNS queries: DNS server IP addresses bypass domain rules, while all application domain queries route via encrypted remote resolvers.
  * IPv6 leak prevention: unroutable IPv6 endpoints drop silently or route to isolated blackhole destinations.
* **SSRF and DNS Rebinding Prevention**:
  * Subscription fetching implements a low-level `net.Dialer.Control` socket hook. Every resolved destination IP address is evaluated at the socket creation level, preventing Time-of-Check to Time-of-Use (TOCTOU) DNS rebinding attacks.
  * Restricted ranges include loopback (`127.0.0.0/8`), private networks (RFC 1918), link-local (`169.254.0.0/16`), Carrier-Grade NAT (`100.64.0.0/10`), IPv6 Unique Local (`fc00::/7`), link-local (`fe80::/10`), Teredo (`2001::/32`), and 6to4 (`2002::/16`).
* **Process Boundary and Win32 DACL Hardening**:
  * Single instance mutex utilizes strict Security Descriptor Definition Language (SDDL): `D:(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;IU)`. Access is restricted to `SYSTEM`, `Builtin Administrators`, and the active `Interactive User`. World Generic All (`WD:GA`) access is removed.
  * Linux single instance enforcement relies on non-blocking `unix.Flock` with runtime descriptor pinning.
* **Firewall and Kill Switch**:
  * Windows: Native Windows Filtering Platform (WFP) rules blocking outbound traffic outside the active VPN tunnel interface.
  * Linux: Atomic `nftables` transaction scripting piped via standard input (`nft -f -`). Marker files guarantee rule cleanup upon application restart if a sudden system crash occurs.
* **Data Protection at Rest**:
  * Windows: AES-256-GCM encryption with master key protected by Windows Data Protection API (DPAPI).
  * Linux: AES-256-GCM cipher with key derivation anchored to system machine ID and an isolated per-user private entropy salt with strict `0600` permissions.
* **Fail-Closed Binary Update Verification**:
  * Release manifests and binary payloads require verification against an embedded Ed25519 cryptographic public key. Untrusted signatures abort automatic installations immediately.

### Installation

#### Windows (10 / 11, x64)

* **Installer**: Download `NeoBox_Setup_v1.9.4.exe` from [GitHub Releases](https://github.com/Dvarais/NeoBox/releases) and execute.
* **Portable**: Download `NeoBox.exe`, place it in a writable directory, and run directly.
* *Note*: TUN mode automatically triggers a standard UAC elevation prompt to load the bundled `wintun.dll` driver.

#### Linux (x86_64)

* **Arch Linux / Manjaro**:
  ```bash
  sudo pacman -U neobox-1.9.4-1-x86_64.pkg.tar.zst
  # or via AUR helper:
  yay -S neobox-bin
  ```
* **Debian / Ubuntu**:
  ```bash
  sudo dpkg -i neobox_1.9.4_amd64.deb
  sudo apt-get install -f
  ```

### Build from Source

#### Prerequisites

* Go 1.24 or newer (`go version`)
* Node.js 18+ and npm
* Wails CLI: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
* Linux dependencies: `webkit2gtk-4.1`, `gtk3`

#### Development

```bash
# Clone the repository
git clone https://github.com/Dvarais/NeoBox.git
cd NeoBox

# Run with hot reload (uTLS enabled)
wails dev -tags with_utls
```

#### Production Build

```bash
# Compile optimized release binary with full protocol support
wails build -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor"
```

The output binary will be written to `build/bin/`.

#### Project Structure

| Path | Responsibility |
|---|---|
| `backend/core/` | sing-box runtime integration, config generator, DoH resolver, TLS root pool |
| `backend/service/` | Wails application bindings, watchdog, state machine, elevation, settings |
| `backend/security/` | DPAPI, Linux encryption vault, firewall drivers, Ed25519 signature checks |
| `backend/platform/` | OS-specific drivers: WFP (Windows), nftables (Linux), ICMP ping |
| `backend/storage/` | Thread-safe encrypted storage with atomic write-temp-rename commits |
| `frontend/` | Frameless UI written in TypeScript and CSS, bundled with Vite |
| `build/` | OS packaging: NSIS installer, Arch PKGBUILD, Debian control, application icons |

---

## Русский

### Архитектура и назначение

NeoBox: клиент прокси и VPN с открытым исходным кодом, рассчитанный на высокую пропускную способность, защиту от утечек трафика и гибкую маршрутизацию. Сетевое ядро интегрировано напрямую с библиотеками среды [sing-box](https://github.com/SagerNet/sing-box) на языке Go. Графическая оболочка построена на [Wails v2](https://wails.io/) с кастомным безрамочным интерфейсом, использующим аппаратное ускорение веб-движка.

### Режимы маршрутизации

Клиент поддерживает два режима работы:

1. **Режим TUN-адаптера (уровень сетевого ядра)**: создает виртуальный сетевой интерфейс через драйвер `Wintun` в Windows или `/dev/net/tun` в Linux. Весь системный IP-трафик направляется в стек sing-box (`gVisor` или системный стек). Для работы требуются права администратора (`Administrator` в Windows, `CAP_NET_ADMIN` в Linux).
2. **Режим системного прокси (уровень сокетов приложений)**: запускает локальный комбинированный HTTP и SOCKS5 прокси на адресе `127.0.0.1:20809`. Автоматически настраивает системные параметры прокси в ОС для программ, поддерживающих эти настройки, без создания сетевых интерфейсов.

### Поддерживаемые протоколы

| Протокол | Транспорт ядра | Шифрование и маскировка | Мультиплексирование |
|---|---|---|---|
| **VLESS** | TCP, WebSocket, gRPC, HTTP/2 | Reality, Vision, TLS 1.3 | smux, yamux, h2mux |
| **VMess** | TCP, WebSocket, gRPC, HTTP/2 | AEAD, VMess-AEAD | smux, yamux |
| **Trojan** | TCP, WebSocket, gRPC | TLS 1.3, ALPN | smux, yamux |
| **Shadowsocks** | TCP, UDP | 2022-blake3, AEAD (ChaCha20-Poly1305, AES-GCM) | smux |
| **Hysteria 2** | UDP (QUIC) | Маскировка Salamander, контроль перегрузок BBR | Нативные потоки QUIC |
| **TUIC** | UDP (QUIC v5) | Кастомное рукопожатие QUIC, авторизация 0-RTT | Нативные потоки QUIC |
| **WireGuard** | UDP | Рукопожатие Noise_IK, Curve25519, ChaCha20-Poly1305 | Многоканальный обмен |

### Спецификация безопасности (`NBX-192-SEC`)

Ревизия `NBX-192-SEC` усиливает защиту сетевого периметра, изоляцию процессов и сохранность локальных данных:

* **Маршрутизация DNS без утечек**:
  * Выделенный пул DNS-over-HTTPS (DoH, RFC 8484) с повторным использованием сессий HTTP/2 (`MaxIdleConnsPerHost: 10`, `MaxConnsPerHost: 20`).
  * Независимое параллельное разрешение записей IPv4 (A) и IPv6 (AAAA).
  * Разделение маршрутов: IP-адреса DoH-резолверов идут в обход доменных правил, а запросы приложений идут исключительно через зашифрованные удаленные серверы.
  * Блокировка утечек IPv6: запросы к отключенным или немаршрутизируемым IPv6 сбрасываются без обращения к открытому порту 53.
* **Защита от атак SSRF и DNS Rebinding**:
  * При загрузке подписок работает сокетный фильтр `net.Dialer.Control`. Адрес назначения проверяется непосредственно перед системным вызовом соединения, что исключает атаки подмены адреса (TOCTOU).
  * Заблокированы диапазоны loopback (`127.0.0.0/8`), частные подсети (RFC 1918), link-local (`169.254.0.0/16`), CGNAT (`100.64.0.0/10`), IPv6 Unique Local (`fc00::/7`), link-local (`fe80::/10`), Teredo (`2001::/32`) и 6to4 (`2002::/16`).
* **Изоляция системных процессов и Win32 DACL**:
  * Именованный мьютекс единственного экземпляра защищен строгим дескриптором безопасности: `D:(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;IU)`. Доступ открыт только для `SYSTEM`, встроенных администраторов и текущего интерактивного пользователя. Небезопасный доступ для всех пользователей (`WD:GA`) исключен.
  * В Linux проверка единственного процесса реализована через неблокирующий вызов `unix.Flock` с закреплением дескриптора в памяти.
* **Сетевой экран и Kill Switch**:
  * В Windows: нативные правила Windows Filtering Platform (WFP), отсекающие трафик мимо активного интерфейса туннеля.
  * В Linux: атомарный пайп правил `nftables` через стандартный ввод (`nft -f -`). Файлы-маркеры гарантируют автоматическую очистку правил при перезапуске после сбоя питания.
* **Криптографическая защита конфигураций**:
  * В Windows: шифрование алгоритмом AES-256-GCM с защитой мастер-ключа через Windows DPAPI.
  * В Linux: шифрование AES-256-GCM с привязкой к machine-id и пользовательской солью энтропии с правами доступа `0600`.
* **Верификация обновлений**:
  * Каждый релизный файл проверяется цифровой подписью Ed25519 в режиме отказа по умолчанию (fail-closed). Без валидной подписи установка не запускается.

### Установка

#### Windows (10 и 11, x64)

* **Установщик**: скачайте файл `NeoBox_Setup_v1.9.4.exe` со страницы [GitHub Releases](https://github.com/Dvarais/NeoBox/releases) и запустите установку.
* **Портативная версия**: скачайте `NeoBox.exe`, положите в отдельную папку и запускайте напрямую.
* При включении TUN-режима появится стандартный запрос UAC Windows для загрузки драйвера `wintun.dll`.

#### Linux (x86_64)

* **Arch Linux / Manjaro**:
  ```bash
  sudo pacman -U neobox-1.9.4-1-x86_64.pkg.tar.zst
  # либо через AUR:
  yay -S neobox-bin
  ```
* **Debian / Ubuntu**:
  ```bash
  sudo dpkg -i neobox_1.9.4_amd64.deb
  sudo apt-get install -f
  ```

### Сборка из исходников

#### Требования к окружению

* Компилятор Go версии 1.24 или новее
* Платформа Node.js 18+ и пакетный менеджер npm
* Утилита Wails CLI: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
* Библиотеки в Linux: `webkit2gtk-4.1`, `gtk3`

#### Запуск в режиме разработки

```bash
# Клонирование репозитория
git clone https://github.com/Dvarais/NeoBox.git
cd NeoBox

# Запуск с горячей перезагрузкой и поддержкой uTLS
wails dev -tags with_utls
```

#### Сборка релизного бинарника

```bash
# Полная сборка со всеми сетевыми протоколами
wails build -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor"
```

Скомпилированный файл появится в каталоге `build/bin/`.

#### Структура репозитория

| Каталог | Назначение |
|---|---|
| `backend/core/` | Интеграция среды sing-box, генерация конфигураций, резолвер DoH, сертификаты TLS |
| `backend/service/` | Сервисный слой Wails, сторожевой таймер, автомат состояний VPN, настройки |
| `backend/security/` | Подсистемы DPAPI, хранилище ключей Linux, сетевой экран, подписи Ed25519 |
| `backend/platform/` | Системные драйверы платформ: WFP (Windows), nftables (Linux), ICMP-пинг |
| `backend/storage/` | Потокобезопасное хранилище с атомарной записью через временные файлы |
| `frontend/` | Безрамочный интерфейс на TypeScript и CSS, собираемый через Vite |
| `build/` | Пакеты сборщика: NSIS для Windows, PKGBUILD для Arch, control для Debian |
