#!/usr/bin/env bash
set -euo pipefail

echo "=== Installing dependencies for Arch Linux ==="
sudo pacman -S --needed git go nodejs npm webkit2gtk-4.1 gtk3 base-devel

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT_DIR}"

if ! command -v wails &> /dev/null; then
    echo "=== Installing Wails CLI ==="
    go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
    export PATH="$PATH:$(go env GOPATH)/bin"
fi

echo "=== Building NeoBox for Arch Linux ==="
wails build -tags "webkit2_41,with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor" -o neobox

echo "=== Build Complete! ==="
echo "Executable: ${ROOT_DIR}/build/bin/neobox"
echo "Run with:   ./build/bin/neobox"
