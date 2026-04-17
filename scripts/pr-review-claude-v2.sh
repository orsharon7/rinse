#!/usr/bin/env bash
# pr-review-claude-v2.sh — Autonomous Copilot PR review fix loop
#
# Runs until Copilot approves or returns 0 comments (no hard iteration cap).
# Uses REST API to request Copilot reviews (avoids deprecated GraphQL path).
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
#   --worktree             Use a git worktree for isolation (used by orchestrator)
#   --repo-root <path>     Original repo root when --worktree is active
#   --dry-run              Print startup state and exit without running Claude
#   --json-insights        Print machine-readable JSON summary after each cycle
#
# Requirements:
#   - claude CLI in PATH
#   - gh CLI authenticated  (gh --version)
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
# STATE_DIR and LOGFILE are scoped per-repo after REPO is known (see below)

# ─── UI ───────────────────────────────────────────────────────────────────────

# shellcheck source=pr-review-ui.sh
source "${SCRIPT_DIR}/pr-review-ui.sh"

# ─── Session / distributed lock ───────────────────────────────────────────────

# shellcheck source=pr-review-session.sh
source "${SCRIPT_DIR}/pr-review-session.sh"

# ─── Insights ─────────────────────────────────────────────────────────────────

# shellcheck source=pr-review-insights.sh
source "${SCRIPT_DIR}/pr-review-insights.sh"

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
USE_WORKTREE=false
REPO_ROOT=""
JSON_INSIGHTS=false

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
    --worktree)            USE_WORKTREE=true;        shift ;;
    --repo-root)           REPO_ROOT="$2";           shift 2 ;;
    --dry-run)             DRY_RUN=true;             shift ;;
    --json-insights)       JSON_INSIGHTS=true;       shift ;;
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

# ─── Scoped state & logs (per-repo isolation for parallel runs) ───────────────

REPO_SLUG="${REPO//\//_}"  # owner/repo → owner_repo
STATE_DIR="${HOME}/.pr-review/state/${REPO_SLUG}"
LOGFILE="${HOME}/.pr-review/logs/${REPO_SLUG}-pr-${PR_NUMBER}.log"
mkdir -p "$STATE_DIR" "$(dirname "$LOGFILE")"
STATE_FILE="${STATE_DIR}/pr-${PR_NUMBER}-last-review"

# ─── Worktree isolation (optional — used by orchestrator for parallel runs) ───

WORKTREE_DIR=""
# REPO_ROOT: the original git clone path, used for reflect to avoid worktree-of-worktree.
# When --worktree is active, CWD is redirected to the worktree, REPO_ROOT stays at the clone.
[[ -z "$REPO_ROOT" ]] && REPO_ROOT="$CWD"

if [[ "$USE_WORKTREE" == true ]]; then
  # Fetch PR branch name
  PR_BRANCH=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" --jq '.head.ref' 2>/dev/null || echo "")
  if [[ -z "$PR_BRANCH" ]]; then
    >&2 echo "Could not determine PR branch — cannot create worktree"
    exit 1
  fi

  WORKTREE_DIR="/tmp/pr-review-worktrees/${REPO_SLUG}/pr-${PR_NUMBER}"
  mkdir -p "$(dirname "$WORKTREE_DIR")"

  # Cleanup function — remove the worktree on exit / signal
  # Called by _insights_exit_trap (the single unified EXIT trap).
  cleanup_pr_worktree() {
    local rc=$?
    set +e
    if [[ -n "$WORKTREE_DIR" && -d "$WORKTREE_DIR" ]]; then
      log "Cleaning up worktree at ${WORKTREE_DIR}..."
      git -C "$REPO_ROOT" worktree remove --force "$WORKTREE_DIR" 2>/dev/null || true
      rm -rf "$WORKTREE_DIR" 2>/dev/null || true
    fi
    session_clear
    if [[ "${DRY_RUN:-false}" != true ]]; then
      gh_lock_release
    fi
    local should_print_insights=false
    if [[ "${DRY_RUN:-false}" != true && -z "${_INS_OUTCOME:-}" && "${_INS_START_EPOCH:-0}" -gt 0 ]]; then
      should_print_insights=true
    fi
    if [[ -z "${_INS_OUTCOME:-}" && "${_INS_START_EPOCH:-0}" -gt 0 ]]; then
      insights_finalize "unknown"
    fi
    if [[ "$should_print_insights" == true ]]; then
      if [[ "${JSON_INSIGHTS:-false}" == true ]]; then
        insights_print --json
      else
        insights_print
      fi
    fi
  }

  # Prune stale worktree references from previous crashed runs
  git -C "$REPO_ROOT" worktree prune 2>/dev/null || true

  # Fetch and create the worktree
  log "Creating worktree for PR #${PR_NUMBER} (branch: ${PR_BRANCH})..."
  git -C "$REPO_ROOT" fetch origin "$PR_BRANCH" 2>/dev/null || {
    >&2 echo "Fatal: could not fetch origin/${PR_BRANCH}"
    exit 1
  }
  if [[ -d "$WORKTREE_DIR" ]]; then
    git -C "$REPO_ROOT" worktree remove --force "$WORKTREE_DIR" 2>/dev/null || true
    rm -rf "$WORKTREE_DIR" 2>/dev/null || true
  fi
  # Use a PR-number-namespaced local branch to avoid clobbering an existing
  # local branch with the same name as the PR head branch.
  local_wt_branch="pr-review/${PR_NUMBER}/${PR_BRANCH}"
  git -C "$REPO_ROOT" worktree add -B "$local_wt_branch" "$WORKTREE_DIR" "origin/${PR_BRANCH}" 2>/dev/null || {
    >&2 echo "Fatal: could not create worktree for branch ${PR_BRANCH}"
    exit 1
  }
  git -C "$WORKTREE_DIR" branch --set-upstream-to="origin/${PR_BRANCH}" "$local_wt_branch" 2>/dev/null || {
    >&2 echo "Fatal: could not set upstream for local branch ${local_wt_branch} to origin/${PR_BRANCH}"
    exit 1
  }

  CWD="$WORKTREE_DIR"
  log "   Worktree ready: ${WORKTREE_DIR}"
