#!/usr/bin/env bash
# pr-review-claude.sh — Automated Copilot PR review loop using Claude Code CLI
#
# Invokes `claude` in a loop to fix Copilot review comments until:
#   - Copilot approves the PR
#   - Copilot reviews with 0 comments (clean)
#   - PR is merged or closed
#   - Max iterations reached
#
# Usage:
#   ./pr-review-claude.sh <pr_number> [options]
#
# Options:
#   --repo <owner/repo>     Override repo detection (default: auto-detect from --cwd)
#   --cwd <path>            Local repo path (default: current directory)
#   --max-iter <n>          Max fix iterations before giving up (default: 10)
#   --wait-max <seconds>    Max seconds to wait per Copilot review (default: 300)
#
# Requirements:
#   - claude CLI in PATH (claude --version)
#   - gh CLI authenticated
#   - pr-review.sh in same directory
#
# Example:
#   ./pr-review-claude.sh 42 --repo owner/repo \
#     --cwd ~/dev/my-repo --max-iter 5
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PR_REVIEW="${SCRIPT_DIR}/pr-review.sh"

# ─── Arg parsing ──────────────────────────────────────────────────────────────

if [[ $# -lt 1 || "$1" == "--help" || "$1" == "-h" ]]; then
  head -30 "$0" | grep '^#' | sed 's/^# \?//'
  exit 0
fi

PR_NUMBER="${1}"
shift

REPO=""
CWD="$(pwd)"
MAX_ITER=10
WAIT_MAX=300
LOGFILE="${HOME}/.pr-review-claude.log"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)      REPO="$2";      shift 2 ;;
    --cwd)       CWD="$2";       shift 2 ;;
    --max-iter)  MAX_ITER="$2";  shift 2 ;;
    --wait-max)  WAIT_MAX="$2";  shift 2 ;;
    *) >&2 echo "Unknown arg: $1"; exit 1 ;;
  esac
done

# ─── Setup ────────────────────────────────────────────────────────────────────

log() {
  local ts
  ts=$(date '+%Y-%m-%d %H:%M:%S')
  echo "[$ts] $*" | tee -a "$LOGFILE"
}

# Detect repo from CWD if not provided
if [[ -z "$REPO" ]]; then
  REPO=$(cd "$CWD" && gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  if [[ -z "$REPO" ]]; then
    >&2 echo "Could not detect repo. Use --repo owner/repo or run from inside a git repo."
    exit 1
  fi
fi

REPO_FLAG="--repo ${REPO}"

# ─── Stats / telemetry (opt-in) ───────────────────────────────────────────────

SCRIPT_DIR_STATS="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=pr-review-stats.sh
source "${SCRIPT_DIR_STATS}/pr-review-stats.sh"
stats_init "$REPO" "$PR_NUMBER" ""   # model unknown for legacy claude script

_RINSE_OUTCOME="aborted"
_stats_exit_trap() {
  local exit_code="${1:-$?}"
  if [[ "$_RINSE_OUTCOME" == "aborted" ]]; then
    local final_status_json final_status
    final_status_json=$(bash "$PR_REVIEW" "$PR_NUMBER" status $REPO_FLAG 2>/dev/null || true)
    final_status=$(echo "$final_status_json" | jq -r '.status // "unknown"' 2>/dev/null || echo "unknown")

    case "$final_status" in
      approved) _RINSE_OUTCOME="approved" ;;
      clean)    _RINSE_OUTCOME="clean" ;;
      merged)   _RINSE_OUTCOME="merged" ;;
      closed)   _RINSE_OUTCOME="closed" ;;
      *)
        # Derive fallback outcome from exit code when status probe is unavailable or unreliable
        if [[ "$exit_code" -ne 0 ]]; then
          _RINSE_OUTCOME="error"
        else
          _RINSE_OUTCOME="clean"
        fi
        ;;
    esac
  fi

  stats_record "$_RINSE_OUTCOME"
}
trap '_exit_code=$?; _stats_exit_trap "$_exit_code"' EXIT

log "🚀 Claude PR review loop starting"
log "   PR:          ${REPO}#${PR_NUMBER}"
log "   Local path:  ${CWD}"
log "   Max iter:    ${MAX_ITER}   Wait max: ${WAIT_MAX}s"
log "   Log file:    ${LOGFILE}"

