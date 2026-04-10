#!/usr/bin/env bash
# pr-review-opencode.sh — Autonomous Copilot PR review fix loop using opencode
#
# Identical flow to pr-review-claude-v2.sh but uses `opencode run` instead of
# `claude --print`. Model provider is GitHub Copilot (configured in ~/.config/opencode/).
#
# Usage:
#   ./pr-review-opencode.sh <pr_number> [options]
#
# Options:
#   --repo  <owner/repo>          Override repo detection (default: auto-detect from --cwd)
#   --cwd   <path>                Local repo path (default: current directory)
#   --model <provider/model>      opencode model string (default: github-copilot/claude-sonnet-4.6)
#   --wait-max <seconds>          Max seconds to wait per Copilot review (default: 300)
#   --reflect                     After each fix, run reflection agent to update AGENTS.md + CLAUDE.md
#   --reflect-model <model>       Model for reflection agent (default: same as --model)
#   --dry-run                     Print startup state and exit without running opencode
#
# Requirements:
#   - opencode CLI in PATH (opencode --version)
#   - opencode authenticated with GitHub Copilot (opencode providers)
#   - gh CLI v2.88+ authenticated
#   - jq
#
# Example:
#   ./pr-review-opencode.sh 1 \
#     --repo owner/repo \
#     --cwd "/path/to/repo"
#
set -euo pipefail

# ─── Constants ────────────────────────────────────────────────────────────────

STATE_DIR="/tmp/pr-review-state"
LOGFILE="${HOME}/.pr-review-opencode.log"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

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
MODEL="github-copilot/claude-sonnet-4.6"
WAIT_MAX=300
DRY_RUN=false
REFLECT=false
REFLECT_MODEL=""
REFLECT_MAIN_BRANCH="main"
AUTO_MERGE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)                REPO="$2";                shift 2 ;;
    --cwd)                 CWD="$2";                 shift 2 ;;
    --model)               MODEL="$2";               shift 2 ;;
    --reflect)             REFLECT=true;             shift ;;
    --reflect-model)       REFLECT_MODEL="$2";       shift 2 ;;
    --reflect-main-branch) REFLECT_MAIN_BRANCH="$2"; shift 2 ;;
    --wait-max)            WAIT_MAX="$2";            shift 2 ;;
    --no-interactive)      export PR_REVIEW_NO_INTERACTIVE=true; shift ;;
    --auto-merge)          AUTO_MERGE=true; shift ;;
    --dry-run)             DRY_RUN=true;             shift ;;
    *) >&2 echo "Unknown arg: $1"; exit 1 ;;
  esac
done

# ─── Helpers ──────────────────────────────────────────────────────────────────

# log() is provided by pr-review-ui.sh (sourced above)

