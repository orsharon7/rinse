#!/usr/bin/env bash
# install.sh — one-line installer for the rinse CLI
#
# Usage:
#   bash install.sh
#   INSTALL_DIR=/usr/local/bin bash install.sh
#
# What it does:
#   1. Detects OS + arch
#   2. If a pre-built binary is available in dist/, installs it
#   3. Otherwise falls back to `go build` (requires Go ≥ 1.24)
#   4. Installs runner scripts to <INSTALL_DIR>/scripts/ so `rinse` finds them
#
set -euo pipefail

BINARY="rinse"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
SCRIPTS_INSTALL_DIR="$INSTALL_DIR/scripts"

# ── Detect platform ────────────────────────────────────────────────────────────
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "error: unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

case "$OS" in
  darwin|linux) ;;
  *)
    echo "error: unsupported OS: $OS" >&2
    exit 1
    ;;
esac

DIST_BINARY="$SCRIPT_DIR/dist/${BINARY}-${OS}-${ARCH}"

# ── Install binary ────────────────────────────────────────────────────────────
mkdir -p "$INSTALL_DIR"

if [[ -f "$DIST_BINARY" ]]; then
  echo "Installing pre-built binary for ${OS}/${ARCH}…"
  install -m 755 "$DIST_BINARY" "$INSTALL_DIR/$BINARY"
else
  echo "No pre-built binary found for ${OS}/${ARCH} — building from source…"
  if ! command -v go >/dev/null 2>&1; then
    echo "error: Go is not installed. Install it from https://go.dev/dl/ and retry." >&2
    exit 1
  fi
  VERSION="$(git -C "$SCRIPT_DIR" describe --tags --always --dirty 2>/dev/null || echo "dev")"
  TMP_BINARY="$(mktemp -t rinse-build.XXXXXX)"
  trap 'rm -f "$TMP_BINARY"' EXIT
  (cd "$SCRIPT_DIR" && go build -ldflags "-X main.version=$VERSION" -o "$TMP_BINARY" .)
  install -m 755 "$TMP_BINARY" "$INSTALL_DIR/$BINARY"
fi

echo "Installed → $INSTALL_DIR/$BINARY"

# ── Install runner scripts ────────────────────────────────────────────────────
# Copy the scripts/*.sh helpers next to the binary so `rinse` can locate them
# automatically (binDir/scripts/ is part of the script-resolution search path).
if [[ -d "$SCRIPT_DIR/scripts" ]]; then
  mkdir -p "$SCRIPTS_INSTALL_DIR"
  installed_any_scripts=false
  for script in "$SCRIPT_DIR/scripts/"*.sh; do
    [[ -e "$script" ]] || continue
    if [[ "$(basename "$script")" == "pr-review-launch.sh" ]]; then
      continue
    fi
    cp "$script" "$SCRIPTS_INSTALL_DIR/"
    chmod +x "$SCRIPTS_INSTALL_DIR/$(basename "$script")"
    installed_any_scripts=true
  done
  if [[ "$installed_any_scripts" == true ]]; then
    echo "Scripts   → $SCRIPTS_INSTALL_DIR/"
  fi
fi

# ── Shell PATH hint ───────────────────────────────────────────────────────────
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  echo ""
  echo "  Note: $INSTALL_DIR is not in your PATH."
  echo "  Add this to your shell profile (~/.zshrc or ~/.bashrc):"
  echo "    export PATH=\"\$PATH:$INSTALL_DIR\""
fi

echo ""
echo "Done! Run:  rinse"
