#!/usr/bin/env bash
# pr-review-daemon.sh — Persistent PR review watcher
#
# Runs continuously, polls watched PRs every POLL_INTERVAL seconds.
# When a new review is detected, fires a system event / notification
# with the review details — letting the agent handle fixes.
#
# This replaces the cron → isolated agent → sub-agent chain.
# The script does the dumb work (polling GitHub), the agent does the smart work (fixing code).
#
# Usage:
#   ./pr-review-daemon.sh                    # foreground
#   nohup ./pr-review-daemon.sh &            # background
#   ./pr-review-daemon.sh --once             # single poll (for testing)
#
# Environment:
#   POLL_INTERVAL    — seconds between polls (default: 300 = 5 min)
#   WATCH_FILE       — path to watch list JSON (default: ~/.pr-review-watches.json)
#   PR_REVIEW_SCRIPT — path to pr-review.sh (default: sibling file)
#
set -euo pipefail

# Require Bash 4+ for associative arrays (declare -A)
if [[ "${BASH_VERSINFO[0]}" -lt 4 ]]; then
  echo "Error: pr-review-daemon.sh requires Bash 4+ (found ${BASH_VERSION}). On macOS, install via: brew install bash" >&2
  exit 1
fi

POLL_INTERVAL="${POLL_INTERVAL:-300}"
WATCH_FILE="${WATCH_FILE:-${HOME}/.pr-review-watches.json}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PR_REVIEW_SCRIPT="${PR_REVIEW_SCRIPT:-${SCRIPT_DIR}/pr-review.sh}"
ONCE=false
PIDFILE="${HOME}/.pr-review-daemon.pid"
LOGFILE="${HOME}/.pr-review-daemon.log"
MAX_CONCURRENT="${MAX_CONCURRENT:-3}"
if ! [[ "$MAX_CONCURRENT" =~ ^[0-9]+$ ]] || (( MAX_CONCURRENT < 1 )); then
  echo "Error: MAX_CONCURRENT must be an integer >= 1 (got: '${MAX_CONCURRENT}')" >&2
  exit 1
fi
RUNNER="${PR_REVIEW_RUNNER:-opencode}"

# ─── Job table (tracking running PIDs per repo#pr) ───────────────────────────
declare -A JOB_PIDS  # key: "repo#pr" → PID

is_job_running() {
  local key="$1"
  local pid="${JOB_PIDS[$key]:-}"
  [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null
}

running_job_count() {
  local count=0
  for key in "${!JOB_PIDS[@]}"; do
    local pid="${JOB_PIDS[$key]}"
    if kill -0 "$pid" 2>/dev/null; then
      count=$((count + 1))
    else
      unset 'JOB_PIDS[$key]'
    fi
  done
  echo "$count"
}

# Repo path mapping (repo → local path)
# Add entries here for repos you work with locally, or leave empty to use --cwd auto-detection.
get_repo_path() {
  case "$1" in
    # "owner/repo-name") echo "/path/to/local/repo/" ;;
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

# ─── Fire event ──────────────────────────────────────────────────────────────

fire_event() {
  local text="$1"
  log "🔔 Event: ${text}"
  # Hook: add your own notification mechanism here (Slack, webhook, etc.)
}

# ─── On-disk lock (atomic mkdir) for cross-process dispatch de-duplication ────

DAEMON_LOCK_DIR="${HOME}/.pr-review/daemon-locks"
mkdir -p "$DAEMON_LOCK_DIR"

# Write owner PID to the dispatch lock pidfile. PGID is intentionally not stored:
# the dispatched background job inherits the daemon's process group, so a PGID
# check would consider a stale lock active as long as the daemon is alive.
_write_dispatch_lock_metadata() {
  local pidfile="$1"
  cat > "$pidfile" <<EOF
owner_pid=$$
EOF
}

# Returns 0 (active) if the owner_pid recorded in the pidfile is still alive.
# Also accepts legacy single-integer pidfiles written by older versions.
_dispatch_lock_is_active() {
  local pidfile="$1"
  local line owner_pid=""

  [[ -f "$pidfile" ]] || return 1

  while IFS= read -r line; do
    case "$line" in
      owner_pid=*) owner_pid="${line#owner_pid=}" ;;
      pgid=*) ;;  # ignored: PGID check removed (see _write_dispatch_lock_metadata)
      *)
        # Legacy: plain integer
        if [[ -z "$owner_pid" && "$line" =~ ^[0-9]+$ ]]; then
          owner_pid="$line"
        fi
        ;;
    esac
  done < "$pidfile"

  if [[ -n "$owner_pid" ]] && kill -0 "$owner_pid" 2>/dev/null; then
    return 0
  fi
  return 1
}

acquire_dispatch_lock() {
  local repo="$1" pr="$2"
  local key="${repo//\//_}#${pr}"
  local lockdir="${DAEMON_LOCK_DIR}/${key}.lock"
  local pidfile="${lockdir}/pid"

  if mkdir "$lockdir" 2>/dev/null; then
    _write_dispatch_lock_metadata "$pidfile"
    return 0
  fi

  if _dispatch_lock_is_active "$pidfile"; then
    return 1  # Another runner is active for this PR
  fi

  # Stale lock — remove and retry atomic acquisition once
  rm -rf "$lockdir"
  if mkdir "$lockdir" 2>/dev/null; then
    _write_dispatch_lock_metadata "$pidfile"
    return 0
  fi

  return 1
}

