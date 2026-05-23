#!/usr/bin/env bash
# Build macOS .dmg and .pkg installers.
# Usage: bash scripts/package-macos.sh <version> <arch>
set -euo pipefail

VERSION="${1:?Usage: $0 <version> <arch>}"
ARCH="${2:?Usage: $0 <version> <arch>}"
BINARY="dist/filetrans_darwin_${ARCH}"
DIST_DIR="dist"

die()  { printf '\033[31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[34m==>\033[0m %s\n' "$*"; }

[ -f "$BINARY" ] || die "Binary not found: $BINARY"
[ "$(uname -s)" = "Darwin" ] || die "Must run on macOS"

# ── .dmg ─────────────────────────────────────────────────────────────────────
info "Creating DMG for darwin/${ARCH} ..."

DMG_STAGE="$(mktemp -d)"
cleanup() { rm -rf "${DMG_STAGE}" "${PKG_ROOT:-}" 2>/dev/null || true; }
trap cleanup EXIT

cp "$BINARY"  "${DMG_STAGE}/filetrans"
chmod +x      "${DMG_STAGE}/filetrans"
cp README.md  "${DMG_STAGE}/README.md"  2>/dev/null || true
cp LICENSE    "${DMG_STAGE}/LICENSE"    2>/dev/null || true

cat > "${DMG_STAGE}/Install.txt" <<'EOF'
filetrans — USB-C & LAN direct file transfer

Install:
  sudo cp filetrans /usr/local/bin/
  chmod +x /usr/local/bin/filetrans

Or use the .pkg installer from the same release.
EOF

DMG_OUT="${DIST_DIR}/filetrans_darwin_${ARCH}_${VERSION}.dmg"
hdiutil create \
    -volname "filetrans ${VERSION}" \
    -srcfolder "${DMG_STAGE}" \
    -ov -format UDZO \
    "${DMG_OUT}"
info "DMG → ${DMG_OUT}"

# ── .pkg ─────────────────────────────────────────────────────────────────────
info "Creating PKG installer for darwin/${ARCH} ..."

PKG_ROOT="$(mktemp -d)"

# pkgbuild needs the tree rooted at install-location (/usr/local/bin).
mkdir -p "${PKG_ROOT}/usr/local/bin"
cp "$BINARY" "${PKG_ROOT}/usr/local/bin/filetrans"
chmod +x     "${PKG_ROOT}/usr/local/bin/filetrans"

PKG_OUT="${DIST_DIR}/filetrans_darwin_${ARCH}_${VERSION}.pkg"
pkgbuild \
    --root            "${PKG_ROOT}" \
    --identifier      "io.filetrans.cli" \
    --version         "${VERSION}" \
    --install-location "/" \
    "${PKG_OUT}"

info "PKG → ${PKG_OUT}"
printf '\033[32m OK \033[0m macOS packages ready in %s/\n' "${DIST_DIR}"
