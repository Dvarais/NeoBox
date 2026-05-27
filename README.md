# NeoBox

[English](#english) | [Русский](#русский)

---

## English

NeoBox is a secure, high-performance VPN client for Windows powered by sing-box and built with Go and Wails. It features a custom frameless window with a native Windows Acrylic translucency effect.

### Installation Guide for Users

#### Prerequisites
NeoBox is compiled for Windows. Windows 10 or Windows 11 is recommended to support the full visual effects of the Acrylic translucent backdrop.

#### Steps
1. Download the latest installer `NeoBox_Setup_v1.5.8.exe` or the standalone executable `NeoBox.exe` from the Releases section of the GitHub repository.
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
- `backend/`: Core Go logic, VPN configuration handling, encryption/decryption, and Wails services.
- `frontend/`: Svelte/HTML5 user interface, custom title bar, connection state, and settings control.
- `build/`: Application icons, Windows build templates, and installers.

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

---

## Русский

NeoBox — это безопасный и высокопроизводительный VPN-клиент для Windows на базе ядра sing-box, разработанный с использованием Go и Wails. Он обладает кастомным окном без рамок (frameless) с поддержкой полупрозрачного эффекта размытия Windows Acrylic.

### Руководство по установке для пользователей

#### Системные требования
NeoBox скомпилирован под операционную систему Windows. Для полноценной поддержки визуального эффекта Acrylic рекомендуется использовать Windows 10 или Windows 11.

#### Шаги установки
1. Загрузите актуальный установщик `NeoBox_Setup_v1.5.8.exe` or портативную версию `NeoBox.exe` из раздела релизов (Releases) репозитория на GitHub.
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
- `backend/`: Основная логика на языке Go, работа с конфигурациями VPN, шифрование/дешифрование данных и интеграция с Wails.
- `frontend/`: Пользовательский интерфейс на Svelte/HTML5 (кастомный заголовок окна, статус подключения и управление настройками).
- `build/`: Иконки приложения, шаблоны манифестов для Windows и скрипты сборки установщика.

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
