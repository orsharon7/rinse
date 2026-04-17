#!/usr/bin/env bash
# pr-review-stats.sh — Per-run telemetry for RINSE Pro dashboard
#
# Stores each run's stats in $XDG_DATA_HOME/rinse/stats.json (JSON Lines format).
# All telemetry is LOCAL — nothing is transmitted anywhere.
# Opt-in: first call prompts the user; subsequent calls honour the saved choice.
#
# Schema version: 1
# Record fields:
#   schema_version   — integer, always 1 (bump only on breaking schema changes)
#   timestamp        — ISO 8601 UTC start time
#   repo             — "owner/repo"
#   pr_number        — integer
#   model            — model string used for the fix agent (empty for non-opencode runs)
#   duration_seconds — integer wall-clock seconds for the full run
#   iterations       — number of fix iterations completed
#   comments_resolved— total top-level Copilot comments fixed across all iterations
#   outcome          — one of: approved | clean | merged | closed | max_iter | error | aborted | dry_run
#
# Usage (from runner scripts):
#
#   # Source the helper — exports stats_* functions and RINSE_STATS_ENABLED
#   source pr-review-stats.sh
#
#   # At startup (after REPO is known):
#   stats_init "$REPO" "$PR_NUMBER" "$MODEL"   # records start time, checks opt-in
#
#   # Per-iteration (call each time opencode/claude fixes comments):
#   stats_add_iteration <comments_resolved_this_iter>
#
#   # At exit (any path):
#   stats_record "approved"    # or: clean merged closed max_iter error aborted
#
# Standalone CLI (for humans):
#   ./pr-review-stats.sh show [--repo owner/repo] [--limit N]
#   ./pr-review-stats.sh clear
#   ./pr-review-stats.sh opt-in
#   ./pr-review-stats.sh opt-out
#   ./pr-review-stats.sh status
#
# set -euo pipefail is applied only when executed directly (not sourced),
# so it does not mutate the caller's shell options.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  set -euo pipefail
fi

# ─── Constants ────────────────────────────────────────────────────────────────

# Honor XDG Base Directory Specification so users on Linux/macOS don't end up
# with a separate ~/.rinse tree alongside os.UserConfigDir()/rinse.
RINSE_CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/rinse"
RINSE_DATA_DIR="${XDG_DATA_HOME:-${HOME}/.local/share}/rinse"
RINSE_STATS_FILE="${RINSE_DATA_DIR}/stats.json"
RINSE_CONFIG_FILE="${RINSE_CONFIG_DIR}/config.json"
STATS_SCHEMA_VERSION=1

# ─── Config helpers ───────────────────────────────────────────────────────────

_rinse_config_get() {
  local key="$1"
  if [[ -f "$RINSE_CONFIG_FILE" ]]; then
    # Normalize JSON booleans (true/false) to strings for backward compatibility
    # with callers that compare against the string "true"/"false".
    jq -r --arg k "$key" '
      .[$k] // empty |
      if type == "boolean" then if . then "true" else "false" end else . end
    ' "$RINSE_CONFIG_FILE" 2>/dev/null || true
  fi
  return 0
}

_rinse_config_set() {
  local key="$1" value="$2"
  mkdir -p "$RINSE_CONFIG_DIR"
  local tmp
  tmp=$(mktemp "${RINSE_CONFIG_DIR}/.config.XXXXXX")
  # Use --argjson so "true"/"false" values are stored as JSON booleans rather
  # than strings, keeping the config file properly typed.
  if [[ -f "$RINSE_CONFIG_FILE" ]]; then
    if ! jq --arg k "$key" --argjson v "$value" '.[$k] = $v' "$RINSE_CONFIG_FILE" > "$tmp"; then
      # If the existing config is invalid JSON, fall back to a fresh minimal
      # object containing only the requested key.
      if ! jq -n --arg k "$key" --argjson v "$value" '{($k): $v}' > "$tmp"; then
        rm -f "$tmp"
        return 1
      fi
    fi
  else
    if ! jq -n --arg k "$key" --argjson v "$value" '{($k): $v}' > "$tmp"; then
      rm -f "$tmp"
      return 1
    fi
  fi
  if ! mv "$tmp" "$RINSE_CONFIG_FILE"; then
    rm -f "$tmp"
    return 1
  fi
}

# ─── Opt-in prompt ────────────────────────────────────────────────────────────

