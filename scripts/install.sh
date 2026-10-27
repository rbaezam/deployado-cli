#!/bin/sh
#
# Deployado CLI Installer
#
# Detects OS and architecture, downloads the correct binary from the Deployado
# control plane, and installs it to the system PATH.
#
# Usage:
#   curl -sSfL https://deployado.com/cli/install.sh | sh
#   curl -sSfL https://deployado.com/cli/install.sh | sh -s -- --version 0.2.0
#   curl -sSfL https://deployado.com/cli/install.sh | sh -s -- --dir ~/.local/bin
#
# Options:
#   --version VERSION   Install a specific version (default: latest)
#   --dir DIRECTORY      Install to a custom directory (default: /usr/local/bin)
#   --help               Show this help message
#

set -e

# ── Configuration ─────────────────────────────────────────────────────────────

BASE_URL="https://deployado.com"
BINARY_NAME="deployado"
DEFAULT_INSTALL_DIR="/usr/local/bin"

# ── Colors (disabled when not a terminal) ─────────────────────────────────────

if [ -t 1 ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    BLUE='\033[0;34m'
    BOLD='\033[1m'
    NC='\033[0m'
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    BOLD=''
    NC=''
fi

# ── Helpers ───────────────────────────────────────────────────────────────────

info() {
    printf "${BLUE}==>${NC} ${BOLD}%s${NC}\n" "$1"
}

success() {
    printf "${GREEN}==>${NC} ${BOLD}%s${NC}\n" "$1"
}

warn() {
    printf "${YELLOW}warning:${NC} %s\n" "$1"
}

error() {
    printf "${RED}error:${NC} %s\n" "$1" >&2
    exit 1
}

# ── Parse arguments ───────────────────────────────────────────────────────────

VERSION=""
INSTALL_DIR=""

while [ $# -gt 0 ]; do
    case "$1" in
        --version)
            VERSION="$2"
            shift 2
            ;;
        --dir)
            INSTALL_DIR="$2"
            shift 2
            ;;
        --help)
            echo "Deployado CLI Installer"
            echo ""
            echo "Usage:"
            echo "  curl -sSfL https://deployado.com/cli/install.sh | sh"
            echo "  curl -sSfL https://deployado.com/cli/install.sh | sh -s -- [options]"
            echo ""
            echo "Options:"
            echo "  --version VERSION   Install a specific version (default: latest)"
            echo "  --dir DIRECTORY     Install to a custom directory (default: /usr/local/bin)"
            echo "  --help              Show this help message"
            exit 0
            ;;
        *)
            error "Unknown option: $1. Use --help for usage."
            ;;
    esac
done

# ── Detect OS ─────────────────────────────────────────────────────────────────

detect_os() {
    OS="$(uname -s)"
    case "$OS" in
        Darwin)  echo "darwin" ;;
        Linux)   echo "linux" ;;
        *)       error "Unsupported operating system: $OS. Deployado CLI supports macOS and Linux." ;;
    esac
}

# ── Detect architecture ──────────────────────────────────────────────────────

detect_arch() {
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)    echo "amd64" ;;
        aarch64|arm64)   echo "arm64" ;;
        *)               error "Unsupported architecture: $ARCH. Deployado CLI supports amd64 and arm64." ;;
    esac
}

# ── Find a download tool ─────────────────────────────────────────────────────

detect_downloader() {
    if command -v curl > /dev/null 2>&1; then
        echo "curl"
    elif command -v wget > /dev/null 2>&1; then
        echo "wget"
    else
        error "Neither 'curl' nor 'wget' found. Please install one of them and try again."
    fi
}

download() {
    url="$1"
    output="$2"
    downloader="$(detect_downloader)"

    case "$downloader" in
        curl)
            curl -sSfL -o "$output" "$url"
            ;;
        wget)
            wget -q -O "$output" "$url"
            ;;
    esac
}

download_to_stdout() {
    url="$1"
    downloader="$(detect_downloader)"

    case "$downloader" in
        curl)
            curl -sSfL "$url"
            ;;
        wget)
            wget -q -O - "$url"
            ;;
    esac
}

# ── Resolve latest version ───────────────────────────────────────────────────

get_latest_version() {
    latest_url="${BASE_URL}/downloads/cli/latest"

    latest_version="$(download_to_stdout "$latest_url" 2>/dev/null)" || error "Failed to fetch latest version from ${latest_url}. Check your internet connection."

    # Trim whitespace
    latest_version="$(echo "$latest_version" | tr -d '[:space:]')"

    if [ -z "$latest_version" ]; then
        error "Could not determine the latest CLI version. Please specify one with --version."
    fi

    echo "$latest_version"
}

# ── Verify checksum ──────────────────────────────────────────────────────────

verify_checksum() {
    archive_path="$1"
    expected_checksum="$2"

    if command -v sha256sum > /dev/null 2>&1; then
        actual_checksum="$(sha256sum "$archive_path" | awk '{print $1}')"
    elif command -v shasum > /dev/null 2>&1; then
        actual_checksum="$(shasum -a 256 "$archive_path" | awk '{print $1}')"
    else
        warn "Neither sha256sum nor shasum found. Skipping checksum verification."
        return 0
    fi

    if [ "$actual_checksum" != "$expected_checksum" ]; then
        error "Checksum verification failed!\n  Expected: $expected_checksum\n  Actual:   $actual_checksum\n\nThe downloaded file may be corrupted. Please try again."
    fi
}