if [[ -z "$REPO" ]]; then
  REPO=$(cd "$CWD" && gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  if [[ -z "$REPO" ]]; then
    >&2 echo "Could not detect repo. Use --repo owner/repo or run from inside a git repo."
    exit 1
  fi
fi

STATE_FILE="${STATE_DIR}/pr-${PR_NUMBER}-last-review"

# ─── GitHub helpers ───────────────────────────────────────────────────────────

copilot_is_pending() {
  gh api "repos/${REPO}/pulls/${PR_NUMBER}" \
    --jq '[.requested_reviewers[] | select(.login | test("copilot"; "i"))] | length > 0' \
    2>/dev/null || echo "false"
}

get_latest_copilot_review() {
  gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews?per_page=100" \
    --jq '[.[] | select(.user.login | test("copilot"; "i")) | {id: .id, state: .state, submitted_at: .submitted_at}]' \
    2>/dev/null | jq -s 'add | sort_by(.submitted_at) | last // empty'
}

get_review_comments() {
  local rid="$1"
  gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews/${rid}/comments" \
    --jq '[.[] | {id: .id, path: .path, line: .original_line, body: .body, in_reply_to_id: .in_reply_to_id}]' \
    2>/dev/null | jq '[.[] | select(.in_reply_to_id == null)]'
}

request_copilot_review() {
  if gh pr edit "$PR_NUMBER" --repo "$REPO" --add-reviewer "@copilot" 2>/dev/null; then
    log "   📨 Copilot review requested via gh pr edit --add-reviewer @copilot"
    return 0
  fi
  if gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
    -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null 2>&1; then
    log "   📨 Copilot review requested via API (fallback)"
    return 0
  fi
  log "   ⚠️  Failed to request Copilot review"
  return 1
}

wait_for_review() {
  local elapsed=0 interval=15
  log "⏳ Waiting for Copilot to finish reviewing (up to ${WAIT_MAX}s)..."
  while [[ $elapsed -lt $WAIT_MAX ]]; do
    [[ "$(copilot_is_pending)" == "false" ]] && { ui_wait_clear; return 0; }
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
    [[ "$(copilot_is_pending)" == "false" ]] && { ui_wait_clear; return 0; }
    ui_wait_tick "$elapsed2" "$WAIT_MAX" "Copilot reviewing (retry)"
    local sleep_time2=$(( interval < (WAIT_MAX - elapsed2) ? interval : (WAIT_MAX - elapsed2) ))
    sleep "$sleep_time2"
    elapsed2=$(( elapsed2 + sleep_time2 ))
  done
  ui_wait_clear
  log "   ❌ Copilot still stalled after dismiss+retry"
  return 1
}

# Interactive stall menu — shown when TTY and Copilot hasn't responded
_stall_menu() {
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
        [[ "$(copilot_is_pending)" == "false" ]] && { ui_wait_clear; return 0; }
        ui_wait_tick "$elapsed3" "$WAIT_MAX" "Copilot reviewing (extended wait)"
        local sleep_time3=$(( interval < (WAIT_MAX - elapsed3) ? interval : (WAIT_MAX - elapsed3) ))
        sleep "$sleep_time3"
        elapsed3=$(( elapsed3 + sleep_time3 ))
      done
      ui_wait_clear
      # Recurse once — offer the menu again
      if [[ "$(copilot_is_pending)" == "false" ]]; then
        log "   ✓ Review arrived — continuing"
        return 0
      fi
      _stall_menu
      return $?
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
        [[ "$(copilot_is_pending)" == "false" ]] && { ui_wait_clear; return 0; }
        ui_wait_tick "$elapsed4" "$WAIT_MAX" "Copilot reviewing"
        local sleep_time4=$(( interval < (WAIT_MAX - elapsed4) ? interval : (WAIT_MAX - elapsed4) ))
        sleep "$sleep_time4"
        elapsed4=$(( elapsed4 + sleep_time4 ))
      done
      ui_wait_clear
      if [[ "$(copilot_is_pending)" == "false" ]]; then return 0; fi
      _stall_menu
      return $?
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
}

# ─── Startup ──────────────────────────────────────────────────────────────────

ui_header "opencode PR review loop  ·  ${REPO}#${PR_NUMBER}"
log "🚀 Starting opencode PR review loop"
log "   Repo:        ${REPO}#${PR_NUMBER}"
log "   Local path:  ${CWD}"
log "   Model:       ${MODEL}"
log "   Wait max:    ${WAIT_MAX}s   (unlimited iterations)"
log "   Log file:    ${LOGFILE}"

pr_json=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" 2>/dev/null)
pr_state=$(echo "$pr_json" | jq -r '.state')
merged_at=$(echo "$pr_json" | jq -r '.merged_at // ""')

if [[ "$pr_state" == "closed" && -n "$merged_at" ]]; then
  log "🎉 PR already merged — nothing to do."; exit 0
elif [[ "$pr_state" == "closed" ]]; then
  log "📕 PR closed (not merged) — nothing to do."; exit 1
fi

log "🔍 Checking existing reviews..."
mkdir -p "$STATE_DIR"

latest=$(get_latest_copilot_review)
pending=$(copilot_is_pending)

if [[ "$pending" == "true" ]]; then
  log "   Copilot review is currently in progress — will wait for it"
elif [[ -z "$latest" ]]; then
  log "   No Copilot reviews yet — will request the first one"
  rm -f "$STATE_FILE"
else
  rid=$(echo "$latest" | jq -r '.id')
  rstate=$(echo "$latest" | jq -r '.state')
  rat=$(echo "$latest" | jq -r '.submitted_at')

  if [[ "$rstate" == "APPROVED" ]]; then
    log "✅ PR already APPROVED by Copilot — nothing to do."; exit 0
  fi

  comments=$(get_review_comments "$rid")
  comment_count=$(echo "$comments" | jq 'length')
  saved=$(cat "$STATE_FILE" 2>/dev/null || echo "")

  if [[ "$comment_count" -gt 0 && "$saved" != "$rid" ]]; then
    log "   Existing review ${rid} (${rat}) has ${comment_count} unresolved comment(s)"
    log "   → Will fix these before requesting a new review"
    rm -f "$STATE_FILE"
  elif [[ "$comment_count" -eq 0 ]]; then
    log "   Existing review ${rid} has 0 comments — will request fresh review"
    echo "$rid" > "$STATE_FILE"
  else
    log "   Existing review ${rid} already seen — will request fresh review"
  fi
fi

[[ "$DRY_RUN" == true ]] && { log "[DRY RUN] Exiting."; exit 0; }
echo ""

# ─── Main loop ────────────────────────────────────────────────────────────────

iter=0

while true; do
  iter=$(( iter + 1 ))
  ui_iter_header "$iter"

  # ── Step 1: Request review if needed ──────────────────────────────────────

  pending=$(copilot_is_pending)
  latest=$(get_latest_copilot_review)
  saved=$(cat "$STATE_FILE" 2>/dev/null || echo "")
  rid=""
  [[ -n "$latest" ]] && rid=$(echo "$latest" | jq -r '.id')

  if [[ "$pending" == "false" && ( -z "$rid" || "$rid" == "$saved" ) ]]; then
    log "📨 Requesting Copilot review..."
    request_copilot_review
    sleep 3
  fi

  # ── Step 2: Wait for review ───────────────────────────────────────────────

  if ! wait_for_review; then
    log "❌ Timed out waiting for Copilot — aborting"
    exit 1
  fi

  # ── Step 3: Read result ───────────────────────────────────────────────────

  latest=$(get_latest_copilot_review)
  [[ -z "$latest" ]] && { log "⚠️  No review found — retrying"; sleep 10; continue; }

  rid=$(echo "$latest" | jq -r '.id')
  rstate=$(echo "$latest" | jq -r '.state')

  pr_state=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" --jq '.state' 2>/dev/null || echo "open")
  merged_at=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" --jq '.merged_at // ""' 2>/dev/null || echo "")
  if [[ "$pr_state" == "closed" ]]; then
    [[ -n "$merged_at" ]] && log "🎉 PR merged!" || log "📕 PR closed."
    exit 0
  fi

  if [[ "$rstate" == "APPROVED" ]]; then
    log "✅ Copilot APPROVED PR #${PR_NUMBER}! Ready to merge."
    echo "$rid" > "$STATE_FILE"
    if [[ "$AUTO_MERGE" == true ]]; then
      log "🔀 Auto-merging and deleting branch..."
      local_branch=$(git -C "$CWD" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
      base_branch=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" --jq '.base.ref' 2>/dev/null || echo "main")
      gh pr merge "$PR_NUMBER" --repo "$REPO" --squash --delete-branch
      if [[ -n "$local_branch" && "$local_branch" != "$base_branch" ]]; then
        git -C "$CWD" checkout "$base_branch" 2>/dev/null || true
        git -C "$CWD" branch -d "$local_branch" 2>/dev/null || true
      fi
      log "✅ Merged, remote branch deleted, local branch deleted."
    else
      ui_merge_menu "$PR_NUMBER" "$REPO" "$CWD"
    fi
    exit 0
  fi

  comments=$(get_review_comments "$rid")
  comment_count=$(echo "$comments" | jq 'length')

  if [[ "$comment_count" -eq 0 ]]; then
    log "✅ Clean review — 0 comments. PR #${PR_NUMBER} is ready to merge."
    echo "$rid" > "$STATE_FILE"
    if [[ "$AUTO_MERGE" == true ]]; then
      log "🔀 Auto-merging and deleting branch..."
      local_branch=$(git -C "$CWD" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
      base_branch=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" --jq '.base.ref' 2>/dev/null || echo "main")
      gh pr merge "$PR_NUMBER" --repo "$REPO" --squash --delete-branch
      if [[ -n "$local_branch" && "$local_branch" != "$base_branch" ]]; then
        git -C "$CWD" checkout "$base_branch" 2>/dev/null || true
        git -C "$CWD" branch -d "$local_branch" 2>/dev/null || true
      fi
      log "✅ Merged, remote branch deleted, local branch deleted."
    else
      ui_merge_menu "$PR_NUMBER" "$REPO" "$CWD"
    fi
    exit 0
  fi

  log "💬 ${comment_count} comment(s) in review ${rid} — invoking opencode (${MODEL})..."

  # ── Step 4: Build prompt and invoke opencode ──────────────────────────────

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

  # Launch reflection agent in background BEFORE fix agent so rules update
  # while Copilot re-reviews (zero wait cost). Reflect uses same comments.
  reflect_pid=""
  if [[ "$REFLECT" == true ]]; then
    reflect_model="${REFLECT_MODEL:-$MODEL}"
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
    ui_reflect_start "$LOGFILE"
  fi

  oc_exit=0
  (cd "$CWD" && opencode run --model "$MODEL" --dangerously-skip-permissions "$PROMPT") \
    2>&1 | tee -a "$LOGFILE" || oc_exit=$?

  if [[ $oc_exit -ne 0 ]]; then
    log "❌ opencode exited with code ${oc_exit} — aborting"
    if [[ -n "$reflect_pid" ]]; then
      kill "$reflect_pid" 2>/dev/null || true
      ui_reflect_done "$LOGFILE"
    fi
    exit 1
  fi

  # Wait for reflection to finish (it should complete well before next Copilot review)
  if [[ -n "$reflect_pid" ]]; then
    wait "$reflect_pid" && ui_reflect_done "$LOGFILE" || { ui_reflect_done "$LOGFILE"; log "⚠️  Reflection exited non-zero (non-fatal)"; }
  fi

  echo "$rid" > "$STATE_FILE"
  log "💾 Saved last-known review ID: ${rid}"

  log "✓ Iteration ${iter} complete — waiting for next Copilot review..."
  echo ""
  sleep 5
done
