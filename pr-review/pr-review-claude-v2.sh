#!/usr/bin/env bash
# pr-review-claude-v2.sh — Autonomous Copilot PR review fix loop
#
# Runs until Copilot approves or returns 0 comments (no hard iteration cap).
# Uses official `gh pr edit --add-reviewer @copilot` (gh CLI v2.88+).
# Model-agnostic: pass --model to override the Claude model.
#
# Usage:
#   ./pr-review-claude-v2.sh <pr_number> [options]
#
# Options:
#   --repo  <owner/repo>   Override repo detection (default: auto-detect from --cwd)
#   --cwd   <path>         Local repo path (default: current directory)
#   --model <model-id>     Claude model to use (default: claude-sonnet-4-6)
#   --wait-max <seconds>   Max seconds to wait per Copilot review cycle (default: 300)
#   --dry-run              Print startup state and exit without running Claude
#
# Requirements:
#   - claude CLI in PATH
#   - gh CLI v2.88+ authenticated  (gh --version)
#   - jq
#
# Example:
#   ./pr-review-claude-v2.sh 1 \
#     --repo owner/repo \
#     --cwd "/path/to/repo" \
#     --model claude-sonnet-4-6
#
set -euo pipefail

# ─── Constants ────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
STATE_DIR="/tmp/pr-review-state"
LOGFILE="${HOME}/.pr-review-claude.log"

# ─── UI ───────────────────────────────────────────────────────────────────────

# shellcheck source=pr-review-ui.sh
source "${SCRIPT_DIR}/pr-review-ui.sh"

# ─── Args ─────────────────────────────────────────────────────────────────────

if [[ $# -lt 1 || "$1" == "--help" || "$1" == "-h" ]]; then
  head -30 "$0" | grep '^#' | sed 's/^# \?//'
  exit 0
fi

PR_NUMBER="${1}"
shift

REPO=""
CWD="$(pwd)"
MODEL="claude-sonnet-4-6"
WAIT_MAX=300
DRY_RUN=false
REFLECT=false
REFLECT_MODEL=""
REFLECT_MAIN_BRANCH="main"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)                REPO="$2";                shift 2 ;;
    --cwd)                 CWD="$2";                 shift 2 ;;
    --model)               MODEL="$2";               shift 2 ;;
    --wait-max)            WAIT_MAX="$2";            shift 2 ;;
    --reflect)             REFLECT=true;             shift ;;
    --reflect-model)       REFLECT_MODEL="$2";       shift 2 ;;
    --reflect-main-branch) REFLECT_MAIN_BRANCH="$2"; shift 2 ;;
    --no-interactive)      export PR_REVIEW_NO_INTERACTIVE=true; shift ;;
    --dry-run)             DRY_RUN=true;             shift ;;
    *) >&2 echo "Unknown arg: $1"; exit 1 ;;
  esac
done

# ─── Helpers ──────────────────────────────────────────────────────────────────

# log() is provided by pr-review-ui.sh (sourced above)

