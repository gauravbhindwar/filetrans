#!/usr/bin/env bash
# Install filetrans on Arch Linux (and Arch-based: Manjaro, EndeavourOS, Garuda, etc.)
#
# Usage:
#   bash install-arch.sh [version]
#
# Method 1 (default): downloads the pre-built binary directly — fastest
# Method 2 (--pkgbuild): builds from the PKGBUILD — creates a proper Arch package
#
# Examples:
#   bash install-arch.sh                # install latest, binary method
#   bash install-arch.sh v0.2.0         # specific version, binary method
#   bash install-arch.sh --pkgbuild     # build Arch package from PKGBUILD

set -euo pipefail

REPO="gauravbhindwar/filetrans"
INSTALL_DIR="/usr/local/bin"
USE_PKGBUILD=false
VERSION=""

# ── parse args ────────────────────────────────────────────────────────────────
for arg in "$@"; do
    case "$arg" in
        --pkgbuild) USE_PKGBUILD=true ;;
        v*) VERSION="$arg" ;;
    esac
done

die()  { printf '\033[31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[34m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[32m OK \033[0m %s\n' "$*"; }

# ── method 1: direct binary install ──────────────────────────────────────────
install_binary() {
    local arch
    case "$(uname -m)" in
        x86_64)          arch="amd64" ;;
        aarch64|arm64)   arch="arm64" ;;
        *) die "Unsupported arch: $(uname -m)" ;;
    esac

    if [ -z "$VERSION" ]; then
        info "Resolving latest version..."
        # Method 1: HTML redirect (no auth, no rate limits)
        VERSION="$(curl -fsSLI "https://github.com/${REPO}/releases/latest" \
            | grep -i '^location:' \
            | sed 's|.*/tag/||' \
            | tr -d '\r\n')"
        # Method 2: JSON API fallback
        if [ -z "$VERSION" ]; then
            VERSION="$(curl -sL "https://api.github.com/repos/${REPO}/releases/latest" \
                | grep '"tag_name"' | sed 's/.*"\(v[^"]*\)".*/\1/')"
        fi
        [ -n "$VERSION" ] || { printf '\033[31mERROR:\033[0m Could not determine latest version. Pass manually: bash install-arch.sh v0.2.2\n' >&2; exit 1; }
    fi

    local base_url="https://github.com/${REPO}/releases/download/${VERSION}"
    info "Downloading filetrans ${VERSION} for linux/${arch} ..."
    curl -fsSL "${base_url}/filetrans_linux_${arch}"     -o /tmp/filetrans
    curl -fsSL "${base_url}/filetrans-gui_linux_${arch}" -o /tmp/filetrans-gui
    curl -fsSL "${base_url}/checksums.txt"               -o /tmp/filetrans_checksums.txt

    info "Verifying checksums..."
    grep "filetrans_linux_${arch}$"     /tmp/filetrans_checksums.txt | sha256sum -c - || die "CLI checksum failed"
    grep "filetrans-gui_linux_${arch}$" /tmp/filetrans_checksums.txt | sha256sum -c - || die "GUI checksum failed"

    sudo install -Dm755 /tmp/filetrans     "${INSTALL_DIR}/filetrans"
    sudo install -Dm755 /tmp/filetrans-gui "${INSTALL_DIR}/filetrans-gui"
    rm -f /tmp/filetrans /tmp/filetrans-gui /tmp/filetrans_checksums.txt

    ok "filetrans ${VERSION} installed → ${INSTALL_DIR}/filetrans"
    ok "filetrans-gui ${VERSION} installed → ${INSTALL_DIR}/filetrans-gui"
}

# ── method 2: build from PKGBUILD ─────────────────────────────────────────────
install_pkgbuild() {
    command -v makepkg >/dev/null 2>&1 || die "makepkg not found — not running on Arch Linux?"

    # Locate PKGBUILD relative to this script, or download it.
    local pkgbuild_dir
    pkgbuild_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/packaging"

    if [ ! -f "${pkgbuild_dir}/PKGBUILD" ]; then
        info "Downloading PKGBUILD..."
        pkgbuild_dir="$(mktemp -d)"
        trap "rm -rf ${pkgbuild_dir}" EXIT
        curl -fsSL \
            "https://raw.githubusercontent.com/${REPO}/main/packaging/PKGBUILD" \
            -o "${pkgbuild_dir}/PKGBUILD"
    fi

    info "Building Arch package from PKGBUILD..."
    cd "$pkgbuild_dir"
    makepkg -si --noconfirm

    ok "filetrans installed via pacman."
}

# ── aur helper install ────────────────────────────────────────────────────────
install_via_aur() {
    info "Attempting AUR helper installation..."
    if command -v yay >/dev/null 2>&1; then
        yay -S --noconfirm filetrans
    elif command -v paru >/dev/null 2>&1; then
        paru -S --noconfirm filetrans
    elif command -v trizen >/dev/null 2>&1; then
        trizen -S --noconfirm filetrans
    else
        info "No AUR helper found. Falling back to PKGBUILD method."
        install_pkgbuild
    fi
}

# ── entry ────────────────────────────────────────────────────────────────────
if $USE_PKGBUILD; then
    install_pkgbuild
elif command -v yay >/dev/null 2>&1 || command -v paru >/dev/null 2>&1; then
    install_via_aur
else
    install_binary
fi

printf '\nQuick start:\n'
printf '  GUI (recommended): filetrans-gui\n'
printf '    Opens browser at http://localhost:7071\n'
printf '  CLI sender:        sudo filetrans --role=sender myfile.zip\n'
printf '  CLI receiver:      filetrans --role=receiver\n\n'
