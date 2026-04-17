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
#   --reflect                     After each fix, run reflection agent to update AGENTS.md
#   --reflect-model <model>       Model for reflection agent (default: same as --model)
#   --reflect-optimize            After auto-merge, run an optimize pass to consolidate rules
#   --worktree                    Use a git worktree for isolation (used by orchestrator)
#   --repo-root <path>            Original repo root when --worktree is active
#   --dry-run                     Print startup state and exit without running opencode
#
# Requirements:
#   - opencode CLI in PATH (opencode --version)
#   - opencode authenticated with GitHub Copilot (opencode providers)
#   - gh CLI authenticated
#   - jq
#
# Example:
#   ./pr-review-opencode.sh 1 \
#     --repo owner/repo \
#     --cwd "/path/to/repo"
#
set -euo pipefail

# ─── Constants ────────────────────────────────────────────────────────────────

# STATE_DIR and LOGFILE are scoped per-repo after REPO is known (see below)
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ─── Session metrics (written on EXIT) ────────────────────────────────────────

# Generate a UUID v4 without external tools.
_gen_uuid() {
  local raw
  raw=$(od -An -tx1 -N16 /dev/urandom 2>/dev/null | tr -d ' \n') || raw=""
  if [[ ${#raw} -eq 32 ]]; then
    # Patch version (4) and variant (8-b) bits.
    raw="${raw:0:12}4${raw:13:3}$(printf '%x' "$(( (16#${raw:16:1} & 0x3) | 0x8 ))")${raw:17:3}${raw:20:12}"
    printf '%s-%s-%s-%s-%s\n' "${raw:0:8}" "${raw:8:4}" "${raw:12:4}" "${raw:16:4}" "${raw:20:12}"
  else
    # Fallback: timestamp + RANDOM (not cryptographic, sufficient for file naming)
    printf '%08x-%04x-4%03x-%04x-%012x\n' \
      "$(date +%s)" "$RANDOM" "$(( RANDOM & 0xfff ))" \
      "$(( (RANDOM & 0x3fff) | 0x8000 ))" "$(( RANDOM * RANDOM * RANDOM ))"
  fi
}

SESSION_ID="$(_gen_uuid)"
SESSION_STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
SESSION_STARTED_EPOCH="$(date +%s)"
SESSION_OUTCOME="aborted"             # updated to final outcome before EXIT writes JSON
declare -a SESSION_COMMENTS_BY_ITER=() # populated each main-loop iteration

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
REFLECT_OPTIMIZE=false  # auto-enabled when REFLECT=true
AUTO_MERGE=false
USE_WORKTREE=false
REPO_ROOT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)                REPO="$2";                shift 2 ;;
    --cwd)                 CWD="$2";                 shift 2 ;;
    --model)               MODEL="$2";               shift 2 ;;
    --reflect)             REFLECT=true; REFLECT_OPTIMIZE=true; shift ;;
    --reflect-model)       REFLECT_MODEL="$2";       shift 2 ;;
    --reflect-main-branch) REFLECT_MAIN_BRANCH="$2"; shift 2 ;;
    --reflect-optimize)    REFLECT_OPTIMIZE=true;    shift ;;  # can also be set standalone without --reflect
    --wait-max)            WAIT_MAX="$2";            shift 2 ;;
    --no-interactive)      export PR_REVIEW_NO_INTERACTIVE=true; shift ;;
    --auto-merge)          AUTO_MERGE=true; shift ;;
    --worktree)            USE_WORKTREE=true;        shift ;;
    --repo-root)           REPO_ROOT="$2";           shift 2 ;;
    --dry-run)             DRY_RUN=true;             shift ;;
    *) >&2 echo "Unknown arg: $1"; exit 1 ;;
  esac
done

# ─── Helpers ──────────────────────────────────────────────────────────────────

# log() is provided by pr-review-ui.sh (sourced above)

# Run the end-of-cycle reflection optimize pass (consolidate/deduplicate rules).
# Called after auto-merge when --reflect-optimize is set.
run_reflect_optimize() {
  local reflect_model="${REFLECT_MODEL:-$MODEL}"
  local skip_flag="${1:-}"
  log "🔧 Running reflection optimize pass (model: ${reflect_model}, target branch: ${REFLECT_MAIN_BRANCH})..."
  bash "${SCRIPT_DIR}/pr-review-reflect-optimize.sh" "$PR_NUMBER" \
    --repo "$REPO" --cwd "$REPO_ROOT" \
    --main-branch "$REFLECT_MAIN_BRANCH" \
    --model "$reflect_model" \
    --agent opencode \
    ${skip_flag:+"$skip_flag"} \
    >> "$LOGFILE" 2>&1 \
    && log "✓ Reflection optimize pass complete" \
    || log "⚠️  Reflection optimize pass exited non-zero (non-fatal)"
}