# ─── Startup state check ──────────────────────────────────────────────────────
# Show the current PR state before entering the loop so we know what cycle sees.
# If there's an existing review with no state file (fresh start), seed last_known
# so cycle requests a fresh review instead of re-processing old comments.

log "🔍 Checking current PR state..."
startup_status=$(bash "$PR_REVIEW" "$PR_NUMBER" status $REPO_FLAG 2>/dev/null) || true
startup_state=$(echo "$startup_status" | jq -r '.status // "unknown"')
REPO_SLUG="${REPO//\//_}"
STATE_DIR="${HOME}/.pr-review/state/${REPO_SLUG}"
STATE_FILE="${STATE_DIR}/pr-${PR_NUMBER}-last-review"

case "$startup_state" in
  pending)
    log "   State: Copilot review in progress — will wait for it"
    ;;
  new_review)
    review_id_startup=$(echo "$startup_status" | jq -r '.review_id')
    cc_startup=$(echo "$startup_status" | jq -r '.comment_count')
    log "   State: Existing review ${review_id_startup} with ${cc_startup} unresolved comment(s)"
    if [[ ! -f "$STATE_FILE" ]]; then
      if [[ "$cc_startup" -gt 0 ]]; then
        # Unresolved comments exist — let the loop pick them up and fix them.
        # Do NOT seed the state file; cycle will return this review for Claude to process.
        log "   No state file + unresolved comments → will fix existing review first"
      else
        # No comments (already clean) — seed so cycle requests a fresh review.
        mkdir -p "$STATE_DIR"
        echo "$review_id_startup" > "$STATE_FILE"
        log "   No state file + 0 comments → seeded, cycle will request a fresh review"
      fi
    else
      saved=$(cat "$STATE_FILE")
      if [[ "$saved" == "$review_id_startup" ]]; then
        log "   State file matches — cycle will request a fresh review"
      else
        log "   State file has different review (${saved}) — cycle will process review ${review_id_startup}"
      fi
    fi
    ;;
  approved)
    log "   State: Already APPROVED — nothing to do"
    _RINSE_OUTCOME="approved"; exit 0
    ;;
  no_reviews)
    log "   State: No Copilot reviews yet — cycle will request the first one"
    ;;
  no_change)
    log "   State: No new review since last-known"
    ;;
  *)
    log "   State: ${startup_state}"
    ;;
esac
echo ""

# ─── Main loop ────────────────────────────────────────────────────────────────

for iter in $(seq 1 "$MAX_ITER"); do
  log "━━━ Iteration ${iter}/${MAX_ITER} ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

  # ── Step 1: Wait for Copilot to finish reviewing ───────────────────────────
  # `cycle` will:
  #   - Auto-request Copilot review if not already pending
  #   - Block until the review lands (up to WAIT_MAX seconds, with stall recovery)
  #   - Return JSON with status + comments

  log "⏳ Waiting for Copilot review (up to ${WAIT_MAX}s)..."

  review_result=$(bash "$PR_REVIEW" "$PR_NUMBER" cycle --wait "$WAIT_MAX" $REPO_FLAG 2>>"$LOGFILE") || {
    log "❌ pr-review cycle exited non-zero — check log for details"
    exit 1
  }
  status=$(echo "$review_result" | jq -r '.status')

  log "📋 Copilot review status: ${status}"

  # ── Step 2: Act on the result ─────────────────────────────────────────────

  case "$status" in

    approved)
      log "✅ Copilot APPROVED PR #${PR_NUMBER}! Ready to merge."
      echo "$review_result"
      _RINSE_OUTCOME="approved"
      exit 0
      ;;

    clean)
      log "✅ Clean review — Copilot returned 0 comments. PR #${PR_NUMBER} is ready to merge."
      echo "$review_result"
      _RINSE_OUTCOME="clean"
      exit 0
      ;;

    merged)
      log "🎉 PR #${PR_NUMBER} is already merged."
      _RINSE_OUTCOME="merged"
      exit 0
      ;;

    closed)
      log "📕 PR #${PR_NUMBER} was closed without merging."
      _RINSE_OUTCOME="closed"
      exit 1
      ;;

    error)
      msg=$(echo "$review_result" | jq -r '.message // "unknown error"')
      log "❌ pr-review cycle error: ${msg}"
      _RINSE_OUTCOME="error"
      exit 1
      ;;

    pending)
      # Still pending after WAIT_MAX — Copilot may be stuck; try again next iter
      log "⚠️  Still pending after ${WAIT_MAX}s — will retry next iteration"
      sleep 15
      continue
      ;;

    no_change)
      # Copilot hasn't posted a new review since last-known — wait and retry
      log "⏳ No new review yet — sleeping 30s before retry"
      sleep 30
      continue
      ;;

    new_review)
      : # fall through to fix step below
      ;;

    *)
      log "⚠️  Unexpected status '${status}' — continuing"
      sleep 10
      continue
      ;;

  esac

  # ── Step 3: Invoke Claude to fix the comments ─────────────────────────────

  comment_count=$(echo "$review_result" | jq -r '.comment_count')
  review_id=$(echo "$review_result" | jq -r '.review_id')

  log "💬 ${comment_count} comment(s) in review ${review_id} — invoking Claude..."

  # Pretty-print comments for the prompt
  comments_json=$(echo "$review_result" | jq '.comments')

  # Build the prompt
  # We give Claude everything it needs to work autonomously:
  # - The comments JSON (id, path, line, body)
  # - The local repo path
  # - Exact bash commands to push and reply
  read -r -d '' PROMPT << PROMPT_EOF || true