# Called during stats_init. If the user hasn't answered yet, asks interactively.
# Sets and exports RINSE_STATS_ENABLED=true|false.
_stats_check_optin() {
  local saved
  saved=$(_rinse_config_get "stats_enabled")

  if [[ -n "$saved" ]]; then
    export RINSE_STATS_ENABLED="$saved"
    return
  fi

  # Not yet answered — only prompt when stdin is a terminal
  if [[ -t 0 ]]; then
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "  RINSE — Local run telemetry (optional)"
    echo ""
    echo "  RINSE can save per-run stats (timestamp, repo, PR,"
    echo "  duration, comments resolved, model, outcome) to:"
    echo "    ${RINSE_STATS_FILE}"
    echo ""
    echo "  This data stays on your machine — nothing is sent"
    echo "  anywhere. It powers the RINSE Pro dashboard."
    echo ""
    echo "  You can opt out at any time:"
    echo "    pr-review-stats.sh opt-out"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    local _answer=""
    read -r -p "  Enable local stats? [y/N] " _answer </dev/tty || true
    echo ""

    case "${_answer,,}" in
      y|yes)
        export RINSE_STATS_ENABLED="true"
        if ! _rinse_config_set "stats_enabled" "true"; then
          echo "  Warning: could not save stats preference to ${RINSE_CONFIG_FILE}; stats remain enabled for this run." >&2
        fi
        echo "  Stats enabled. Saved to ${RINSE_STATS_FILE}"
        ;;
      *)
        export RINSE_STATS_ENABLED="false"
        if ! _rinse_config_set "stats_enabled" "false"; then
          echo "  Warning: could not save stats preference to ${RINSE_CONFIG_FILE}; stats remain disabled for this run." >&2
        fi
        echo "  Stats disabled. Run 'pr-review-stats.sh opt-in' to enable later."
        ;;
    esac
    echo ""
  else
    # Non-interactive (CI / piped) — default to off; don't persist the choice
    export RINSE_STATS_ENABLED="false"
  fi
}

# ─── Internal run state (in-memory) ──────────────────────────────────────────

_STATS_REPO=""
_STATS_PR=""
_STATS_MODEL=""
_STATS_START_TS=""        # ISO 8601
_STATS_START_EPOCH=0      # seconds since epoch
_STATS_ITERATIONS=0
_STATS_COMMENTS_RESOLVED=0

# ─── Public API ───────────────────────────────────────────────────────────────

# stats_init <repo> <pr_number> [model]
# Call once at runner startup, after REPO and PR_NUMBER are known.
stats_init() {
  _STATS_REPO="${1:?stats_init: repo required}"
  _STATS_PR="${2:?stats_init: pr_number required}"
  _STATS_MODEL="${3:-}"
  _STATS_START_TS=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  _STATS_START_EPOCH=$(date +%s)
  _STATS_ITERATIONS=0
  _STATS_COMMENTS_RESOLVED=0

  _stats_check_optin
}

# stats_add_iteration <comments_resolved_this_iter>
# Call once per fix iteration, after opencode/claude returns.
stats_add_iteration() {
  local count="${1:-0}"
  _STATS_ITERATIONS=$(( _STATS_ITERATIONS + 1 ))
  _STATS_COMMENTS_RESOLVED=$(( _STATS_COMMENTS_RESOLVED + count ))
}

# stats_record <outcome>
# Call once at exit. outcome: approved | clean | merged | closed | max_iter | error | aborted | dry_run
stats_record() {
  local outcome="${1:?stats_record: outcome required}"

  # Bail early if opt-in not given
  if [[ "${RINSE_STATS_ENABLED:-false}" != "true" ]]; then
    return 0
  fi

  local end_epoch duration
  end_epoch=$(date +%s)
  duration=$(( end_epoch - _STATS_START_EPOCH ))

  mkdir -p "$RINSE_DATA_DIR"

  # Append a JSON object (one record per line — JSON Lines)
  jq -cn \
    --argjson schema_version "$STATS_SCHEMA_VERSION" \
    --arg timestamp "$_STATS_START_TS" \
    --arg repo "$_STATS_REPO" \
    --argjson pr_number "$_STATS_PR" \
    --arg model "$_STATS_MODEL" \
    --argjson duration_seconds "$duration" \
    --argjson iterations "$_STATS_ITERATIONS" \
    --argjson comments_resolved "$_STATS_COMMENTS_RESOLVED" \
    --arg outcome "$outcome" \
    '{
      schema_version: $schema_version,
      timestamp: $timestamp,
      repo: $repo,
      pr_number: $pr_number,
      model: $model,
      duration_seconds: $duration_seconds,
      iterations: $iterations,
      comments_resolved: $comments_resolved,
      outcome: $outcome
    }' >> "$RINSE_STATS_FILE" || true
}

