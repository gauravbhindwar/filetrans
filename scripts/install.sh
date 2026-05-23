#!/usr/bin/env sh
# filetrans universal installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/gauravbhindwar/filetrans/main/scripts/install.sh | sh
#   or
#   sh install.sh [version]   e.g. sh install.sh v0.2.0
#
# Installs the correct binary for the current OS/arch to /usr/local/bin.
# Requires: curl or wget, sha256sum (or shasum on macOS).
# Requires root (or sudo) to write to /usr/local/bin.

set -e

REPO="gauravbhindwar/filetrans"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY="filetrans"

# ── helpers ──────────────────────────────────────────────────────────────────

die() { printf '\033[31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[34m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[32m OK \033[0m %s\n' "$*"; }

need() {
    command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"
}

download() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$2" "$1"
    else
        die "Neither curl nor wget found. Install one and retry."
    fi
}

# ── detect OS + arch ─────────────────────────────────────────────────────────

detect_os() {
    OS="$(uname -s)"
    case "$OS" in
        Linux*)  OS=linux   ;;
        Darwin*) OS=darwin  ;;
        MINGW*|MSYS*|CYGWIN*) OS=windows ;;
        *) die "Unsupported OS: $OS" ;;
    esac
}

detect_arch() {
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)   ARCH=amd64  ;;
        aarch64|arm64)  ARCH=arm64  ;;
        armv7l)         ARCH=arm    ;;
        *) die "Unsupported architecture: $ARCH" ;;
    esac
}

# ── resolve version ───────────────────────────────────────────────────────────

resolve_version() {
    if [ -n "$1" ]; then
        VERSION="$1"
        return
    fi
    info "Fetching latest release..."
    # Method 1: follow the /releases/latest HTML redirect — works without auth,
    # not subject to API rate limits, reliable even seconds after a tag push.
    if command -v curl >/dev/null 2>&1; then
        VERSION="$(curl -fsSLI "https://github.com/${REPO}/releases/latest" \
            | grep -i '^location:' \
            | sed 's|.*/tag/||' \
            | tr -d '\r\n')"
    elif command -v wget >/dev/null 2>&1; then
        VERSION="$(wget -q --server-response --spider \
            "https://github.com/${REPO}/releases/latest" 2>&1 \
            | grep -i 'Location:' | tail -1 \
            | sed 's|.*/tag/||' | tr -d '\r\n')"
    fi
    # Method 2: fallback to JSON API (slower, rate-limited, but works if redirect fails)
    if [ -z "$VERSION" ]; then
        if command -v curl >/dev/null 2>&1; then
            VERSION="$(curl -sL "https://api.github.com/repos/${REPO}/releases/latest" \
                | grep '"tag_name"' | head -1 \
                | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
        fi
    fi
    [ -n "$VERSION" ] || die "Could not determine latest version. Pass version manually: sh install.sh v0.2.2"
    info "Version: $VERSION"
}

# ── main ──────────────────────────────────────────────────────────────────────

detect_os
detect_arch
resolve_version "$1"

# Construct asset name
if [ "$OS" = "windows" ]; then
    ASSET="filetrans_${OS}_${ARCH}.exe"
    DEST="${INSTALL_DIR}/filetrans.exe"
else
    ASSET="filetrans_${OS}_${ARCH}"
    DEST="${INSTALL_DIR}/filetrans"
fi

BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
ASSET_URL="${BASE_URL}/${ASSET}"
CHECKSUMS_URL="${BASE_URL}/checksums.txt"

info "Downloading $ASSET ..."
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

download "$ASSET_URL"    "$TMP_DIR/$ASSET"
download "$CHECKSUMS_URL" "$TMP_DIR/checksums.txt"

# ── verify checksum ───────────────────────────────────────────────────────────
info "Verifying checksum..."
cd "$TMP_DIR"
if command -v sha256sum >/dev/null 2>&1; then
    grep "$ASSET" checksums.txt | sha256sum -c -
elif command -v shasum >/dev/null 2>&1; then
    grep "$ASSET" checksums.txt | shasum -a 256 -c -
else
    printf '\033[33mWARN:\033[0m sha256sum not found — skipping checksum verification\n'
fi
cd - >/dev/null

# ── install ───────────────────────────────────────────────────────────────────
info "Installing to $DEST ..."
chmod +x "$TMP_DIR/$ASSET"

if [ -w "$INSTALL_DIR" ]; then
    cp "$TMP_DIR/$ASSET" "$DEST"
elif command -v sudo >/dev/null 2>&1; then
    sudo cp "$TMP_DIR/$ASSET" "$DEST"
else
    die "Cannot write to $INSTALL_DIR. Re-run as root or set INSTALL_DIR to a writable path."
fi

ok "filetrans $VERSION installed → $DEST"
printf '\nQuick start:\n'
printf '  Linux (sender):  sudo filetrans --role=sender myfile.zip\n'
printf '  Windows (recv):  filetrans --role=receiver\n'
printf '  Help:            filetrans --help\n\n'
