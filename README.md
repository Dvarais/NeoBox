# NeoBox

[English](#english) | [Русский](#русский)

---

## English

NeoBox is a secure, high-performance VPN client for Windows powered by sing-box and built with Go and Wails. It features a custom frameless window with a native Windows Acrylic translucency effect.

### Installation Guide for Users

#### Prerequisites
NeoBox is compiled for Windows. Windows 10 or Windows 11 is recommended to support the full visual effects of the Acrylic translucent backdrop.

#### Steps
1. Download the latest installer `NeoBox_Setup_v1.7.5.exe` or the standalone executable `NeoBox.exe` from the Releases section of the GitHub repository.
2. Run the installer to install NeoBox on your system.
3. Launch the application.
4. If you enable TUN mode, the application will prompt you for Administrator privileges. This is required because creating a virtual network interface (using the Wintun driver) to route system-wide traffic requires administrative control.
5. The required `wintun.dll` driver is bundled with the application and loaded automatically.

### Developer Guide

#### Prerequisites
To run and build this project from source, you must install:
- Go (version 1.20 or newer)
- Node.js (version 16 or newer) and npm
- Wails CLI (install via `go install github.com/wailsapp/wails/v2/cmd/wails@latest`)

#### Directory Structure
- `backend/`: Core Go logic — sing-box lifecycle, config generation, encrypted storage, and the Wails service bindings.
- `frontend/`: Vanilla JavaScript UI bundled with Vite — custom title bar, connection state, and settings.
- `build/`: Application icons, Windows build templates, and installers.
- `cmd/`: Release tooling (`keygen`, `sign`).

#### Development Mode
To start a hot-reloading development server with uTLS support enabled (which allows sing-box outbound TLS masquerading), run the following command in the project root:
```bash
wails dev -tags with_utls
```
This launches a Vite development server for the frontend and compiles the backend on the fly.

#### Production Build
To compile a production-ready package with full capabilities (including uTLS, Clash API, QUIC, WireGuard, and gVisor support), run:
```bash
wails build -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor"
```
The compiled binaries will be outputted to the `build/bin/` directory.

#### Using the Go toolchain directly
`go build ./...` and `go test ./...` work on a fresh clone. `frontend/dist/`
ships a tracked `.gitkeep` so that the `//go:embed all:frontend/dist` pattern
always matches something — without it, Go fails the build outright before npm
has ever run.

A binary produced that way embeds **no user interface**. Build the frontend
first whenever you intend to actually run the app:
```bash
cd frontend && npm ci && npm run build
```
`wails build` and `wails dev` do this for you.

#### Cutting a release
Every release **must** carry an Ed25519 signature next to the installer.
Without it, NeoBox 1.7.5 and newer refuse to install the update in-app and fall
back to opening the release page (see `SECURITY_CHANGES.md`, fix #2):
```bash
wails build -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor"
go run ./cmd/sign -key <private_key_hex> -file NeoBox_Setup_v1.7.5.exe
```
Upload the resulting file as a release asset named exactly
`<installer filename>.sig`. Keep the private key offline; `go run ./cmd/keygen`
generates a new pair if it is ever lost, but the public key in
`backend/security/signature.go` must then be updated before the release ships.

---

## Русский

NeoBox — это безопасный и высокопроизводительный VPN-клиент для Windows на базе ядра sing-box, разработанный с использованием Go и Wails. Он обладает кастомным окном без рамок (frameless) с поддержкой полупрозрачного эффекта размытия Windows Acrylic.

### Руководство по установке для пользователей

#### Системные требования
NeoBox скомпилирован под операционную систему Windows. Для полноценной поддержки визуального эффекта Acrylic рекомендуется использовать Windows 10 или Windows 11.

#### Шаги установки
1. Загрузите актуальный установщик `NeoBox_Setup_v1.7.5.exe` or портативную версию `NeoBox.exe` из раздела релизов (Releases) репозитория на GitHub.
2. Запустите установщик для установки NeoBox в систему.
3. Запустите установленное приложение.
4. При включении TUN-режима приложение запросит права администратора. Это необходимо, так как создание виртуального сетевого интерфейса (через драйвер Wintun) для маршрутизации системного трафика требует привилегий суперпользователя.
5. Необходимый драйвер `wintun.dll` поставляется в комплекте с приложением и загружается автоматически.

### Руководство для разработчиков

#### Требования к окружению
Для запуска и сборки проекта из исходного кода вам понадобятся:
- Go (версии 1.20 или новее)
- Node.js (версии 16 или новее) и менеджер пакетов npm
- Wails CLI (установка через команду `go install github.com/wailsapp/wails/v2/cmd/wails@latest`)

#### Структура директорий
- `backend/`: Основная логика на Go — жизненный цикл sing-box, генерация конфигурации, шифрованное хранилище и биндинги Wails.
- `frontend/`: Интерфейс на чистом JavaScript, собираемый через Vite (кастомный заголовок окна, статус подключения и управление настройками).
- `build/`: Иконки приложения, шаблоны манифестов для Windows и скрипты сборки установщика.
- `cmd/`: Утилиты выпуска релизов (`keygen`, `sign`).

#### Режим разработки
Чтобы запустить сервер разработки с поддержкой автоматической перезагрузки кода и поддержкой uTLS (необходим для маскировки исходящего TLS-трафика в sing-box), выполните следующую команду в корневой папке проекта:
```bash
wails dev -tags with_utls
```
Эта команда запустит локальный Vite-сервер для фронтенда и скомпилирует бэкенд на лету.

#### Сборка финального релиза
Для компиляции готового к распространению дистрибутива со всеми возможностями (включая uTLS, Clash API, QUIC, WireGuard и gVisor), выполните:
```bash
wails build -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor"
```
Скомпилированные файлы сборки будут сохранены в директорию `build/bin/`.

#### Прямая работа с Go
`go build ./...` и `go test ./...` работают на свежем клоне. В `frontend/dist/`
лежит отслеживаемый `.gitkeep`, чтобы шаблон `//go:embed all:frontend/dist`
всегда что-то находил — без него Go прерывает сборку ещё до того, как запускался
npm.

Собранный так бинарник **не содержит интерфейса**. Перед реальным запуском
приложения соберите фронтенд:
```bash
cd frontend && npm ci && npm run build
```
`wails build` и `wails dev` делают это автоматически.

#### Выпуск релиза
Каждый релиз **обязан** нести Ed25519-подпись рядом с установщиком. Без неё
NeoBox 1.7.5 и новее откажется устанавливать обновление внутри приложения и
откроет страницу релиза (см. `SECURITY_CHANGES.md`, исправление #2):
```bash
wails build -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor"
go run ./cmd/sign -key <приватный_ключ_hex> -file NeoBox_Setup_v1.7.5.exe
```
Загрузите полученный файл как ассет релиза с именем ровно
`<имя установщика>.sig`. Приватный ключ храните офлайн; `go run ./cmd/keygen`
создаст новую пару, если он утерян, но тогда до выпуска релиза нужно обновить
публичный ключ в `backend/security/signature.go`.