fi

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
    2>/dev/null | jq -s 'add // [] | sort_by(.submitted_at) | last // empty'
}

# Returns top-level review comments as JSON array
get_review_comments() {
  local rid="$1"
  gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews/${rid}/comments" \
    --jq '[.[] | {id: .id, path: .path, line: .original_line, body: .body, in_reply_to_id: .in_reply_to_id}]' \
    2>/dev/null | jq -s 'add // [] | [.[] | select(.in_reply_to_id == null)]'
}

# Request Copilot review via REST API
# Note: gh pr edit --add-reviewer uses GraphQL updatePullRequest which triggers
# "Projects (classic) is being deprecated" warnings — use REST instead (see #14)
request_copilot_review() {
  if gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
    -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null 2>&1; then
    log "   📨 Copilot review requested via REST API"
    return 0
  fi
  log "   ⚠️  Failed to request Copilot review"
  return 1
}

react_eyes_to_review() {
  local review_id="$1"
  local node_id
  node_id=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}/reviews/${review_id}" --jq '.node_id' 2>/dev/null || echo "")
  [[ -z "$node_id" || "$node_id" == "null" ]] && return
  gh api graphql -f query="mutation { addReaction(input: {subjectId: \"${node_id}\", content: EYES}) { reaction { content } } }" >/dev/null 2>&1 \
    && log "   👀 Reacted to review ${review_id}" \
    || true
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

  # Grace check: review may have arrived in the last poll window — check before acting
  if [[ "$(copilot_is_pending)" == "false" ]]; then
    log "   ✓ Review arrived just before timeout — continuing"
    return 0
  fi

  # Stall confirmed — ask user what to do (interactive) or auto-dismiss (non-interactive)
  if [[ "$_UI_TTY" == true ]]; then
    _stall_menu
    return $?
  else
    log "   ⚠️  Stalled after ${WAIT_MAX}s — dismissing and re-requesting (non-interactive)..."
    _dismiss_and_rerequst
    return $?
  fi
}

# Called when Copilot is confirmed stalled: dismiss + re-request + wait once more
_dismiss_and_rerequst() {
  gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
    -X DELETE --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null 2>&1 || true
  sleep 2
  request_copilot_review || true
  sleep 5

  local elapsed2=0 interval=15
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
  log "   ❌ Copilot still stalled after dismiss+retry"
  return 1
}

