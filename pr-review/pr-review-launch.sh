#!/usr/bin/env bash
# pr-review-launch.sh — launches the pr-review TUI wizard
#
# Delegates to the compiled Go binary (tui/pr-review-tui) which uses
# Bubble Tea for a smooth, flicker-free, full-width terminal UI.
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TUI_BIN="${SCRIPT_DIR}/../tui/pr-review-tui"

if [[ ! -x "$TUI_BIN" ]]; then
  echo "error: TUI binary not found at ${TUI_BIN}" >&2
  echo "  cd $(dirname "$TUI_BIN") && go build -o pr-review-tui ." >&2
  exit 1
fi

export PR_REVIEW_SCRIPT_DIR="$SCRIPT_DIR"

exec "$TUI_BIN" "$@"