# ─── Standalone CLI ───────────────────────────────────────────────────────────

_cli_show() {
  local limit=20 repo_filter=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --limit) limit="$2"; shift 2 ;;
      --repo)  repo_filter="$2"; shift 2 ;;
      *) >&2 echo "Unknown flag: $1"; exit 1 ;;
    esac
  done

  # Validate --limit as a positive integer; fall back to default on invalid input
  if ! [[ "$limit" =~ ^[0-9]+$ ]] || (( limit < 1 )); then
    >&2 echo "Warning: --limit must be a positive integer; using default (20)."
    limit=20
  fi

  if [[ ! -f "$RINSE_STATS_FILE" ]]; then
    echo "No stats yet. Run RINSE at least once with stats enabled."
    return
  fi

  echo "RINSE run stats (last ${limit}):"
  echo ""
  printf "%-24s %-30s %-5s %-8s %-6s %-10s %-10s %s\n" \
    "timestamp" "repo" "pr" "dur(s)" "iters" "comments" "outcome" "model"
  echo "$(printf '%.0s─' {1..110})"

  if [[ -n "$repo_filter" ]]; then
    awk -v limit="$limit" -v repo_filter="$repo_filter" '
      BEGIN {
        pattern = "\"repo\":\"" repo_filter "\""
      }
      index($0, pattern) {
        buf[count % limit] = $0
        count++
      }
      END {
        start = (count > limit ? count - limit : 0)
        for (i = start; i < count; i++) {
          print buf[i % limit]
        }
      }
    ' "$RINSE_STATS_FILE" 2>/dev/null | \
    jq -r '[
      .timestamp,
      .repo,
      (.pr_number | tostring),
      (.duration_seconds | tostring),
      (.iterations | tostring),
      (.comments_resolved | tostring),
      .outcome,
      (.model // "")
    ] | @tsv' 2>/dev/null | \
    awk -F'\t' '{ printf "%-24s %-30s %-5s %-8s %-6s %-10s %-10s %s\n", $1, $2, $3, $4, $5, $6, $7, $8 }'
  else
    tail -n "$limit" "$RINSE_STATS_FILE" 2>/dev/null | \
    jq -r '[
      .timestamp,
      .repo,
      (.pr_number | tostring),
      (.duration_seconds | tostring),
      (.iterations | tostring),
      (.comments_resolved | tostring),
      .outcome,
      (.model // "")
    ] | @tsv' 2>/dev/null | \
    awk -F'\t' '{ printf "%-24s %-30s %-5s %-8s %-6s %-10s %-10s %s\n", $1, $2, $3, $4, $5, $6, $7, $8 }'
  fi
}

_cli_clear() {
  if [[ -f "$RINSE_STATS_FILE" ]]; then
    rm -f "$RINSE_STATS_FILE"
    echo "Stats cleared: ${RINSE_STATS_FILE}"
  else
    echo "No stats file to clear."
  fi
}

_cli_status() {
  local enabled
  enabled=$(_rinse_config_get "stats_enabled")
  echo "Stats file:    ${RINSE_STATS_FILE}"
  echo "Stats enabled: ${enabled:-not set (will prompt on next run)}"
  if [[ -f "$RINSE_STATS_FILE" ]]; then
    local count
    count=$(wc -l < "$RINSE_STATS_FILE" | tr -d ' ')
    echo "Total records: ${count}"
  else
    echo "Total records: 0"
  fi
}

# ─── Entry point (CLI mode) ───────────────────────────────────────────────────

# Only run CLI when this script is executed directly (not sourced)
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  CMD="${1:-help}"
  shift || true

  case "$CMD" in
    show)     _cli_show "$@" ;;
    clear)    _cli_clear ;;
    opt-in)
      _rinse_config_set "stats_enabled" "true"
      echo "RINSE stats enabled. Records will be saved to ${RINSE_STATS_FILE}"
      ;;
    opt-out)
      _rinse_config_set "stats_enabled" "false"
      echo "RINSE stats disabled. Existing records kept at ${RINSE_STATS_FILE}"
      ;;
    status)   _cli_status ;;
    help|--help|-h)
      grep '^#' "$0" | head -40 | sed 's/^# \?//'
      ;;
    *)
      >&2 echo "Unknown command: ${CMD}. Try: show | clear | opt-in | opt-out | status"
      exit 1
      ;;
  esac
fi
