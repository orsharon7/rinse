#!/usr/bin/env bash
# install.sh — one-line installer for rinse TUI
#
# Usage:
#   bash install.sh
#   INSTALL_DIR=/usr/local/bin bash install.sh
#
# What it does:
#   1. Detects OS + arch
#   2. If a pre-built binary is available in dist/, installs it
#   3. Otherwise falls back to go build (requires Go ≥ 1.24)
#
set -euo pipefail

BINARY="rinse"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# ── Detect platform ────────────────────────────────────────────────────────────
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64)   ARCH="amd64" ;;
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

# ── Install ───────────────────────────────────────────────────────────────────
mkdir -p "$INSTALL_DIR"

if [[ -f "$DIST_BINARY" ]]; then
  echo "Installing pre-built binary for ${OS}/${ARCH}…"
  install -m 755 "$DIST_BINARY" "$INSTALL_DIR/$BINARY"
else
  echo "No pre-built binary found for ${OS}/${ARCH} — building from source…"
  if ! command -v go &>/dev/null; then
    echo "error: Go is not installed. Install it from https://go.dev/dl/ and retry." >&2
    exit 1
  fi
  VERSION="$(git -C "$SCRIPT_DIR" describe --tags --always --dirty 2>/dev/null || echo "dev")"
  (cd "$SCRIPT_DIR" && go build -ldflags "-X main.version=$VERSION" -o "$INSTALL_DIR/$BINARY" .)
fi

echo "Installed → $INSTALL_DIR/$BINARY"

# ── Install runner scripts alongside the binary ───────────────────────────────
# Copy the scripts/ helper scripts into $INSTALL_DIR/rinse-scripts/ so the
# installed command works regardless of where this repo lives or whether it is
# deleted after installation. The binary discovers them via RINSE_SCRIPT_DIR.
RINSE_SCRIPT_INSTALL_DIR="$INSTALL_DIR/rinse-scripts"
if [[ -d "$SCRIPT_DIR/scripts" ]]; then
  mkdir -p "$RINSE_SCRIPT_INSTALL_DIR"
  cp "$SCRIPT_DIR/scripts/"*.sh "$RINSE_SCRIPT_INSTALL_DIR/"
  chmod +x "$RINSE_SCRIPT_INSTALL_DIR/"*.sh
  echo "Scripts    → $RINSE_SCRIPT_INSTALL_DIR/"
fi

# ── Shell hint ────────────────────────────────────────────────────────────────
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  echo ""
  echo "  Note: $INSTALL_DIR is not in your PATH."
  echo "  Add this to your shell profile:"
  echo "    export PATH=\"\$PATH:$INSTALL_DIR\""
fi

# ── Wrapper script hint ───────────────────────────────────────────────────────
WRAPPER="$INSTALL_DIR/rinse-launch"
if [[ -d "$RINSE_SCRIPT_INSTALL_DIR" && ! -f "$WRAPPER" ]]; then
  cat > "$WRAPPER" <<WRAPPER_EOF
#!/usr/bin/env bash
export RINSE_SCRIPT_DIR="$RINSE_SCRIPT_INSTALL_DIR"
exec "$INSTALL_DIR/$BINARY" "\$@"
WRAPPER_EOF
  chmod +x "$WRAPPER"
  echo "Wrapper    → $WRAPPER"
fi

echo ""
echo "Done! Run:  rinse"