# Interactive stall menu — shown when TTY and Copilot hasn't responded.
# Uses a loop instead of recursion to avoid stack overflow on repeated "Wait again".
_stall_menu() {
  while true; do
    echo "" >&2
    log "   ⚠️  Copilot hasn't responded after ${WAIT_MAX}s"

    local choice
    choice=$(_ui_arrow_menu \
      "Wait again  (another ${WAIT_MAX}s)" \
      "Check now  (single poll, then keep waiting)" \
      "Dismiss & re-request  (restart Copilot review)" \
      "Stop the cycle  (exit)")

    case "$choice" in
      0)  # Wait again
        log "   ⏳ Waiting another ${WAIT_MAX}s..."
        local elapsed3=0 interval=15
        while [[ $elapsed3 -lt $WAIT_MAX ]]; do
          local p3
          p3=$(copilot_is_pending)
          [[ "$p3" == "false" ]] && { ui_wait_clear; return 0; }
          ui_wait_tick "$elapsed3" "$WAIT_MAX" "Copilot reviewing (extended wait)"
          local sleep_time3=$(( interval < (WAIT_MAX - elapsed3) ? interval : (WAIT_MAX - elapsed3) ))
          sleep "$sleep_time3"
          elapsed3=$(( elapsed3 + sleep_time3 ))
        done
        ui_wait_clear
        if [[ "$(copilot_is_pending)" == "false" ]]; then
          log "   ✓ Review arrived — continuing"
          return 0
        fi
        # Loop back to show menu again
        ;;
      1)  # Check now
        ui_wait_clear
        if [[ "$(copilot_is_pending)" == "false" ]]; then
          log "   ✓ Review found — continuing"
          return 0
        fi
        log "   Still pending — resuming wait..."
        local elapsed4=0 interval=15
        while [[ $elapsed4 -lt $WAIT_MAX ]]; do
          local p4
          p4=$(copilot_is_pending)
          [[ "$p4" == "false" ]] && { ui_wait_clear; return 0; }
          ui_wait_tick "$elapsed4" "$WAIT_MAX" "Copilot reviewing"
          local sleep_time4=$(( interval < (WAIT_MAX - elapsed4) ? interval : (WAIT_MAX - elapsed4) ))
          sleep "$sleep_time4"
          elapsed4=$(( elapsed4 + sleep_time4 ))
        done
        ui_wait_clear
        if [[ "$(copilot_is_pending)" == "false" ]]; then return 0; fi
        # Loop back to show menu again
        ;;
      2)  # Dismiss & re-request
        log "   🔄 Dismissing and re-requesting Copilot review..."
        _dismiss_and_rerequst
        return $?
        ;;
      3)  # Stop
        log "   🛑 Cycle stopped by user."
        return 1
        ;;
    esac
  done
}

# ─── Startup ──────────────────────────────────────────────────────────────────

ui_header "Claude PR review loop v2  ·  ${REPO}#${PR_NUMBER}"
log "🚀 Starting Claude PR review loop v2"
log "   Repo:        ${REPO}#${PR_NUMBER}"
log "   Local path:  ${CWD}"
log "   Model:       ${MODEL}"
log "   Wait max:    ${WAIT_MAX}s   (unlimited iterations)"
log "   Log file:    ${LOGFILE}"

# ── Session init & crash recovery ────────────────────────────────────────────

session_init "$REPO" "$PR_NUMBER"

# ── Insights init ─────────────────────────────────────────────────────────────
insights_init "$PR_NUMBER" "$REPO" "$MODEL"

# Centralize insights finalization so every exit path (including early exits)
# produces a summary when --json-insights is active.  Each exit path sets
# _INSIGHTS_OUTCOME to the semantic label; the trap derives a fallback from the
# exit code when it is not set.  A done-guard prevents double-finalization for
# paths that already called insights_finalize before the trap fires.
_INSIGHTS_OUTCOME=""
_INSIGHTS_DONE=false
_insights_exit_trap() {
  [[ "$_INSIGHTS_DONE" == true ]] && return
  _INSIGHTS_DONE=true
  local exit_code="${1:-$?}"
  # Consolidated cleanup: always run worktree cleanup + session/lock release
  if [[ "$USE_WORKTREE" == true ]]; then
    cleanup_pr_worktree
  else
    session_clear
    gh_lock_release
  fi
  local outcome="${_INSIGHTS_OUTCOME:-}"
  if [[ -z "$outcome" ]]; then
    case "$exit_code" in
      0) outcome="clean" ;;
      *) outcome="error" ;;
    esac
  fi
  insights_finalize "$outcome"
  if [[ "$JSON_INSIGHTS" == true ]]; then
    insights_print --json
  else
    insights_print
  fi
}
trap '_insights_exit_trap $?' EXIT

