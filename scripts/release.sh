#!/bin/bash
#
# Deployado CLI Release Script
#
# Creates a git tag and pushes it to trigger the GitHub Actions release workflow.
#
# Usage:
#   ./scripts/release.sh 0.1.0        # Release v0.1.0
#   ./scripts/release.sh 0.2.0-beta   # Pre-release
#   ./scripts/release.sh              # Prompts for version
#

set -e

# ── Colors ────────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
NC='\033[0m'

info()    { printf "${BLUE}==>${NC} ${BOLD}%s${NC}\n" "$1"; }
success() { printf "${GREEN}==>${NC} ${BOLD}%s${NC}\n" "$1"; }
warn()    { printf "${YELLOW}warning:${NC} %s\n" "$1"; }
error()   { printf "${RED}error:${NC} %s\n" "$1" >&2; exit 1; }

# ── Navigate to repo root ────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CLI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

# ── Preflight checks ─────────────────────────────────────────────────────────

# Must be in a git repo
if ! git rev-parse --is-inside-work-tree > /dev/null 2>&1; then
    error "Not inside a git repository."
fi

# Check for uncommitted changes
if ! git diff --quiet HEAD -- 2>/dev/null; then
    echo ""
    warn "You have uncommitted changes:"
    git --no-pager diff --stat HEAD
    echo ""
    read -p "Continue anyway? (y/N) " -n 1 -r
    echo
    [[ $REPLY =~ ^[Yy]$ ]] || exit 1
fi

# ── Resolve version ──────────────────────────────────────────────────────────

VERSION="${1}"

if [ -z "$VERSION" ]; then
    # Show existing CLI tags for reference
    EXISTING_TAGS=$(git tag -l 'cli-v*' --sort=-version:refname 2>/dev/null | head -5)
    if [ -n "$EXISTING_TAGS" ]; then
        echo ""
        info "Recent CLI releases:"
        echo "$EXISTING_TAGS" | sed 's/^/    /'
        echo ""
    fi

    read -p "Version to release (e.g. 0.1.0): " VERSION
    [ -z "$VERSION" ] && error "No version provided."
fi

# Strip leading 'v' or 'cli-v' if the user included it
VERSION="${VERSION#cli-v}"
VERSION="${VERSION#v}"

TAG="cli-v${VERSION}"

# ── Validate version format ──────────────────────────────────────────────────

if ! echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$'; then
    error "Invalid version format: '${VERSION}'. Expected semver like 0.1.0 or 1.0.0-beta.1"
fi

# Check if tag already exists
if git rev-parse "$TAG" > /dev/null 2>&1; then
    error "Tag '${TAG}' already exists. Choose a different version."
fi

# ── Verify build compiles ────────────────────────────────────────────────────

info "Verifying CLI compiles..."
cd "$CLI_DIR"
if go build -o /dev/null ./cmd/deployado 2>&1; then
    success "Build OK"
else
    error "Build failed. Fix compilation errors before releasing."
fi
cd "$REPO_ROOT"

# ── Confirm ──────────────────────────────────────────────────────────────────

COMMIT=$(git rev-parse --short HEAD)
BRANCH=$(git rev-parse --abbrev-ref HEAD)

echo ""
echo "┌─────────────────────────────────────────┐"
echo "│          Deployado CLI Release           │"
echo "├─────────────────────────────────────────┤"
printf "│  Version:  ${GREEN}%-28s${NC} │\n" "$VERSION"
printf "│  Tag:      ${GREEN}%-28s${NC} │\n" "$TAG"
printf "│  Commit:   %-28s │\n" "$COMMIT"
printf "│  Branch:   %-28s │\n" "$BRANCH"
echo "├─────────────────────────────────────────┤"
echo "│  Targets:                                │"
echo "│    • darwin/amd64  (Mac Intel)           │"
echo "│    • darwin/arm64  (Mac Apple Silicon)   │"
echo "│    • linux/amd64   (Linux x86_64)        │"
echo "│    • linux/arm64   (Linux ARM)           │"
echo "└─────────────────────────────────────────┘"
echo ""

if [[ "$BRANCH" != "main" && "$BRANCH" != "master" ]]; then
    warn "You are releasing from branch '${BRANCH}', not main."
    echo ""
fi

read -p "Create tag and push to trigger release? (y/N) " -n 1 -r
echo
[[ $REPLY =~ ^[Yy]$ ]] || { echo "Aborted."; exit 0; }

# ── Create and push tag ──────────────────────────────────────────────────────

info "Creating tag ${TAG}..."
git tag -a "$TAG" -m "Release Deployado CLI v${VERSION}"

info "Pushing tag to origin..."
git push origin "$TAG"

# ── Done ──────────────────────────────────────────────────────────────────────

echo ""
success "Tag ${TAG} pushed!"
echo ""
echo "  GitHub Actions will now:"
echo "    1. Cross-compile for macOS & Linux (amd64 + arm64)"
echo "    2. Upload binaries to deployado.com"
echo "    3. Create a GitHub Release"
echo ""
echo "  Watch the progress:"
printf "    ${BOLD}https://github.com/rbaezam/deployado/actions${NC}\n"
echo ""
echo "  Once complete, users can install with:"
printf "    ${BOLD}curl -sSfL https://deployado.com/cli/install.sh | sh${NC}\n"
echo ""