You are fixing GitHub Copilot code review comments on PR #${PR_NUMBER} in the ${REPO} repository.

Local repo directory: ${CWD}
Review ID: ${review_id}
Total comments to fix: ${comment_count}

## Review comments (JSON):
\`\`\`json
${comments_json}
\`\`\`

Each comment object has:
  - \`id\`              — comment ID (needed for replies)
  - \`path\`            — file path relative to repo root
  - \`line\`            — line number the comment is on
  - \`body\`            — the review text to address
  - \`in_reply_to_id\`  — null for top-level comments (these are the ones to fix)

## Your task

1. For each top-level comment (where \`in_reply_to_id\` is null):
   a. Read the file at \`${CWD}/<path>\`
   b. Understand and fix the issue described in \`body\` at/around \`line\`
   c. Make the minimal targeted change — do not refactor unrelated code

2. After ALL comments are fixed, commit and push:
   \`\`\`bash
   bash ${PR_REVIEW} ${PR_NUMBER} push ${REPO_FLAG}
   \`\`\`

3. Request a new Copilot review (required — do not skip):
   \`\`\`bash
   bash ${PR_REVIEW} ${PR_NUMBER} request ${REPO_FLAG}
   \`\`\`

4. Reply to EVERY top-level comment to confirm it was fixed:
   \`\`\`bash
   bash ${PR_REVIEW} ${PR_NUMBER} reply <comment_id> "Fixed: <one-line description> ✅"
   \`\`\`

## Rules
- Fix ALL comments before pushing (one commit for all fixes)
- Only change what each comment asks for — no refactoring beyond the comment scope
- If a comment is ambiguous, make the most reasonable interpretation
- Push exactly once, after all fixes
- Always run \`request\` after push — the outer loop waits for a fresh Copilot review
- Reply to every comment after the push succeeds
PROMPT_EOF

  # Run Claude from the repo directory with full tool access
  log "🤖 Running claude --print from ${CWD}..."
  claude_exit=0
  (cd "$CWD" && claude --print --dangerously-skip-permissions "$PROMPT") 2>&1 | tee -a "$LOGFILE" || claude_exit=$?

  if [[ $claude_exit -ne 0 ]]; then
    log "❌ Claude exited with code ${claude_exit} — aborting (last-known NOT saved, same review will retry)"
    _RINSE_OUTCOME="error"
    exit 1
  fi

  # ── Critical: save this review ID as last-known ────────────────────────────
  # Without this, the next `cycle` call has no last_known, sees the same review,
  # and returns new_review again — infinite loop on the same comments.
  # pr-review.sh reads this file in load_last_known() and uses it to detect
  # whether Copilot has posted a *new* review since our last fix.
  mkdir -p "$STATE_DIR"
  echo "$review_id" > "${STATE_DIR}/pr-${PR_NUMBER}-last-review"
  log "💾 Saved last-known review ID: ${review_id}"
  stats_add_iteration "$comment_count"

  log "✓ Claude finished iteration ${iter} — cycling back for next Copilot review..."
  echo ""
  sleep 5
done

log "⚠️  Max iterations (${MAX_ITER}) reached without approval. Check ${LOGFILE} for details."
_RINSE_OUTCOME="max_iter"
exit 1
