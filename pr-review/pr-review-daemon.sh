#!/usr/bin/env bash
# pr-review-daemon.sh — Persistent PR review watcher
#
# Runs continuously, polls watched PRs every POLL_INTERVAL seconds.
# When a new review is detected, fires an OpenClaw system event
# with the review details — letting the agent handle fixes.
#
# This replaces the cron → isolated agent → sub-agent chain.
# The script does the dumb work (polling GitHub), OpenClaw does the smart work (fixing code).
#
# Usage:
#   ./pr-review-daemon.sh                    # foreground
#   nohup ./pr-review-daemon.sh &            # background
#   ./pr-review-daemon.sh --once             # single poll (for testing)
#
# Environment:
#   POLL_INTERVAL   — seconds between polls (default: 300 = 5 min)
#   WATCH_FILE      — path to watch list JSON (default: ~/.pr-review-watches.json)
#   PR_REVIEW_SCRIPT — path to pr-review.sh (default: sibling file)
#   OPENCLAW_TOKEN  — gateway token (optional, for auth)
#
set -euo pipefail

POLL_INTERVAL="${POLL_INTERVAL:-300}"
WATCH_FILE="${WATCH_FILE:-${HOME}/.pr-review-watches.json}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PR_REVIEW_SCRIPT="${PR_REVIEW_SCRIPT:-${SCRIPT_DIR}/pr-review.sh}"
ONCE=false
PIDFILE="${HOME}/.pr-review-daemon.pid"
LOGFILE="${HOME}/.pr-review-daemon.log"

# Repo path mapping (repo → local path)
# Using a function instead of associative array to avoid bash slash issues
get_repo_path() {
  case "$1" in
    "orsharon7/gsc-solar-monitor") echo "/home/luli/.openclaw/workspace/gsc-solar-monitor/" ;;
    "orsharon7/gsc-website") echo "/home/luli/.openclaw/workspace/gsc-website/" ;;
    *) echo "" ;;
  esac
}

# ─── Argument parsing ─────────────────────────────────────────────────────────

for arg in "$@"; do
  case "$arg" in
    --once) ONCE=true ;;
    --stop)
      if [[ -f "$PIDFILE" ]]; then
        kill "$(cat "$PIDFILE")" 2>/dev/null && echo "Daemon stopped." || echo "No running daemon."
        rm -f "$PIDFILE"
      else
        echo "No pidfile found."
      fi
      exit 0
      ;;
    --status)
      if [[ -f "$PIDFILE" ]] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "Running (PID $(cat "$PIDFILE"))"
        echo "Watches: $(cat "$WATCH_FILE" 2>/dev/null | jq 'length' 2>/dev/null || echo 0)"
        echo "Last 5 log lines:"
        tail -5 "$LOGFILE" 2>/dev/null || echo "(no log)"
      else
        echo "Not running."
        rm -f "$PIDFILE"
      fi
      exit 0
      ;;
    --help|-h)
      head -20 "$0" | grep '^#' | sed 's/^# \?//'
      exit 0
      ;;
  esac
done

# ─── PID management ───────────────────────────────────────────────────────────

if [[ "$ONCE" == false ]]; then
  # Check for existing daemon
  if [[ -f "$PIDFILE" ]] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
    echo "Daemon already running (PID $(cat "$PIDFILE")). Use --stop first."
    exit 1
  fi
  echo $$ > "$PIDFILE"
  trap 'rm -f "$PIDFILE"' EXIT
fi

# ─── Logging ──────────────────────────────────────────────────────────────────

log() {
  local ts
  ts=$(date '+%Y-%m-%d %H:%M:%S')
  echo "[$ts] $*" >> "$LOGFILE"
}

# ─── Fire OpenClaw event ─────────────────────────────────────────────────────

fire_event() {
  local text="$1"
  log "🔔 Firing OpenClaw event..."

  local token_arg=""
  if [[ -n "${OPENCLAW_TOKEN:-}" ]]; then
    token_arg="--token $OPENCLAW_TOKEN"
  fi

  # Use openclaw CLI to send system event
  openclaw system event --text "$text" --mode now $token_arg 2>&1 | tee -a "$LOGFILE" || {
    log "⚠️  Failed to fire OpenClaw event"
    return 1
  }
}

# ─── Build agent task for a review ────────────────────────────────────────────

