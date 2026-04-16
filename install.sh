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

DIST_BINARY="$SCRIPT_DIR/tui/dist/${BINARY}-${OS}-${ARCH}"

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
  (cd "$SCRIPT_DIR/tui" && go build -ldflags "-X main.version=$VERSION" -o "$INSTALL_DIR/$BINARY" .)
fi

echo "Installed → $INSTALL_DIR/$BINARY"

# ── Install pr-review scripts alongside the binary ────────────────────────────
# Copy the pr-review/ helper scripts into $INSTALL_DIR/pr-review/ so the
# installed command works regardless of where this repo lives or whether it is
# deleted after installation.
PR_REVIEW_INSTALL_DIR="$INSTALL_DIR/pr-review"
mkdir -p "$PR_REVIEW_INSTALL_DIR"
cp "$SCRIPT_DIR/pr-review/"*.sh "$PR_REVIEW_INSTALL_DIR/"
chmod +x "$PR_REVIEW_INSTALL_DIR/"*.sh
echo "Scripts    → $PR_REVIEW_INSTALL_DIR/"

# ── Shell hint ────────────────────────────────────────────────────────────────
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  echo ""
  echo "  Note: $INSTALL_DIR is not in your PATH."
  echo "  Add this to your shell profile:"
  echo "    export PATH=\"\$PATH:$INSTALL_DIR\""
fi

# ── Wrapper script hint ───────────────────────────────────────────────────────
WRAPPER="$INSTALL_DIR/pr-review"
if [[ ! -f "$WRAPPER" ]]; then
  cat > "$WRAPPER" <<WRAPPER_EOF
#!/usr/bin/env bash
# pr-review — thin wrapper that sets PR_REVIEW_SCRIPT_DIR and launches the TUI
# PR_REVIEW_SCRIPT_DIR points to the scripts installed alongside this binary,
# so it works even if the original repo is moved or deleted.
export PR_REVIEW_SCRIPT_DIR="$INSTALL_DIR/pr-review"
exec "$INSTALL_DIR/$BINARY" "\$@"
WRAPPER_EOF
  chmod +x "$WRAPPER"
  echo "Wrapper    → $WRAPPER"
fi

echo ""
echo "Done! Run:  pr-review"
