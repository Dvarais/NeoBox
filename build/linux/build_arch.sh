#!/usr/bin/env bash
set -euo pipefail

echo "=== Installing dependencies for Arch Linux ==="
sudo pacman -S --needed git go nodejs npm webkit2gtk-4.1 gtk3 base-devel

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT_DIR}"

# Wails CLI <= v2.12 bundles x/tools v0.30, which fails on Go 1.25+ with
# "package ... without types was imported from". Pin a CLI built with newer x/tools.
echo "=== Building NeoBox for Arch Linux ==="
go run github.com/wailsapp/wails/v2/cmd/wails@v2.16.0 build -tags "webkit2_41,with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor" -o neobox

echo "=== Build Complete! ==="
echo "Executable: ${ROOT_DIR}/build/bin/neobox"
echo "Run with:   ./build/bin/neobox"