if [[ -z "$REPO" ]]; then
  REPO=$(cd "$CWD" && gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  if [[ -z "$REPO" ]]; then
    >&2 echo "Could not detect repo. Use --repo owner/repo or run from inside a git repo."
    exit 1
  fi
fi

# ─── Scoped state & logs (per-repo isolation for parallel runs) ───────────────

REPO_SLUG="${REPO//\//_}"  # owner/repo → owner_repo
STATE_DIR="/tmp/pr-review-state/${REPO_SLUG}"
LOGFILE="${HOME}/.pr-review/logs/${REPO_SLUG}-pr-${PR_NUMBER}.log"
mkdir -p "$STATE_DIR" "$(dirname "$LOGFILE")"
STATE_FILE="${STATE_DIR}/pr-${PR_NUMBER}-last-review"

# ─── Session JSON writer ──────────────────────────────────────────────────────

write_session_json() {
  # Best-effort / non-fatal: never let a failure here change the main exit
  # status or abort cleanup under set -e.
  set +e

  umask 077

  local sessions_dir="${HOME}/.rinse/sessions"
  mkdir -p "$sessions_dir" || { set -e; log "⚠️  Could not create sessions dir (non-fatal)"; return; }
  chmod 700 "$sessions_dir" || true

  local ended_at
  ended_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  local ended_epoch
  ended_epoch="$(date +%s)"
  local duration_seconds=$(( ended_epoch - SESSION_STARTED_EPOCH ))

  # Build copilot_comments_by_iteration JSON array.
  local comments_arr="["
  local total_comments=0
  local first=true
  for c in "${SESSION_COMMENTS_BY_ITER[@]+"${SESSION_COMMENTS_BY_ITER[@]}"}"; do
    [[ "$first" == true ]] && first=false || comments_arr+=","
    comments_arr+="$c"
    total_comments=$(( total_comments + c ))
  done
  comments_arr+="]"

  local estimated_saved=$(( total_comments * 240 ))
  local iterations="${#SESSION_COMMENTS_BY_ITER[@]}"

  # Fetch PR title (best-effort; non-fatal).
  local pr_title=""
  pr_title=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" --jq '.title' 2>/dev/null || echo "")

  local repo_session_slug="${REPO//\//-}"
  local date_part="${SESSION_STARTED_AT:0:10}"
  local time_part="${SESSION_STARTED_AT:11:8}"
  date_part="${date_part//-/}"
  time_part="${time_part//:/}"
  local fname="${sessions_dir}/${date_part}-${time_part}-${repo_session_slug}-PR${PR_NUMBER}-${SESSION_ID}.json"

  local approved="false"
  [[ "$SESSION_OUTCOME" == "approved" || "$SESSION_OUTCOME" == "merged" ]] && approved="true"

  local tmp_fname
  tmp_fname="$(mktemp "$(dirname "$fname")/.tmp_session_XXXXXX.json")" || { log "⚠️  Could not create temp file for session JSON (non-fatal)"; return; }
  if jq -n \
    --arg session_id      "$SESSION_ID" \
    --arg repo            "$REPO" \
    --arg pr              "$PR_NUMBER" \
    --arg pr_title        "$pr_title" \
    --arg started_at      "$SESSION_STARTED_AT" \
    --arg ended_at        "$ended_at" \
    --arg runner          "opencode" \
    --arg model           "$MODEL" \
    --arg outcome         "$SESSION_OUTCOME" \
    --argjson approved    "$approved" \
    --argjson iterations  "$iterations" \
    --argjson comments    "$comments_arr" \
    --argjson total       "$total_comments" \
    --argjson saved       "$estimated_saved" \
    --argjson duration    "$duration_seconds" \
    '{
      session_id:                    $session_id,
      repo:                          $repo,
      pr:                            $pr,
      pr_title:                      $pr_title,
      started_at:                    $started_at,
      ended_at:                      $ended_at,
      duration_seconds:              $duration,
      runner:                        $runner,
      model:                         $model,
      outcome:                       $outcome,
      approved:                      $approved,
      iterations:                    $iterations,
      copilot_comments_by_iteration: $comments,
      total_comments:                $total,
      estimated_time_saved_seconds:  $saved
    }' > "$tmp_fname"; then
    mv "$tmp_fname" "$fname" \
      && chmod 600 "$fname" \
      && log "📊 Session saved: ${fname}" \
      || { rm -f "$tmp_fname"; log "⚠️  Could not write session JSON (non-fatal)"; }
  else
    rm -f "$tmp_fname"
    log "⚠️  Could not write session JSON (non-fatal)"
  fi
  set -e
}