build_fix_task() {
  local repo="$1" pr="$2" review_id="$3" comment_count="$4" comments_json="$5"
  local repo_path
  repo_path=$(get_repo_path "$repo")

  if [[ -z "$repo_path" ]]; then
    echo "Unknown repo: $repo"
    return 1
  fi

  # Build a concise summary of comments for the event text
  local comment_summary
  comment_summary=$(echo "$comments_json" | jq -r '
    [.[] | select(.in_reply_to_id == null) | "\(.path): \(.body | split("\n")[0] | .[0:100])"]
    | join("\n  - ")
  ')

  cat <<EOF
🔍 PR Review: ${repo}#${pr} — ${comment_count} new comments (review ${review_id})

Comments:
  - ${comment_summary}

Please fix these review comments. For each comment:
1. Read the full comment from GitHub
2. Fix the code in the local repo at: ${repo_path}
3. Reply to EVERY comment on GitHub with what you fixed
4. Commit and push the fixes
5. Re-request Copilot review

Use: gh api repos/${repo}/pulls/${pr}/comments/COMMENT_ID/replies -X POST -f body="Fixed — description. ✅"
Then: gh api repos/${repo}/pulls/${pr}/requested_reviewers -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}'

Branch: $(echo "$comments_json" | jq -r '.[0].path // "unknown"' | head -1)
EOF
}

# ─── Single poll cycle ────────────────────────────────────────────────────────

poll_once() {
  if [[ ! -f "$WATCH_FILE" ]]; then
    log "No watch file. Nothing to do."
    return
  fi

  local watches
  watches=$(cat "$WATCH_FILE")
  local count
  count=$(echo "$watches" | jq 'length')

  if [[ "$count" -eq 0 ]]; then
    log "Watch list empty."
    return
  fi

  log "Polling ${count} watched PR(s)..."

  # Run poll-all and capture results
  local result
  result=$(bash "$PR_REVIEW_SCRIPT" poll-all 2>/dev/null) || {
    log "⚠️  poll-all failed"
    return
  }

  # Parse results
  local results_count
  results_count=$(echo "$result" | jq '.results | length' 2>/dev/null || echo 0)

  if [[ "$results_count" -eq 0 ]]; then
    log "No results."
    return
  fi

  # Process each result
  local event_parts=()
  local has_actionable=false

  for i in $(seq 0 $((results_count - 1))); do
    local status repo pr
    status=$(echo "$result" | jq -r ".results[$i].status")
    repo=$(echo "$result" | jq -r ".results[$i].repo")
    pr=$(echo "$result" | jq -r ".results[$i].pr")

    case "$status" in
      new_review)
        has_actionable=true
        local review_id comment_count comments
        review_id=$(echo "$result" | jq -r ".results[$i].review_id")
        comment_count=$(echo "$result" | jq -r ".results[$i].comment_count")
        comments=$(echo "$result" | jq ".results[$i].comments")

        local task
        task=$(build_fix_task "$repo" "$pr" "$review_id" "$comment_count" "$comments")
        event_parts+=("$task")
        log "🆕 ${repo}#${pr}: ${comment_count} new comments"
        ;;
      approved)
        has_actionable=true
        event_parts+=("✅ ${repo}#${pr} — APPROVED by Copilot! Ready to merge.")
        log "✅ ${repo}#${pr}: Approved"
        ;;
      clean)
        has_actionable=true
        event_parts+=("✅ ${repo}#${pr} — Clean review (0 comments). Ready to merge.")
        log "✅ ${repo}#${pr}: Clean review"
        ;;
      merged)
        has_actionable=true
        event_parts+=("🎉 ${repo}#${pr} — Merged!")
        log "🎉 ${repo}#${pr}: Merged"
        ;;
      closed)
        has_actionable=true
        event_parts+=("📕 ${repo}#${pr} — Closed (not merged)")
        log "📕 ${repo}#${pr}: Closed"
        ;;
      error_retried)
        local msg
        msg=$(echo "$result" | jq -r ".results[$i].message")
        log "🔄 ${repo}#${pr}: ${msg}"
        ;;
      pending|no_change|no_reviews)
        log "⏳ ${repo}#${pr}: ${status}"
        ;;
      error)
        local msg
        msg=$(echo "$result" | jq -r ".results[$i].message")
        log "❌ ${repo}#${pr}: ${msg}"
        ;;
    esac
  done

  # Fire single combined event if there's anything actionable
  if [[ "$has_actionable" == true && ${#event_parts[@]} -gt 0 ]]; then
    local combined_text
    combined_text=$(printf '%s\n\n---\n\n' "${event_parts[@]}")
    fire_event "$combined_text"
  fi
}

# ─── Main loop ────────────────────────────────────────────────────────────────

if [[ "$ONCE" == true ]]; then
  poll_once
  exit 0
fi

log "🚀 PR Review Daemon started (PID $$, interval ${POLL_INTERVAL}s)"
log "   Watch file: ${WATCH_FILE}"
log "   Script: ${PR_REVIEW_SCRIPT}"

while true; do
  poll_once
  log "💤 Sleeping ${POLL_INTERVAL}s..."
  sleep "$POLL_INTERVAL"
done