# ── Main ──────────────────────────────────────────────────────────────────────

main() {
    echo ""
    printf "${GREEN}  ____             _                       _       ${NC}\n"
    printf "${GREEN} |  _ \\  ___ _ __ | | ___  _   _  __ _  __| | ___  ${NC}\n"
    printf "${GREEN} | | | |/ _ \\ '_ \\| |/ _ \\| | | |/ _\` |/ _\` |/ _ \\ ${NC}\n"
    printf "${GREEN} | |_| |  __/ |_) | | (_) | |_| | (_| | (_| | (_) |${NC}\n"
    printf "${GREEN} |____/ \\___| .__/|_|\\___/ \\__, |\\__,_|\\__,_|\\___/ ${NC}\n"
    printf "${GREEN}            |_|            |___/           CLI      ${NC}\n"
    echo ""

    # Detect platform
    OS="$(detect_os)"
    ARCH="$(detect_arch)"
    info "Detected platform: ${OS}/${ARCH}"

    # Resolve version
    if [ -z "$VERSION" ]; then
        info "Fetching latest version..."
        VERSION="$(get_latest_version)"
    fi
    info "Installing Deployado CLI v${VERSION}"

    # Resolve install directory
    if [ -z "$INSTALL_DIR" ]; then
        # If /usr/local/bin is not writable, fall back to ~/.local/bin
        if [ -w "$DEFAULT_INSTALL_DIR" ] || [ "$(id -u)" = "0" ]; then
            INSTALL_DIR="$DEFAULT_INSTALL_DIR"
        else
            INSTALL_DIR="${HOME}/.local/bin"
            warn "/usr/local/bin is not writable. Installing to ${INSTALL_DIR} instead."
            warn "Make sure ${INSTALL_DIR} is in your PATH."
        fi
    fi

    # Create install directory if needed
    mkdir -p "$INSTALL_DIR"

    # Build download URL
    ARCHIVE_NAME="${BINARY_NAME}_${VERSION}_${OS}_${ARCH}.tar.gz"
    DOWNLOAD_URL="${BASE_URL}/downloads/cli/${VERSION}/${ARCHIVE_NAME}"
    CHECKSUMS_URL="${BASE_URL}/downloads/cli/${VERSION}/checksums.txt"

    # Create temp directory
    TMP_DIR="$(mktemp -d)"
    trap 'rm -rf "$TMP_DIR"' EXIT

    # Download archive
    info "Downloading ${ARCHIVE_NAME}..."
    download "$DOWNLOAD_URL" "${TMP_DIR}/${ARCHIVE_NAME}" || error "Download failed. Version v${VERSION} may not exist for ${OS}/${ARCH}.\nCheck available versions at: ${BASE_URL}/en/docs/cli"

    # Download and verify checksum
    info "Verifying checksum..."
    if download "${CHECKSUMS_URL}" "${TMP_DIR}/checksums.txt" 2>/dev/null; then
        EXPECTED_SUM="$(grep "${ARCHIVE_NAME}" "${TMP_DIR}/checksums.txt" | awk '{print $1}')"
        if [ -n "$EXPECTED_SUM" ]; then
            verify_checksum "${TMP_DIR}/${ARCHIVE_NAME}" "$EXPECTED_SUM"
            success "Checksum verified"
        else
            warn "Could not find checksum for ${ARCHIVE_NAME}. Skipping verification."
        fi
    else
        warn "Could not download checksums. Skipping verification."
    fi

    # Extract
    info "Extracting..."
    tar -xzf "${TMP_DIR}/${ARCHIVE_NAME}" -C "$TMP_DIR"

    # Install
    if [ -w "$INSTALL_DIR" ]; then
        mv "${TMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
        chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
    else
        info "Elevated permissions required to install to ${INSTALL_DIR}"
        sudo mv "${TMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
        sudo chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
    fi

    # Verify installation
    echo ""
    success "Deployado CLI v${VERSION} installed to ${INSTALL_DIR}/${BINARY_NAME}"
    echo ""

    # Check if the binary is in PATH
    if command -v "$BINARY_NAME" > /dev/null 2>&1; then
        printf "  ${BOLD}Installed version:${NC}\n"
        printf "  "
        "$BINARY_NAME" version 2>/dev/null || true
    else
        echo ""
        warn "'${BINARY_NAME}' is not in your PATH."
        echo ""
        echo "  Add the install directory to your PATH by adding this line to your shell config:"
        echo ""
        case "$(basename "${SHELL:-/bin/sh}")" in
            zsh)
                echo "    echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ~/.zshrc"
                echo "    source ~/.zshrc"
                ;;
            bash)
                echo "    echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ~/.bashrc"
                echo "    source ~/.bashrc"
                ;;
            fish)
                echo "    fish_add_path ${INSTALL_DIR}"
                ;;
            *)
                echo "    export PATH=\"${INSTALL_DIR}:\$PATH\""
                ;;
        esac
    fi

    echo ""
    echo "  Get started:"
    echo ""
    printf "    ${BOLD}deployado auth login${NC}    Log in to your Deployado account\n"
    printf "    ${BOLD}deployado launch${NC}        Deploy your first app\n"
    printf "    ${BOLD}deployado --help${NC}        Show all available commands\n"
    echo ""
}

main
