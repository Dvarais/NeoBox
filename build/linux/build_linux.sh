#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Read version from wails.json or fallback to 1.9.1
VERSION=$(grep -o '"productVersion": *"[^"]*"' "${ROOT_DIR}/wails.json" 2>/dev/null | cut -d'"' -f4 || echo "1.9.1")
if [ -z "${VERSION}" ]; then
  VERSION="1.9.1"
fi

PRIVATE_KEY="${PRIVATE_KEY:-}"

# Parse optional arguments: -k / --key <key_or_file>
while [[ $# -gt 0 ]]; do
  case "$1" in
    -k|--key)
      PRIVATE_KEY="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

# If PRIVATE_KEY points to an existing file, read the key from it (keeps key out of history)
if [[ -n "${PRIVATE_KEY}" && -f "${PRIVATE_KEY}" ]]; then
  PRIVATE_KEY="$(head -n 1 "${PRIVATE_KEY}" | tr -d '\r\n ')"
fi

echo "=== Building NeoBox v${VERSION} for Linux ==="
cd "${ROOT_DIR}"

# Ensure frontend is built
if [ ! -d "${ROOT_DIR}/frontend/dist" ] || [ ! -f "${ROOT_DIR}/frontend/dist/index.html" ]; then
  echo "Building frontend..."
  (cd "${ROOT_DIR}/frontend" && npm run build)
fi

# Clean previous build artifacts
rm -rf "${ROOT_DIR}/build/linux/pkg" "${ROOT_DIR}/build/linux/src"

# If makepkg is available, run it to build the native Arch package
if command -v makepkg >/dev/null 2>&1; then
  echo "=== Packaging .pkg.tar.zst (Arch Linux) ==="
  (cd "${ROOT_DIR}/build/linux" && makepkg -f)
  if [ -f "${ROOT_DIR}/build/linux/neobox-${VERSION}-1-x86_64.pkg.tar.zst" ]; then
    mv -f "${ROOT_DIR}/build/linux/neobox-${VERSION}-1-x86_64.pkg.tar.zst" "${ROOT_DIR}/build/bin/"
    rm -rf "${ROOT_DIR}/build/linux/pkg" "${ROOT_DIR}/build/linux/src"
  fi
else
  # Wails CLI build
  go run github.com/wailsapp/wails/v2/cmd/wails@v2.16.0 build -s -skipbindings -tags "webkit2_41,with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor" -o neobox
fi

# Create standardized linux binary copy
cp -f "${ROOT_DIR}/build/bin/neobox" "${ROOT_DIR}/build/bin/neobox-linux-amd64"
chmod 755 "${ROOT_DIR}/build/bin/neobox" "${ROOT_DIR}/build/bin/neobox-linux-amd64"

echo "=== Packaging .deb ==="
# Build deb inside /tmp to avoid NTFS 777 permission restrictions on WSL mounts
DEB_DIR="$(mktemp -d /tmp/neobox-deb-XXXXXX)"
trap 'rm -rf "${DEB_DIR:-}"' EXIT

mkdir -p "${DEB_DIR}/DEBIAN"
mkdir -p "${DEB_DIR}/usr/bin"
mkdir -p "${DEB_DIR}/usr/share/applications"
mkdir -p "${DEB_DIR}/usr/share/icons/hicolor/512x512/apps"
mkdir -p "${DEB_DIR}/usr/share/icons/hicolor/scalable/apps"
mkdir -p "${DEB_DIR}/usr/share/polkit-1/actions"

cp "${ROOT_DIR}/build/linux/debian/control" "${DEB_DIR}/DEBIAN/"
sed -i "s/^Version:.*/Version: ${VERSION}/" "${DEB_DIR}/DEBIAN/control"
cp "${ROOT_DIR}/build/linux/debian/postinst" "${DEB_DIR}/DEBIAN/"
cp "${ROOT_DIR}/build/linux/debian/postrm" "${DEB_DIR}/DEBIAN/"
chmod 755 "${DEB_DIR}/DEBIAN"
chmod 755 "${DEB_DIR}/DEBIAN/postinst" "${DEB_DIR}/DEBIAN/postrm"

cp "${ROOT_DIR}/build/bin/neobox" "${DEB_DIR}/usr/bin/neobox"
chmod 755 "${DEB_DIR}/usr/bin/neobox"
cp "${ROOT_DIR}/build/linux/neobox.desktop" "${DEB_DIR}/usr/share/applications/"
chmod 644 "${DEB_DIR}/usr/share/applications/neobox.desktop"
cp "${ROOT_DIR}/build/linux/icon.png" "${DEB_DIR}/usr/share/icons/hicolor/512x512/apps/neobox.png"
chmod 644 "${DEB_DIR}/usr/share/icons/hicolor/512x512/apps/neobox.png"
cp "${ROOT_DIR}/build/neobox.svg" "${DEB_DIR}/usr/share/icons/hicolor/scalable/apps/neobox.svg"
chmod 644 "${DEB_DIR}/usr/share/icons/hicolor/scalable/apps/neobox.svg"
cp "${ROOT_DIR}/build/linux/app.neobox.policy" "${DEB_DIR}/usr/share/polkit-1/actions/"
chmod 644 "${DEB_DIR}/usr/share/polkit-1/actions/app.neobox.policy"

dpkg-deb --root-owner-group --build "${DEB_DIR}" "${ROOT_DIR}/build/bin/neobox_${VERSION}_amd64.deb"
rm -rf "${DEB_DIR}"

echo "=== Packaging .tar.gz (Generic Linux / Arch) ==="
TAR_DIR="$(mktemp -d /tmp/neobox-tar-XXXXXX)"
mkdir -p "${TAR_DIR}"
cp "${ROOT_DIR}/build/bin/neobox" "${TAR_DIR}/neobox"
chmod 755 "${TAR_DIR}/neobox"
cp "${ROOT_DIR}/build/linux/neobox.desktop" "${TAR_DIR}/neobox.desktop"
chmod 644 "${TAR_DIR}/neobox.desktop"
cp "${ROOT_DIR}/build/linux/icon.png" "${TAR_DIR}/icon.png"
chmod 644 "${TAR_DIR}/icon.png"
tar -czf "${ROOT_DIR}/build/bin/neobox-linux-amd64.tar.gz" -C "${TAR_DIR}" neobox neobox.desktop icon.png
rm -rf "${TAR_DIR}"

if [[ -n "${PRIVATE_KEY}" ]]; then
  echo "=== Signing release artifacts ==="
  go run "${ROOT_DIR}/cmd/sign/main.go" -key "${PRIVATE_KEY}" -file "${ROOT_DIR}/build/bin/neobox"
  cp -f "${ROOT_DIR}/build/bin/neobox.sig" "${ROOT_DIR}/build/bin/neobox-linux-amd64.sig"
  go run "${ROOT_DIR}/cmd/sign/main.go" -key "${PRIVATE_KEY}" -file "${ROOT_DIR}/build/bin/neobox_${VERSION}_amd64.deb"
  go run "${ROOT_DIR}/cmd/sign/main.go" -key "${PRIVATE_KEY}" -file "${ROOT_DIR}/build/bin/neobox-linux-amd64.tar.gz"
  if [ -f "${ROOT_DIR}/build/bin/neobox-${VERSION}-1-x86_64.pkg.tar.zst" ]; then
    go run "${ROOT_DIR}/cmd/sign/main.go" -key "${PRIVATE_KEY}" -file "${ROOT_DIR}/build/bin/neobox-${VERSION}-1-x86_64.pkg.tar.zst"
  fi
  echo "Signatures created (.sig files)."
else
  echo "NOTE: No private key provided. Skipping signing."
  echo "      Pass --key <hex_or_file> or set PRIVATE_KEY to sign release artifacts."
fi

# Clean up temporary build folders left in build/linux
rm -rf "${ROOT_DIR}/build/linux/pkg" "${ROOT_DIR}/build/linux/src"

echo "=== Linux Build Complete ==="