if session_recover; then
  log "⚠️  Previous session crashed (iter ${RECOVER_ITER}, last review: ${RECOVER_REVIEW_ID:-none})"
  log "   Recovering — will resume from last known state"
  if [[ -n "$RECOVER_REVIEW_ID" ]]; then
    echo "$RECOVER_REVIEW_ID" > "$STATE_FILE"
    log "   Restored last-known review ID: ${RECOVER_REVIEW_ID}"
  fi
fi

if [[ "$USE_WORKTREE" == false ]]; then
  _cleanup_on_exit() {
    local rc=$?
    local should_print_insights=true
    set +e

    session_clear
    if [[ "${DRY_RUN:-false}" != true ]]; then
      gh_lock_release
    fi

    # Finalize and print insights only if not already handled by an explicit exit path.
    if [[ -z "${_INS_OUTCOME:-}" ]]; then
      local outcome="error"
      [[ $rc -eq 0 ]] && outcome="clean"
      insights_finalize "$outcome"
    else
      should_print_insights=false
    fi

    if [[ "${DRY_RUN:-false}" != true && "$should_print_insights" == true ]]; then
      if [[ "${JSON_INSIGHTS:-false}" == true ]]; then
        insights_print --json
      else
        insights_print
      fi
    fi
  }
  trap _cleanup_on_exit EXIT
fi

if [[ "$DRY_RUN" != true ]]; then
  if ! gh_lock_acquire; then
    log "🔒 Another RINSE runner already holds the lock for PR #${PR_NUMBER} — exiting to avoid duplicate run"
    insights_finalize "skipped"
    if [[ "${JSON_INSIGHTS:-false}" == true ]]; then
      insights_print --json
    else
      insights_print
    fi
    exit 2
  fi
  log "🔑 Acquired distributed lock for PR #${PR_NUMBER}"
fi

session_update 0 "$(cat "$STATE_FILE" 2>/dev/null || echo "")"

# Check PR state (single fetch)
_pr_json=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" 2>/dev/null || echo '{}')
pr_state=$(echo "$_pr_json" | jq -r '.state // "unknown"')
merged_at=$(echo "$_pr_json" | jq -r '.merged_at // ""')

if [[ "$pr_state" == "closed" && -n "$merged_at" ]]; then
  log "🎉 PR already merged — nothing to do."
  insights_finalize "already_merged"
  [[ "${JSON_INSIGHTS:-false}" == true ]] && insights_print --json || insights_print
  exit 0