# Register EXIT trap AFTER REPO/LOGFILE are initialised so write_session_json
# has access to all variables. Any previously registered trap (worktree
# cleanup) will be superseded; worktree cleanup is handled inline below.
trap 'write_session_json' EXIT

# ─── Worktree isolation (optional — used by orchestrator for parallel runs) ───

WORKTREE_DIR=""
# REPO_ROOT: the original git clone path, used for reflect to avoid worktree-of-worktree.
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

  cleanup_pr_worktree() {
    if [[ -n "$WORKTREE_DIR" && -d "$WORKTREE_DIR" ]]; then
      log "Cleaning up worktree at ${WORKTREE_DIR}..."
      git -C "$REPO_ROOT" worktree remove --force "$WORKTREE_DIR" 2>/dev/null || true
      rm -rf "$WORKTREE_DIR" 2>/dev/null || true
    fi
    write_session_json
  }
  # Override the earlier EXIT trap with one that also runs worktree cleanup.
  trap cleanup_pr_worktree EXIT

  git -C "$REPO_ROOT" worktree prune 2>/dev/null || true

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

copilot_is_pending() {
  gh api "repos/${REPO}/pulls/${PR_NUMBER}" \
    --jq '[.requested_reviewers[] | select(.login | test("copilot"; "i"))] | length > 0' \
    2>/dev/null || echo "false"
}

get_latest_copilot_review() {
  gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews?per_page=100" \
    --jq '[.[] | select(.user.login | test("copilot"; "i")) | {id: .id, state: .state, submitted_at: .submitted_at}]' \
    2>/dev/null | jq -s 'add // [] | sort_by(.submitted_at) | last // empty'
}

get_review_comments() {
  local rid="$1"
  gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews/${rid}/comments" \
    --jq '[.[] | {id: .id, path: .path, line: .original_line, body: .body, in_reply_to_id: .in_reply_to_id}]' \
    2>/dev/null | jq -s 'add // [] | [.[] | select(.in_reply_to_id == null)]'
}

