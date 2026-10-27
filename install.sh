#!/bin/bash
#
# Deployado CLI Installer
#
# Usage:
#   ./install.sh          # Install to /usr/local/bin (requires sudo)
#   ./install.sh --local  # Install to ~/go/bin (no sudo needed)
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY_NAME="deployado"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Deployado CLI Installer${NC}"
echo "========================"
echo

# Check for Go
if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: Go is not installed.${NC}"
    echo "Install Go from https://go.dev/dl/"
    exit 1
fi

GO_VERSION=$(go version | awk '{print $3}')
echo -e "Go version: ${GREEN}${GO_VERSION}${NC}"

# Determine install directory
if [[ "$1" == "--local" ]]; then
    INSTALL_DIR="$HOME/go/bin"
    USE_SUDO=""
    mkdir -p "$INSTALL_DIR"
else
    INSTALL_DIR="/usr/local/bin"
    USE_SUDO="sudo"
fi

echo -e "Install directory: ${GREEN}${INSTALL_DIR}${NC}"
echo

# Build
echo "Building..."
cd "$SCRIPT_DIR"

# Get dependencies
go mod tidy

# Build with version info
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

go build -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o "$BINARY_NAME" ./cmd/deployado

echo -e "${GREEN}Build successful!${NC}"
echo

# Install
echo "Installing to ${INSTALL_DIR}..."
$USE_SUDO mv "$BINARY_NAME" "$INSTALL_DIR/"
$USE_SUDO chmod +x "$INSTALL_DIR/$BINARY_NAME"

echo -e "${GREEN}Installation complete!${NC}"
echo

# Verify
if command -v deployado &> /dev/null; then
    echo "Installed version:"
    deployado version
else
    echo -e "${YELLOW}Warning: 'deployado' not found in PATH.${NC}"
    if [[ "$1" == "--local" ]]; then
        echo "Add this to your ~/.zshrc or ~/.bashrc:"
        echo "  export PATH=\"\$HOME/go/bin:\$PATH\""
    fi
fi

echo
echo -e "${GREEN}Done!${NC} Run 'deployado --help' to get started."
