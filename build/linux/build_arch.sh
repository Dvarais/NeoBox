#!/usr/bin/env bash
set -euo pipefail

# Request sudo credentials upfront and keep them active throughout the build process
# so the user is prompted for their password only once ("один общий запрос паролей").
sudo -v
while true; do sudo -n true; sleep 60; kill -0 "$$" || exit; done 2>/dev/null &
SUDO_KEEP_ALIVE_PID=$!
trap 'kill ${SUDO_KEEP_ALIVE_PID} 2>/dev/null || true' EXIT

echo "=== Installing dependencies for Arch Linux ==="
sudo pacman -S --needed git go nodejs npm webkit2gtk-4.1 gtk3 base-devel

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT_DIR}"

# Wails CLI <= v2.12 bundles x/tools v0.30, which fails on Go 1.25+ with
# "package ... without types was imported from". Pin a CLI built with newer x/tools.
echo "=== Building NeoBox for Arch Linux ==="
go run github.com/wailsapp/wails/v2/cmd/wails@v2.16.0 build -tags "webkit2_41,with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor" -o neobox

echo "=== Setting network capabilities on executable ==="
# Grant and persist root network privileges ("сохранение root прав") on the compiled binary
sudo setcap cap_net_admin,cap_net_bind_service+eip "${ROOT_DIR}/build/bin/neobox"

echo "=== Build Complete! ==="
echo "Executable: ${ROOT_DIR}/build/bin/neobox"
echo "Run with:   ./build/bin/neobox"

