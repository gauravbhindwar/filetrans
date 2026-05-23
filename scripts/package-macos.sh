#!/usr/bin/env bash
# Build macOS .dmg and .pkg installers.
# Must run on macOS (uses hdiutil and pkgbuild).
#
# Usage: bash scripts/package-macos.sh <version> <arch>
# Example: bash scripts/package-macos.sh 0.1.0 arm64
set -euo pipefail

VERSION="${1:?Usage: $0 <version> <arch>}"
ARCH="${2:?Usage: $0 <version> <arch>}"   # amd64 | arm64
BINARY="dist/filetrans_darwin_${ARCH}"
DIST_DIR="dist"

die()  { printf '\033[31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[34m==>\033[0m %s\n' "$*"; }

[ -f "$BINARY" ] || die "Binary not found: $BINARY  (build first with: make darwin)"
[ "$(uname -s)" = "Darwin" ] || die "This script must run on macOS"

# ── .dmg ─────────────────────────────────────────────────────────────────────
info "Creating DMG for darwin/${ARCH} ..."

DMG_STAGE="$(mktemp -d)"
trap "rm -rf ${DMG_STAGE}" EXIT

cp "$BINARY"    "${DMG_STAGE}/filetrans"
cp README.md    "${DMG_STAGE}/README.md"
cp LICENSE      "${DMG_STAGE}/LICENSE"

# Write a simple drag-to-install note for GUI users.
cat > "${DMG_STAGE}/Install.txt" <<'EOF'
filetrans is a command-line tool.

To install:
  1. Open Terminal
  2. Run: sudo cp filetrans /usr/local/bin/
  3. Run: chmod +x /usr/local/bin/filetrans

Or use the installer package (.pkg) from the same release.
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
trap "rm -rf ${PKG_ROOT}" EXIT

install -Dm755 "$BINARY" "${PKG_ROOT}/usr/local/bin/filetrans"

PKG_OUT="${DIST_DIR}/filetrans_darwin_${ARCH}_${VERSION}.pkg"
pkgbuild \
    --root "${PKG_ROOT}" \
    --identifier "io.filetrans.filetrans" \
    --version "${VERSION}" \
    --install-location "/" \
    "${PKG_OUT}"

info "PKG → ${PKG_OUT}"
printf '\033[32m OK \033[0m macOS packages ready in %s/\n' "${DIST_DIR}"
