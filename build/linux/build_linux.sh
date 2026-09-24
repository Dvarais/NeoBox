#!/usr/bin/env bash
set -euo pipefail

VERSION="1.8.1"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "=== Building NeoBox v${VERSION} for Linux ==="
cd "${ROOT_DIR}"

wails build -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor" -o neobox

echo "=== Packaging .deb ==="
DEB_DIR="${ROOT_DIR}/build/bin/deb"
rm -rf "${DEB_DIR}"
mkdir -p "${DEB_DIR}/DEBIAN"
mkdir -p "${DEB_DIR}/usr/bin"
mkdir -p "${DEB_DIR}/usr/share/applications"
mkdir -p "${DEB_DIR}/usr/share/icons/hicolor/512x512/apps"

cp "${ROOT_DIR}/build/linux/debian/control" "${DEB_DIR}/DEBIAN/"
cp "${ROOT_DIR}/build/linux/debian/postinst" "${DEB_DIR}/DEBIAN/"
cp "${ROOT_DIR}/build/linux/debian/postrm" "${DEB_DIR}/DEBIAN/"
chmod 755 "${DEB_DIR}/DEBIAN/postinst" "${DEB_DIR}/DEBIAN/postrm"

cp "${ROOT_DIR}/build/bin/neobox" "${DEB_DIR}/usr/bin/neobox"
chmod 755 "${DEB_DIR}/usr/bin/neobox"
cp "${ROOT_DIR}/build/linux/neobox.desktop" "${DEB_DIR}/usr/share/applications/"
cp "${ROOT_DIR}/build/linux/icon.png" "${DEB_DIR}/usr/share/icons/hicolor/512x512/apps/neobox.png"

dpkg-deb --build "${DEB_DIR}" "${ROOT_DIR}/build/bin/neobox_${VERSION}_amd64.deb"

echo "=== Linux Build Complete ==="