# Detect repo from CWD if not provided
if [[ -z "$REPO" ]]; then
  REPO=$(cd "$CWD" && gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  if [[ -z "$REPO" ]]; then
    >&2 echo "Could not detect repo. Use --repo owner/repo or run from inside a git repo."
    exit 1
  fi
fi

STATE_FILE="${STATE_DIR}/pr-${PR_NUMBER}-last-review"

# ─── GitHub helpers ───────────────────────────────────────────────────────────

# Returns "true" if copilot-pull-request-reviewer[bot] is in requested_reviewers
copilot_is_pending() {
  gh api "repos/${REPO}/pulls/${PR_NUMBER}" \
    --jq '[.requested_reviewers[] | select(.login | test("copilot"; "i"))] | length > 0' \
    2>/dev/null || echo "false"
}

# Returns the latest Copilot review as JSON {id, state, submitted_at} or empty string
get_latest_copilot_review() {
  gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews?per_page=100" \
    --jq '[.[] | select(.user.login | test("copilot"; "i")) | {id: .id, state: .state, submitted_at: .submitted_at}]' \
    2>/dev/null | jq -s 'add | sort_by(.submitted_at) | last // empty'
}

# Returns top-level review comments as JSON array
get_review_comments() {
  local rid="$1"
  gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews/${rid}/comments" \
    --jq '[.[] | {id: .id, path: .path, line: .original_line, body: .body, in_reply_to_id: .in_reply_to_id}]' \
    2>/dev/null | jq '[.[] | select(.in_reply_to_id == null)]'
}

# Request Copilot review using official gh CLI method (gh v2.88+)
request_copilot_review() {
  if gh pr edit "$PR_NUMBER" --repo "$REPO" --add-reviewer "@copilot" 2>/dev/null; then
    log "   📨 Copilot review requested via gh pr edit --add-reviewer @copilot"
    return 0
  fi
  # Fallback: direct API (for older gh versions)
  if gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
    -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null 2>&1; then
    log "   📨 Copilot review requested via API (fallback)"
    return 0
  fi
  log "   ⚠️  Failed to request Copilot review"
  return 1
}

# Wait for Copilot to finish reviewing (blocks up to WAIT_MAX seconds)
# Returns 0 when done, 1 on timeout
wait_for_review() {
  local elapsed=0 interval=15
  log "⏳ Waiting for Copilot to finish reviewing (up to ${WAIT_MAX}s)..."
  while [[ $elapsed -lt $WAIT_MAX ]]; do
    local pending
    pending=$(copilot_is_pending)
    if [[ "$pending" == "false" ]]; then
      ui_wait_clear; return 0
    fi
    ui_wait_tick "$elapsed" "$WAIT_MAX" "Copilot reviewing"
    local sleep_time=$(( interval < (WAIT_MAX - elapsed) ? interval : (WAIT_MAX - elapsed) ))
    sleep "$sleep_time"
    elapsed=$(( elapsed + sleep_time ))
  done
  ui_wait_clear

  # Stall recovery: dismiss and re-request once
  log "   ⚠️  Stalled after ${WAIT_MAX}s — dismissing and re-requesting..."
  gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
    -X DELETE --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null 2>&1 || true
  sleep 2
  request_copilot_review || true
  sleep 5

  local elapsed2=0
  while [[ $elapsed2 -lt $WAIT_MAX ]]; do
    local pending2
    pending2=$(copilot_is_pending)
    if [[ "$pending2" == "false" ]]; then
      ui_wait_clear; return 0
    fi
    ui_wait_tick "$elapsed2" "$WAIT_MAX" "Copilot reviewing (retry)"
    local sleep_time2=$(( interval < (WAIT_MAX - elapsed2) ? interval : (WAIT_MAX - elapsed2) ))
    sleep "$sleep_time2"
    elapsed2=$(( elapsed2 + sleep_time2 ))
  done
  ui_wait_clear

  log "   ❌ Copilot still stalled after $(( WAIT_MAX * 2 ))s"
  return 1
}

# ─── Startup ──────────────────────────────────────────────────────────────────

ui_header "Claude PR review loop v2  ·  ${REPO}#${PR_NUMBER}"
log "🚀 Starting Claude PR review loop v2"
log "   Repo:        ${REPO}#${PR_NUMBER}"
log "   Local path:  ${CWD}"
log "   Model:       ${MODEL}"
log "   Wait max:    ${WAIT_MAX}s   (unlimited iterations)"
log "   Log file:    ${LOGFILE}"

# Check PR state
pr_state=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" --jq '.state' 2>/dev/null || echo "unknown")
merged_at=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" --jq '.merged_at // ""' 2>/dev/null || echo "")

if [[ "$pr_state" == "closed" && -n "$merged_at" ]]; then
  log "🎉 PR already merged — nothing to do."
  exit 0
elif [[ "$pr_state" == "closed" ]]; then
  log "📕 PR is closed (not merged) — nothing to do."
  exit 1
fi

# Assess what review state we're starting from
log "🔍 Checking existing reviews..."
mkdir -p "$STATE_DIR"

latest=$(get_latest_copilot_review)
pending=$(copilot_is_pending)

if [[ "$pending" == "true" ]]; then
  log "   Copilot review is currently in progress — will wait for it"
elif [[ -z "$latest" ]]; then
  log "   No Copilot reviews yet — will request the first one"
  # Clear any stale state file
  rm -f "$STATE_FILE"
else
  rid=$(echo "$latest" | jq -r '.id')
  rstate=$(echo "$latest" | jq -r '.state')
  rat=$(echo "$latest" | jq -r '.submitted_at')

  if [[ "$rstate" == "APPROVED" ]]; then
    log "✅ PR already APPROVED by Copilot — nothing to do."
    exit 0
  fi

  comments=$(get_review_comments "$rid")
  comment_count=$(echo "$comments" | jq 'length')
  saved_id="${STATE_FILE}"
  saved=$(cat "$STATE_FILE" 2>/dev/null || echo "")

  if [[ "$comment_count" -gt 0 && ("$saved" != "$rid" || ! -f "$STATE_FILE") ]]; then
    log "   Existing review ${rid} (${rat}) has ${comment_count} unresolved comment(s)"
    log "   → Will fix these before requesting a new review"
    # Remove state file so the loop processes this review
    rm -f "$STATE_FILE"
  elif [[ "$comment_count" -eq 0 ]]; then
    log "   Existing review ${rid} has 0 comments — seeding state, will request fresh review"
    echo "$rid" > "$STATE_FILE"
  else
    log "   Existing review ${rid} already seen (state file matches) — will request fresh review"
  fi
fi

[[ "$DRY_RUN" == true ]] && { log "[DRY RUN] Exiting."; exit 0; }
echo ""

# ─── Main loop ────────────────────────────────────────────────────────────────

iter=0

while true; do
  iter=$(( iter + 1 ))
  ui_iter_header "$iter"

  # ── Step 1: Ensure a review is in progress / get latest ───────────────────

  pending=$(copilot_is_pending)
  latest=$(get_latest_copilot_review)
  saved=$(cat "$STATE_FILE" 2>/dev/null || echo "")

  if [[ "$pending" == "false" ]]; then
    rid=""
    [[ -n "$latest" ]] && rid=$(echo "$latest" | jq -r '.id')

    # Request a new review if: no review yet, or latest matches last-known (already processed)
    if [[ -z "$rid" || "$rid" == "$saved" ]]; then
      log "📨 Requesting Copilot review..."
      request_copilot_review
      sleep 3
    fi
  fi

  # ── Step 2: Wait for Copilot to finish ────────────────────────────────────

  if ! wait_for_review; then
    log "❌ Timed out waiting for Copilot — aborting"
    exit 1
  fi

  # ── Step 3: Read the new review ───────────────────────────────────────────

  latest=$(get_latest_copilot_review)
  if [[ -z "$latest" ]]; then
    log "⚠️  No review found after wait — retrying"
    sleep 10
    continue
  fi

  rid=$(echo "$latest" | jq -r '.id')
  rstate=$(echo "$latest" | jq -r '.state')

  # Check PR state (may have been merged/closed while we waited)
  pr_state=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" --jq '.state' 2>/dev/null || echo "open")
  merged_at=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" --jq '.merged_at // ""' 2>/dev/null || echo "")
  if [[ "$pr_state" == "closed" ]]; then
    [[ -n "$merged_at" ]] && log "🎉 PR merged!" || log "📕 PR closed."
    exit 0
  fi

  if [[ "$rstate" == "APPROVED" ]]; then
    log "✅ Copilot APPROVED PR #${PR_NUMBER}! Ready to merge."
    echo "$rid" > "$STATE_FILE"
    ui_merge_menu "$PR_NUMBER" "$REPO" "$CWD"
    exit 0
  fi

  comments=$(get_review_comments "$rid")
  comment_count=$(echo "$comments" | jq 'length')

  if [[ "$comment_count" -eq 0 ]]; then
    log "✅ Clean review — 0 comments. PR #${PR_NUMBER} is ready to merge."
    echo "$rid" > "$STATE_FILE"
    ui_merge_menu "$PR_NUMBER" "$REPO" "$CWD"
    exit 0
  fi

  log "💬 ${comment_count} comment(s) in review ${rid} — invoking Claude (${MODEL})..."

  # ── Step 4: Build prompt and invoke Claude ────────────────────────────────

  comments_json=$(echo "$comments" | jq '.')

  read -r -d '' PROMPT << PROMPT_EOF || true
You are fixing GitHub Copilot code review comments on PR #${PR_NUMBER} in ${REPO}.

Local repo directory: ${CWD}
Review ID: ${rid}
Total top-level comments: ${comment_count}

## Review comments (JSON):
\`\`\`json
${comments_json}
\`\`\`

Each comment has: id, path (file), line, body (the review text), in_reply_to_id (null = top-level).

## Your task

1. For each top-level comment (in_reply_to_id == null):
   a. Read \`${CWD}/<path>\`
   b. Fix the issue at/around the given line
   c. Make the minimal targeted change only

2. Commit and push all fixes at once:
   \`\`\`bash
   cd "${CWD}" && git add -A && git commit -m "fix: address Copilot review comments" && git push
   \`\`\`
   (Skip commit/push if there are genuinely no code changes needed.)

3. Request a new Copilot review:
   \`\`\`bash
   gh pr edit ${PR_NUMBER} --repo ${REPO} --add-reviewer @copilot
   \`\`\`

4. Reply to every top-level comment:
   \`\`\`bash
   gh api repos/${REPO}/pulls/${PR_NUMBER}/comments/<id>/replies -X POST -f body="Fixed: <description> ✅"
   \`\`\`

## Rules
- Fix all comments before committing (one commit for all fixes)
- Only change what each comment asks — no refactoring beyond the comment scope
- Always request a new Copilot review after pushing (step 3)
- Reply to every top-level comment (step 4)
- If a comment is already fixed in the current code, still reply to confirm it
PROMPT_EOF

  # Launch reflection agent in background alongside fix agent
  reflect_pid=""
  if [[ "$REFLECT" == true ]]; then
    reflect_model="${REFLECT_MODEL:-github-copilot/claude-sonnet-4.6}"
    log "🔍 Launching reflection agent in background (model: ${reflect_model}, target branch: ${REFLECT_MAIN_BRANCH})..."
    export REFLECT_COMMENTS_JSON="$comments_json"
    bash "${SCRIPT_DIR}/pr-review-reflect.sh" "$PR_NUMBER" \
      --repo "$REPO" --cwd "$CWD" \
      --review-id "$rid" \
      --main-branch "$REFLECT_MAIN_BRANCH" \
      --model "$reflect_model" \
      --agent opencode \
      >> "$LOGFILE" 2>&1 &
    reflect_pid=$!
  fi

  claude_exit=0
  (cd "$CWD" && claude --print --dangerously-skip-permissions --model "$MODEL" "$PROMPT") \
    2>&1 | tee -a "$LOGFILE" || claude_exit=$?

  if [[ $claude_exit -ne 0 ]]; then
    log "❌ Claude exited with code ${claude_exit} — aborting"
    [[ -n "$reflect_pid" ]] && kill "$reflect_pid" 2>/dev/null || true
    exit 1
  fi

  if [[ -n "$reflect_pid" ]]; then
    wait "$reflect_pid" && log "✓ Reflection complete" || log "⚠️  Reflection exited non-zero (non-fatal)"
  fi

  # Save last-known review ID so next iteration knows to wait for a fresh review
  echo "$rid" > "$STATE_FILE"
  log "💾 Saved last-known review ID: ${rid}"

  log "✓ Iteration ${iter} complete — waiting for next Copilot review..."
  echo ""
  sleep 5
done