request_copilot_review() {
  # Use REST API directly — gh pr edit --add-reviewer uses GraphQL updatePullRequest
  # which triggers "Projects (classic) is being deprecated" warnings (see #14)
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
          [[ "$(copilot_is_pending)" == "false" ]] && { ui_wait_clear; return 0; }
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
          [[ "$(copilot_is_pending)" == "false" ]] && { ui_wait_clear; return 0; }
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

  ui_step 1 "Check review status"

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

  ui_step 2 "Wait for Copilot review"

  if ! wait_for_review; then
    log "❌ Timed out waiting for Copilot — aborting"
    SESSION_OUTCOME="aborted"
    exit 1
  fi

  # ── Step 3: Read result ───────────────────────────────────────────────────

  ui_step 3 "Read review result"

  latest=$(get_latest_copilot_review)
  [[ -z "$latest" ]] && { log "⚠️  No review found — retrying"; sleep 10; continue; }

  rid=$(echo "$latest" | jq -r '.id')
  rstate=$(echo "$latest" | jq -r '.state')

  # Signal on GitHub that we've seen the review
  react_eyes_to_review "$rid"

  # Check PR state (single fetch for state, merged_at, base.ref)
  _pr_json=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" 2>/dev/null || echo '{}')
  pr_state=$(echo "$_pr_json" | jq -r '.state // "open"')
  merged_at=$(echo "$_pr_json" | jq -r '.merged_at // ""')
  base_branch=$(echo "$_pr_json" | jq -r '.base.ref // "main"')
  if [[ "$pr_state" == "closed" ]]; then
    if [[ -n "$merged_at" ]]; then
      log "🎉 PR merged!"
      SESSION_OUTCOME="merged"
    else
      log "📕 PR closed."
      SESSION_OUTCOME="aborted"
    fi
    exit 0
  fi

  if [[ "$rstate" == "APPROVED" ]]; then
    ui_outcome "✅" "Copilot APPROVED PR #${PR_NUMBER}! Ready to merge."
    log "✅ Copilot APPROVED PR #${PR_NUMBER}! Ready to merge."
    echo "$rid" > "$STATE_FILE"
    if [[ "$AUTO_MERGE" == true ]]; then
      log "🔀 Auto-merging and deleting branch..."
      local_branch=$(git -C "$CWD" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
      gh pr merge "$PR_NUMBER" --repo "$REPO" --squash --delete-branch
      SESSION_OUTCOME="merged"
      _local_deleted=false
      if [[ -n "$local_branch" && "$local_branch" != "$base_branch" ]]; then
        if git -C "$CWD" checkout "$base_branch" 2>/dev/null; then
          git -C "$CWD" pull --ff-only origin "$base_branch" 2>/dev/null || true
          if git -C "$CWD" branch -d "$local_branch" 2>/dev/null \
              || git -C "$CWD" branch -D "$local_branch" 2>/dev/null; then
            _local_deleted=true
          else
            log "⚠️  Could not delete local branch ${local_branch}"
          fi
        else
          log "⚠️  Could not switch to ${base_branch} — skipping local branch deletion"
        fi
      fi
      if [[ "$_local_deleted" == true ]]; then
        log "✅ Merged, remote branch deleted, local branch deleted."
      else
        log "✅ Merged, remote branch deleted."
      fi
      [[ "$REFLECT_OPTIMIZE" == true ]] && run_reflect_optimize
    else
      SESSION_OUTCOME="approved"
      ui_merge_menu "$PR_NUMBER" "$REPO" "$CWD"
    fi
    exit 0
  fi

  comments=$(get_review_comments "$rid")
  comment_count=$(echo "$comments" | jq 'length')

  if [[ "$comment_count" -eq 0 ]]; then
    ui_outcome "✅" "Clean review — 0 comments. PR #${PR_NUMBER} is ready to merge."
    log "✅ Clean review — 0 comments. PR #${PR_NUMBER} is ready to merge."
    echo "$rid" > "$STATE_FILE"
    if [[ "$AUTO_MERGE" == true ]]; then
      log "🔀 Auto-merging and deleting branch..."
      local_branch=$(git -C "$CWD" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
      gh pr merge "$PR_NUMBER" --repo "$REPO" --squash --delete-branch
      SESSION_OUTCOME="merged"
      _local_deleted=false
      if [[ -n "$local_branch" && "$local_branch" != "$base_branch" ]]; then
        if git -C "$CWD" checkout "$base_branch" 2>/dev/null; then
          git -C "$CWD" pull --ff-only origin "$base_branch" 2>/dev/null || true
          if git -C "$CWD" branch -d "$local_branch" 2>/dev/null \
              || git -C "$CWD" branch -D "$local_branch" 2>/dev/null; then
            _local_deleted=true
          else
            log "⚠️  Could not delete local branch ${local_branch}"
          fi
        else
          log "⚠️  Could not switch to ${base_branch} — skipping local branch deletion"
        fi
      fi
      if [[ "$_local_deleted" == true ]]; then
        log "✅ Merged, remote branch deleted, local branch deleted."
      else
        log "✅ Merged, remote branch deleted."
      fi
      [[ "$REFLECT_OPTIMIZE" == true ]] && run_reflect_optimize
    else
      SESSION_OUTCOME="approved"
      ui_merge_menu "$PR_NUMBER" "$REPO" "$CWD"
    fi
    exit 0
  fi

  # ── Record comments for this iteration in session metrics ─────────────────
  SESSION_COMMENTS_BY_ITER+=("$comment_count")
  ui_outcome "💬" "${comment_count} comment(s) in review ${rid}" "$GUM_WARN"
  log "💬 ${comment_count} comment(s) in review ${rid} — invoking opencode (${MODEL})..."

  # ── Step 4: Build prompt and invoke opencode ──────────────────────────────

  ui_step 4 "Fix comments with opencode (${MODEL})"

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

  # Launch reflection agent in background BEFORE fix agent so rules update
  # while Copilot re-reviews (zero wait cost). Reflect uses same comments.
  reflect_pid=""
  reflect_log=""
  if [[ "$REFLECT" == true ]]; then
    reflect_model="${REFLECT_MODEL:-$MODEL}"
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
    ui_reflect_start "$LOGFILE"
  fi

  oc_exit=0
  (cd "$CWD" && opencode run --model "$MODEL" --dangerously-skip-permissions "$PROMPT") \
    2>&1 | tee -a "$LOGFILE" || oc_exit=$?

  if [[ $oc_exit -ne 0 ]]; then
    log "❌ opencode exited with code ${oc_exit} — aborting"
    SESSION_OUTCOME="error"
    if [[ -n "$reflect_pid" ]]; then
      kill "$reflect_pid" 2>/dev/null || true
      ui_reflect_log "killed (opencode failed)" false
    fi
    exit 1
  fi

  # Wait for reflection to finish (it should complete well before next Copilot review)
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

  echo "$rid" > "$STATE_FILE"
  log "💾 Saved last-known review ID: ${rid}"

  if [[ "$REFLECT_OPTIMIZE" == true ]] && (( iter % 3 == 0 )); then
    log "🔁 Running mid-cycle optimize pass (iteration ${iter})..."
    run_reflect_optimize "--skip-if-open-prs"
  fi

  log "✓ Iteration ${iter} complete — waiting for next Copilot review..."
  echo ""
  sleep 5
done