release_dispatch_lock() {
  local repo="$1" pr="$2"
  local key="${repo//\//_}#${pr}"
  rm -rf "${DAEMON_LOCK_DIR}/${key}.lock"
}

# ─── Dispatch runner (launch a review cycle for a PR) ─────────────────────────

dispatch_runner() {
  local repo="$1" pr="$2"
  local key="${repo}#${pr}"

  # Skip if a runner is already active for this PR (in-memory check)
  if is_job_running "$key"; then
    log "   ⏭  PR ${key} already has an active runner — skipping"
    return 0
  fi

  # Acquire on-disk atomic lock to prevent cross-process duplicates
  if ! acquire_dispatch_lock "$repo" "$pr"; then
    log "   ⏭  PR ${key} is locked by another process — skipping"
    return 0
  fi

  # Check concurrency limit
  local active
  active=$(running_job_count)
  if [[ "$active" -ge "$MAX_CONCURRENT" ]]; then
    log "   ⏸  Concurrency limit reached (${active}/${MAX_CONCURRENT}) — deferring ${key}"
    release_dispatch_lock "$repo" "$pr"
    return 0
  fi

  local repo_path
  repo_path=$(get_repo_path "$repo")
  if [[ -z "$repo_path" ]]; then
    log "   ⚠️  No repo path configured for ${repo} — cannot dispatch runner"
    fire_event "⚠️ ${key}: no local path configured — manual fix needed"
    release_dispatch_lock "$repo" "$pr"
    return 1
  fi

  local script
  case "$RUNNER" in
    opencode) script="${SCRIPT_DIR}/pr-review-opencode.sh" ;;
    claude)   script="${SCRIPT_DIR}/pr-review-claude-v2.sh" ;;
    *) log "   ⚠️  Unknown runner: $RUNNER"; release_dispatch_lock "$repo" "$pr"; return 1 ;;
  esac

  local repo_slug="${repo//\//_}"
  local pr_log="${HOME}/.pr-review/logs/${repo_slug}-pr-${pr}.log"
  mkdir -p "$(dirname "$pr_log")"

  local lockdir="${DAEMON_LOCK_DIR}/${repo//\//_}#${pr}.lock"

  log "🚀 Dispatching runner for ${key} (runner: ${RUNNER})"
  (
    runner_pid=""

    cleanup_dispatch_wrapper() {
      release_dispatch_lock "$repo" "$pr"
    }

    trap 'cleanup_dispatch_wrapper' EXIT

    forward_runner_signal() {
      local sig="$1"
      if [[ -n "$runner_pid" ]] && kill -0 "$runner_pid" 2>/dev/null; then
        kill "-${sig}" "$runner_pid" 2>/dev/null || kill "$runner_pid" 2>/dev/null || true
      fi
    }

    trap 'forward_runner_signal TERM; wait "$runner_pid" 2>/dev/null || true; exit 143' TERM
    trap 'forward_runner_signal INT; wait "$runner_pid" 2>/dev/null || true; exit 130' INT

    bash "$script" "$pr" \
      --repo "$repo" \
      --cwd "$repo_path" \
      --worktree \
      --repo-root "$repo_path" \
      --no-interactive \
      >/dev/null 2>&1 &
    runner_pid=$!

    cat > "${lockdir}/pid" <<EOF
owner_pid=${runner_pid}
wrapper_pid=${BASHPID}
EOF

    if wait "$runner_pid"; then
      runner_status=0
    else
      runner_status=$?
    fi

    release_dispatch_lock "$repo" "$pr"
    exit "$runner_status"
  ) &

  local child_pid=$!
  JOB_PIDS["$key"]=$child_pid
  # Store the wrapper PID in memory for daemon cleanup; the wrapper now traps
  # termination and forwards it to the real runner process before waiting for
  # shutdown. The on-disk pidfile is updated inside the wrapper with the real
  # runner PID for stale-lock detection.
  log "   PID ${child_pid} (wrapper) → log: ${pr_log}"
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

        # Dispatch a runner to fix the comments
        dispatch_runner "$repo" "$pr" || true
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

# Cleanup trap: kill all dispatched runners on daemon exit
cleanup_daemon() {
  log "🛑 Daemon shutting down — killing dispatched runners..."
  for key in "${!JOB_PIDS[@]}"; do
    local pid="${JOB_PIDS[$key]}"
    if kill -0 "$pid" 2>/dev/null; then
      log "   Killing ${key} (PID ${pid})"
      kill "$pid" 2>/dev/null || true
    fi
    # Do not release the on-disk dispatch lock here: the wrapper/runner may
    # still be exiting after SIGTERM, and clearing the lock early can allow a
    # duplicate dispatch for the same repo#PR. Let normal stale-PID detection
    # recover the lock if the daemon exits before the job fully stops.
  done
  rm -f "$PIDFILE"
}
if [[ "$ONCE" != true ]]; then
  trap cleanup_daemon EXIT
fi

if [[ "$ONCE" == true ]]; then
  poll_once
  exit 0
fi

log "🚀 PR Review Daemon started (PID $$, interval ${POLL_INTERVAL}s)"
log "   Watch file: ${WATCH_FILE}"
log "   Script: ${PR_REVIEW_SCRIPT}"
log "   Max concurrent: ${MAX_CONCURRENT}"
log "   Runner: ${RUNNER}"

while true; do
  poll_once
  active=$(running_job_count)
  log "💤 Sleeping ${POLL_INTERVAL}s... (${active} active runner(s))"
  sleep "$POLL_INTERVAL"
done