elif [[ "$pr_state" == "closed" ]]; then
  log "📕 PR is closed (not merged) — nothing to do."
  insights_finalize "closed"
  [[ "${JSON_INSIGHTS:-false}" == true ]] && insights_print --json || insights_print
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
    insights_finalize "approved"
    [[ "${JSON_INSIGHTS:-false}" == true ]] && insights_print --json || insights_print
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
  session_update "$iter" "$(cat "$STATE_FILE" 2>/dev/null || echo "")"

  # ── Step 1: Ensure a review is in progress / get latest ───────────────

  ui_step 1 "Check review status"

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

  # ── Step 2: Wait for Copilot to finish ────────────────────────────

  ui_step 2 "Wait for Copilot review"

  if ! wait_for_review; then
    log "❌ Timed out waiting for Copilot — aborting"
    insights_finalize "stalled"
    if [[ "${JSON_INSIGHTS:-false}" == true ]]; then
      insights_print --json
    else
      insights_print
    fi
    exit 1
  fi

  # ── Step 3: Read the new review ───────────────────────────────────

  ui_step 3 "Read review result"

  latest=$(get_latest_copilot_review)
  if [[ -z "$latest" ]]; then
    log "⚠️  No review found after wait — retrying"
    sleep 10
    continue
  fi

  rid=$(echo "$latest" | jq -r '.id')
  rstate=$(echo "$latest" | jq -r '.state')

  # Signal on GitHub that we've seen the review
  react_eyes_to_review "$rid"

  # Check PR state (may have been merged/closed while we waited — single fetch)
  _pr_json=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" 2>/dev/null || echo '{}')
  pr_state=$(echo "$_pr_json" | jq -r '.state // "open"')
  merged_at=$(echo "$_pr_json" | jq -r '.merged_at // ""')
  if [[ "$pr_state" == "closed" ]]; then
    [[ -n "$merged_at" ]] && { log "🎉 PR merged!"; _INSIGHTS_OUTCOME="merged"; } || { log "📕 PR closed."; _INSIGHTS_OUTCOME="closed"; }
    exit 0
  fi

  if [[ "$rstate" == "APPROVED" ]]; then
    ui_outcome "✅" "Copilot APPROVED PR #${PR_NUMBER}! Ready to merge."
    log "✅ Copilot APPROVED PR #${PR_NUMBER}! Ready to merge."
    echo "$rid" > "$STATE_FILE"
    insights_finalize "approved"
    if [[ "${JSON_INSIGHTS:-false}" == true ]]; then
      insights_print --json
    else
      insights_print
    fi
    ui_merge_menu "$PR_NUMBER" "$REPO" "$CWD"
    exit 0
  fi

  comments=$(get_review_comments "$rid")
  comment_count=$(echo "$comments" | jq 'length')

  if [[ "$comment_count" -eq 0 ]]; then
    ui_outcome "✅" "Clean review — 0 comments. PR #${PR_NUMBER} is ready to merge."
    log "✅ Clean review — 0 comments. PR #${PR_NUMBER} is ready to merge."
    echo "$rid" > "$STATE_FILE"
    insights_finalize "clean"
    if [[ "${JSON_INSIGHTS:-false}" == true ]]; then
      insights_print --json
    else
      insights_print
    fi
    ui_merge_menu "$PR_NUMBER" "$REPO" "$CWD"
    exit 0
  fi

  ui_outcome "💬" "${comment_count} comment(s) in review ${rid}" "$GUM_WARN"
  log "💬 ${comment_count} comment(s) in review ${rid} — invoking Claude (${MODEL})..."

  # Record insights for this iteration (classify comments by category)
  insights_record_iteration "$comment_count" "$comments_json"

  # ── Step 4: Build prompt and invoke Claude ────────────────────────────

  ui_step 4 "Fix comments with Claude (${MODEL})"

  comments_json=$(echo "$comments" | jq '.')

  # Record insights for this iteration (classify comments by category)
  insights_record_iteration "$comment_count" "$comments_json"

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
   gh api repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}'
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
  reflect_log=""
  if [[ "$REFLECT" == true ]]; then
    reflect_model="${REFLECT_MODEL:-github-copilot/claude-sonnet-4.6}"
    reflect_log="${HOME}/.pr-review/logs/${REPO_SLUG}-pr-${PR_NUMBER}-reflect.log"
    ui_reflect_log "starting  (model: ${reflect_model} → ${REFLECT_MAIN_BRANCH})"
    export REFLECT_COMMENTS_JSON="$comments_json"
    bash "${SCRIPT_DIR}/pr-review-reflect.sh" "$PR_NUMBER" \
      --repo "$REPO" --cwd "$REPO_ROOT" \
      --review-id "$rid" \
      --main-branch "$REFLECT_MAIN_BRANCH" \
      --model "$reflect_model" \
      --agent opencode \
      >/dev/null 2>&1 &
    reflect_pid=$!
  fi

  claude_exit=0
  (cd "$CWD" && claude --print --dangerously-skip-permissions --model "$MODEL" "$PROMPT") \
    2>&1 | tee -a "$LOGFILE" || claude_exit=$?

  if [[ $claude_exit -ne 0 ]]; then
    log "❌ Claude exited with code ${claude_exit} — aborting"
    if [[ -n "$reflect_pid" ]]; then
      kill "$reflect_pid" 2>/dev/null || true
      ui_reflect_log "killed (claude failed)" false
    fi
    insights_finalize "error"
    if [[ "${JSON_INSIGHTS:-false}" == true ]]; then
      insights_print --json
    else
      insights_print
    fi
    exit 1
  fi

  if [[ -n "$reflect_pid" ]]; then
    if wait "$reflect_pid"; then
      reflect_summary=$(grep -E '\[reflect\].*(Reflection complete|No changes|No top-level)' "$reflect_log" 2>/dev/null | tail -1 | sed 's/^.*\[reflect\] //' || echo "done")
      ui_reflect_log "$reflect_summary"
    else
      # Surface the last error from the per-PR reflect log so it's visible in the TUI
      reflect_err=""
      reflect_err=$(tail -1 "$reflect_log" 2>/dev/null | tr -d '\n' || echo "")
      ui_reflect_log "exited non-zero — ${reflect_err:-check ${reflect_log}}" false
    fi
  fi

  # Save last-known review ID so next iteration knows to wait for a fresh review
  echo "$rid" > "$STATE_FILE"
  log "💾 Saved last-known review ID: ${rid}"

  log "✓ Iteration ${iter} complete — waiting for next Copilot review..."
  echo ""
  sleep 5
done
