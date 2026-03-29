#!/usr/bin/env bash
# pr-review.sh — Full Copilot PR review lifecycle tool
#
# Subcommands:
#   status    — Check review state (pending / new_review / approved / no_reviews)
#   comments  — List all unresolved Copilot comments on the PR
#
# Both `status` (on new_review) and `comments` automatically react with 👀 to
# each comment, so the PR author can see they've been acknowledged.
#   reply     — Reply to a specific comment by ID
#   reply-all — Reply to all comments in latest review (reads JSON from stdin)
#   request   — Request Copilot review (only if not already pending)
#   push      — Commit, push, and request review in one step
#   cycle       — Full cycle: wait for review → show comments (used by agents)
#   clear-state — Delete the local state file for this PR (reset last-known)
#   resolve-comment — Mark a comment as resolved (comment_id → commit_sha) in the watch file
#   watch       — Add a PR to the watch list (for async polling)
#   unwatch     — Remove a PR from the watch list
#   poll-all    — Check all watched PRs, output results, auto-retry errors
#
# Usage:
#   ./scripts/pr-review.sh <pr_number> status [--wait [<seconds>]]
#   ./scripts/pr-review.sh <pr_number> comments [--review-id <id>]
#   ./scripts/pr-review.sh <pr_number> reply <comment_id> <body>
#   ./scripts/pr-review.sh <pr_number> reply-all < replies.json
#   ./scripts/pr-review.sh <pr_number> request
#   ./scripts/pr-review.sh <pr_number> push [-m <message>]
#   ./scripts/pr-review.sh <pr_number> cycle [--wait <seconds>]
#   ./scripts/pr-review.sh <pr_number> clear-state
#   ./scripts/pr-review.sh <pr_number> resolve-comment <comment_id> <commit_sha>
#   ./scripts/pr-review.sh <pr_number> watch --repo <owner/repo>
#   ./scripts/pr-review.sh <pr_number> unwatch --repo <owner/repo>
#   ./scripts/pr-review.sh poll-all [--dry-run]
#
# Global flags (before or after subcommand):
#   --repo <owner/repo>        Override repo detection
#   --last-known <review_id>   Skip if latest review matches this ID
#   --no-color                 Suppress emoji in stderr progress messages (historical flag name; does not affect ANSI colors)
#
# reply-all stdin format (JSON array):
#   [{"comment_id": 123, "body": "Fixed in abc123"}, ...]
#
# Statuses (from `status`):
#   pending      — Copilot is actively reviewing
#   new_review   — New review with comments
#   approved     — Copilot approved
#   no_change    — Latest review matches --last-known
#   no_reviews   — No Copilot reviews exist
#   merged       — PR already merged
#   closed       — PR closed without merge
#   error        — API error or PR not found
#
# All output is JSON (stdout) with NUL bytes (\\000) stripped. Progress/logs go to stderr.

set -uo pipefail

# ─── Constants ────────────────────────────────────────────────────────────────

WATCH_FILE="${PR_REVIEW_WATCH_FILE:-${HOME}/.pr-review-watches.json}"
STATE_DIR="/tmp/pr-review-state"
mkdir -p "$STATE_DIR"

# ─── Arg parsing ──────────────────────────────────────────────────────────────

# poll-all doesn't need a PR number
if [[ "${1:-}" == "poll-all" ]]; then
  PR_NUMBER=""
  SUBCOMMAND="poll-all"
  shift
else
  PR_NUMBER="${1:?Usage: pr-review.sh <pr_number> <subcommand> [options]}"
  shift
  SUBCOMMAND="${1:-status}"
  shift || true
fi

LAST_KNOWN=""
REPO=""
WAIT=0
WAIT_MAX=300
REVIEW_ID=""
COMMENT_ID=""
REPLY_BODY=""
COMMIT_MSG=""
NO_COLOR=0
DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --last-known) LAST_KNOWN="$2"; shift 2 ;;
    --repo) REPO="$2"; shift 2 ;;
    --review-id) REVIEW_ID="$2"; shift 2 ;;
    --no-color) NO_COLOR=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    --wait)
      WAIT=1
      if [[ $# -ge 2 && "$2" =~ ^[0-9]+$ ]]; then
        WAIT_MAX="$2"; shift 2
      else
        shift
      fi
      ;;
    -m) COMMIT_MSG="$2"; shift 2 ;;
    *)
      # Positional args for reply and resolve-comment subcommands
      if [[ "$SUBCOMMAND" == "reply" && -z "$COMMENT_ID" ]]; then
        COMMENT_ID="$1"; shift
      elif [[ "$SUBCOMMAND" == "reply" && -z "$REPLY_BODY" ]]; then
        REPLY_BODY="$1"; shift
      elif [[ "$SUBCOMMAND" == "resolve-comment" && -z "$COMMENT_ID" ]]; then
        COMMENT_ID="$1"; shift
      elif [[ "$SUBCOMMAND" == "resolve-comment" && -z "$REPLY_BODY" ]]; then
        REPLY_BODY="$1"; shift
      else
        >&2 echo "Unknown arg: $1"; exit 1
      fi
      ;;
  esac
done

# ─── Repo detection (skip for poll-all — it reads repos from watch file) ──────

if [[ "$SUBCOMMAND" != "poll-all" && -z "$REPO" ]]; then
  REPO=$(gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  if [[ -z "$REPO" ]]; then
    echo '{"status":"error","message":"Could not detect repo. Use --repo owner/repo"}' | tr -d '\000'
    exit 1
  fi
fi

# ─── State file helpers ───────────────────────────────────────────────────────

state_file() {
  echo "${STATE_DIR}/pr-${PR_NUMBER}-last-review"
}

load_last_known() {
  # Auto-load last-known from state file if not provided via CLI
  if [[ -z "$LAST_KNOWN" && -f "$(state_file)" ]]; then
    LAST_KNOWN=$(cat "$(state_file)")
  fi
}

save_last_known() {
  local rid="$1"
  echo "$rid" > "$(state_file)"
}

# ─── Reactions ─────────────────────────────────────────────────────────────────

# React with 👀 to the review summary (the main review comment, not individual comments)
react_eyes_to_review() {
  local review_id="$1"
  # Get the review's node_id for GraphQL
  local node_id
  node_id=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}/reviews/${review_id}" --jq '.node_id' 2>/dev/null)
  if [[ -z "$node_id" || "$node_id" == "null" ]]; then
    >&2 echo "  [warn] Could not get node_id for review ${review_id}"
    return
  fi
  if gh api graphql -f query="mutation { addReaction(input: {subjectId: \"${node_id}\", content: EYES}) { reaction { content } } }" >/dev/null 2>&1; then
    if [[ "$NO_COLOR" -eq 0 ]]; then
      >&2 echo "  👀 Reacted to review ${review_id}"
    else
      >&2 echo "  [eyes] Reacted to review ${review_id}"
    fi
  else
    if [[ "$NO_COLOR" -eq 0 ]]; then
      >&2 echo "  ⚠️  Failed to react to review ${review_id}"
    else
      >&2 echo "  [warn] Failed to react to review ${review_id}"
    fi
  fi
}

# ─── Helpers ──────────────────────────────────────────────────────────────────

PR_DATA=""

fetch_pr() {
  PR_DATA=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" 2>/dev/null) || {
    echo "{\"status\":\"error\",\"message\":\"PR #${PR_NUMBER} not found in ${REPO}\"}" | tr -d '\000'
    exit 1
  }
}

check_pr_state() {
  local state merged
  state=$(echo "$PR_DATA" | jq -r '.state')
  merged=$(echo "$PR_DATA" | jq -r '.merged_at // empty')
  if [[ "$state" == "closed" ]]; then
    if [[ -n "$merged" ]]; then
      echo "{\"status\":\"merged\",\"message\":\"PR #${PR_NUMBER} was merged at ${merged}\"}" | tr -d '\000'
    else
      echo "{\"status\":\"closed\",\"message\":\"PR #${PR_NUMBER} is closed (not merged)\"}" | tr -d '\000'
    fi
    exit 0
  fi
}

is_copilot_pending() {
  echo "$PR_DATA" | jq '[.requested_reviewers[] | select(.login == "copilot-pull-request-reviewer[bot]" or .login == "Copilot")] | length'
}

get_latest_copilot_review() {
  local reviews
  reviews=$(gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews?per_page=100" \
    --jq '[.[] | select(.user.login | contains("copilot")) | {id: .id, state: .state, submitted_at: .submitted_at}]' 2>/dev/null) || {
    echo '{"status":"error","message":"Failed to fetch reviews"}' | tr -d '\000'
    return 1
  }
  echo "$reviews" | jq -s 'add | sort_by(.submitted_at) | last // empty'
}

get_review_comments() {
  local rid="${1:?review_id required}"
  gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews/${rid}/comments" \
    --jq '[.[] | {id: .id, path: .path, line: .original_line, body: .body, in_reply_to_id: .in_reply_to_id}]' 2>/dev/null || echo "[]"
}

# ─── Subcommand: status ──────────────────────────────────────────────────────

cmd_status() {
  load_last_known
  fetch_pr
  check_pr_state

  # --wait mode: poll until Copilot finishes
  if [[ "$WAIT" -eq 1 ]]; then
    local elapsed=0 interval=15
    while [[ $elapsed -lt $WAIT_MAX ]]; do
      local pending
      pending=$(is_copilot_pending)
      if [[ "$pending" -eq 0 ]]; then
        _emit_review_status
        return
      fi
      >&2 echo "[$(date +%H:%M:%S)] Copilot still reviewing... (${elapsed}s / ${WAIT_MAX}s)"
      local sleep_time=$((interval < (WAIT_MAX - elapsed) ? interval : (WAIT_MAX - elapsed)))
      sleep "$sleep_time"
      elapsed=$((elapsed + sleep_time))
      fetch_pr
    done
    # Copilot may have stalled — dismiss and re-request once, then wait again
    >&2 echo "Copilot appears stalled after ${WAIT_MAX}s — dismissing and re-requesting..."
    if ! gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
      -X DELETE --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null; then
      >&2 echo "Failed to dismiss existing Copilot review request."
      echo '{"status":"error","message":"Failed to dismiss existing Copilot review request"}' | tr -d '\000'
      return 1
    fi
    sleep 2
    if ! gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
      -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null; then
      >&2 echo "Failed to re-request Copilot review — aborting cycle."
      echo '{"status":"error","message":"Failed to re-request Copilot review"}' | tr -d '\000'
      return 1
    fi
    >&2 echo "Re-requested. Waiting another ${WAIT_MAX}s..."
    sleep 5
    fetch_pr

    local elapsed2=0
    while [[ $elapsed2 -lt $WAIT_MAX ]]; do
      local pending2
      pending2=$(is_copilot_pending)
      if [[ "$pending2" -eq 0 ]]; then
        _emit_review_status
        return
      fi
      >&2 echo "[$(date +%H:%M:%S)] Copilot still reviewing (retry)... (${elapsed2}s / ${WAIT_MAX}s)"
      local sleep_time2=$((interval < (WAIT_MAX - elapsed2) ? interval : (WAIT_MAX - elapsed2)))
      sleep "$sleep_time2"
      elapsed2=$((elapsed2 + sleep_time2))
      fetch_pr
    done

    echo '{"status":"pending","message":"Copilot still stalled after dismiss+re-request (total '"$((WAIT_MAX * 2))"'s)"}' | tr -d '\000'
    return
  fi

  # Single check
  local pending
  pending=$(is_copilot_pending)
  if [[ "$pending" -gt 0 ]]; then
    echo '{"status":"pending","message":"Copilot review in progress"}' | tr -d '\000'
    return
  fi

  _emit_review_status
}

_emit_review_status() {
  local latest
  latest=$(get_latest_copilot_review) || return

  if [[ -z "$latest" || "$latest" == "null" ]]; then
    echo '{"status":"no_reviews"}' | tr -d '\000'
    return
  fi

  local rid rstate rat
  rid=$(echo "$latest" | jq -r '.id')
  rstate=$(echo "$latest" | jq -r '.state')
  rat=$(echo "$latest" | jq -r '.submitted_at')

  # Count total reviews
  local total
  total=$(gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews?per_page=100" \
    --jq '[.[] | select(.user.login | contains("copilot"))] | length' 2>/dev/null | jq -s 'add')

  if [[ "$rstate" == "APPROVED" ]]; then
    jq -n --arg rid "$rid" --arg rat "$rat" --argjson total "$total" \
      '{"status":"approved","review_id":$rid,"submitted_at":$rat,"total_reviews":$total}' | tr -d '\000'
    return
  fi

  if [[ -n "$LAST_KNOWN" && "$rid" == "$LAST_KNOWN" ]]; then
    jq -n --arg rid "$rid" --arg rat "$rat" --argjson total "$total" \
      '{"status":"no_change","review_id":$rid,"submitted_at":$rat,"total_reviews":$total}' | tr -d '\000'
    return
  fi

  local comments comment_count
  comments=$(get_review_comments "$rid")
  comment_count=$(echo "$comments" | jq 'length')

  # React 👀 to the review summary
  react_eyes_to_review "$rid"

  # Check review body for Copilot error messages
  local review_body
  review_body=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}/reviews/$rid" --jq '.body // ""' 2>/dev/null || echo "")
  if echo "$review_body" | grep -qiE 'encountered an error|unable to review|try again'; then
    jq -n \
      --arg rid "$rid" --arg rat "$rat" --argjson total "$total" --arg body "$review_body" \
      '{"status":"error","review_id":$rid,"submitted_at":$rat,"total_reviews":$total,"message":"Copilot review failed — re-request needed","body":$body}' | tr -d '\000'
    return
  fi

  if [[ "$comment_count" -eq 0 ]]; then
    jq -n \
      --arg rid "$rid" --arg rat "$rat" --argjson total "$total" \
      '{"status":"clean","review_id":$rid,"submitted_at":$rat,"total_reviews":$total,"message":"Copilot reviewed with no new comments — ready to merge"}' | tr -d '\000'
    return
  fi

  jq -n \
    --arg rid "$rid" --arg rat "$rat" --arg rstate "$rstate" \
    --argjson cc "$comment_count" --argjson comments "$comments" --argjson total "$total" \
    '{"status":"new_review","review_id":$rid,"submitted_at":$rat,"review_state":$rstate,"comment_count":$cc,"comments":$comments,"total_reviews":$total}' | tr -d '\000'
}

# ─── Subcommand: comments ────────────────────────────────────────────────────

cmd_comments() {
  fetch_pr
  check_pr_state

  local rid="$REVIEW_ID"
  if [[ -z "$rid" ]]; then
    local latest
    latest=$(get_latest_copilot_review) || return
    if [[ -z "$latest" || "$latest" == "null" ]]; then
      echo '{"comments":[],"count":0}' | tr -d '\000'
      return
    fi
    rid=$(echo "$latest" | jq -r '.id')
  fi

  local comments count
  comments=$(get_review_comments "$rid")
  # Filter to only top-level comments (not replies)
  comments=$(echo "$comments" | jq '[.[] | select(.in_reply_to_id == null)]')
  count=$(echo "$comments" | jq 'length')

  # React 👀 to the review summary
  if [[ "$count" -gt 0 ]]; then
    react_eyes_to_review "$rid"
  fi

  jq -n --arg rid "$rid" --argjson count "$count" --argjson comments "$comments" \
    '{"review_id":$rid,"count":$count,"comments":$comments}' | tr -d '\000'
}

# ─── Subcommand: reply ────────────────────────────────────────────────────────

cmd_reply() {
  if [[ -z "$COMMENT_ID" || -z "$REPLY_BODY" ]]; then
    >&2 echo "Usage: pr-review.sh <pr> reply <comment_id> <body>"
    exit 1
  fi

  local result
  result=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}/comments/${COMMENT_ID}/replies" \
    -X POST -f body="$REPLY_BODY" --jq '{id: .id, body: .body, created_at: .created_at}' 2>&1) || {
    echo "{\"status\":\"error\",\"message\":\"Failed to reply to comment ${COMMENT_ID}\",\"detail\":$(echo "$result" | jq -Rs .)}" | tr -d '\000'
    exit 1
  }

  echo "$result" | jq --arg cid "$COMMENT_ID" '. + {"status":"replied","comment_id":$cid}' | tr -d '\000'
}

# ─── Subcommand: reply-all ────────────────────────────────────────────────────

cmd_reply_all() {
  local input
  input=$(cat)

  local count
  count=$(echo "$input" | jq 'length')

  if [[ "$count" -eq 0 ]]; then
      echo '{"status":"ok","replied":0,"failed":0}' | tr -d '\000'
    return
  fi

  local replied=0 failed=0 errors="[]"

  for i in $(seq 0 $((count - 1))); do
    local cid body
    cid=$(echo "$input" | jq -r ".[$i].comment_id")
    body=$(echo "$input" | jq -r ".[$i].body")

    if gh api "repos/${REPO}/pulls/${PR_NUMBER}/comments/${cid}/replies" \
      -X POST -f body="$body" >/dev/null 2>&1; then
      replied=$((replied + 1))
      if [[ "$NO_COLOR" -eq 0 ]]; then
        >&2 echo "  ✓ Replied to comment ${cid}"
      else
        >&2 echo "  [ok] Replied to comment ${cid}"
      fi
    else
      failed=$((failed + 1))
      errors=$(echo "$errors" | jq --arg cid "$cid" '. + [$cid]')
      if [[ "$NO_COLOR" -eq 0 ]]; then
        >&2 echo "  ✗ Failed to reply to comment ${cid}"
      else
        >&2 echo "  [fail] Failed to reply to comment ${cid}"
      fi
    fi
  done

  jq -n --argjson replied "$replied" --argjson failed "$failed" --argjson errors "$errors" \
    '{"status":"ok","replied":$replied,"failed":$failed,"failed_ids":$errors}' | tr -d '\000'
}

# ─── Subcommand: request ─────────────────────────────────────────────────────

cmd_request() {
  fetch_pr
  check_pr_state

  local pending
  pending=$(is_copilot_pending)
  if [[ "$pending" -gt 0 ]]; then
    echo '{"status":"already_pending","message":"Copilot review already in progress — not re-requesting"}' | tr -d '\000'
    return
  fi

  # Try the newer `gh pr edit --add-reviewer @copilot` syntax first (gh ≥ 2.67, March 2026)
  if gh pr edit "$PR_NUMBER" --repo "$REPO" --add-reviewer "@copilot" >/dev/null 2>&1; then
    echo '{"status":"requested","message":"Copilot review requested (gh pr edit)"}' | tr -d '\000'
  else
    # Fallback to raw API: DELETE first for idempotent re-request semantics, then POST
    gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
      -X DELETE --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null 2>&1 || true
    sleep 1
    gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
      -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null 2>&1 || {
      echo '{"status":"error","message":"Failed to request review"}' | tr -d '\000'
      exit 1
    }
    echo '{"status":"requested","message":"Copilot review requested (API fallback)"}' | tr -d '\000'
  fi
}

# ─── Subcommand: push ────────────────────────────────────────────────────────

cmd_push() {
  fetch_pr
  check_pr_state

  local branch
  branch=$(echo "$PR_DATA" | jq -r '.head.ref')

  # Check for uncommitted changes
  if [[ -n $(git status --porcelain 2>/dev/null) ]]; then
    local msg="${COMMIT_MSG:-fix: address Copilot review comments}"
    git add -A
    git -c user.email="ors@gscapital.co.il" -c user.name="Or Sharon" commit -m "$msg"
    >&2 echo "Committed: ${msg}"
  else
    >&2 echo "No uncommitted changes"
  fi

  # Check for unpushed commits
  local ahead
  ahead=$(git rev-list --count "origin/${branch}..${branch}" 2>/dev/null || echo "0")

  if [[ "$ahead" -gt 0 ]]; then
    git push origin "$branch" 2>&1 || {
      echo '{"status":"error","message":"Failed to push"}' | tr -d '\000'
      exit 1
    }
    >&2 echo "Pushed ${ahead} commit(s) to ${branch}"
  else
    >&2 echo "Nothing to push"
  fi

  jq -n --arg branch "$branch" --argjson ahead "$ahead" \
    '{"status":"pushed","branch":$branch,"commits_pushed":$ahead}' | tr -d '\000'
}

# ─── Subcommand: cycle ────────────────────────────────────────────────────────
# Full cycle: wait for review to land → output comments
# Designed for agent loops:
#   1. Agent fixes code + runs `push`
#   2. Agent runs `cycle --wait 300`
#   3. Script blocks until Copilot finishes, then returns comments
#   4. Agent reads comments, fixes, loops back to 1

cmd_cycle() {
  WAIT=1  # force wait mode
  fetch_pr
  check_pr_state

  # Snapshot the latest review ID at cycle start — used for new_review detection.
  # Do NOT use load_last_known here; stale state files cause missed reviews.
  local snapshot_id=""
  local _snap
  _snap=$(get_latest_copilot_review) || true
  if [[ -n "$_snap" && "$_snap" != "null" ]]; then
    snapshot_id=$(echo "$_snap" | jq -r '.id')
  fi

  # If Copilot isn't pending, always request a fresh review so the wait loop
  # has something to detect.  snapshot_id is still used below as the baseline
  # to recognise when the *new* review has landed.
  local pending
  pending=$(is_copilot_pending)
  if [[ "$pending" -eq 0 ]]; then
    >&2 echo "Requesting Copilot review..."
    if ! gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
      -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null; then
      >&2 echo "Warning: Failed to request Copilot review."
    fi
    sleep 3
    fetch_pr
  fi

  # Now wait — compare against snapshot_id, not LAST_KNOWN from state file
  local elapsed=0 interval=15
  while [[ $elapsed -lt $WAIT_MAX ]]; do
    pending=$(is_copilot_pending)
    if [[ "$pending" -eq 0 ]]; then
      local cur_review cur_id
      cur_review=$(get_latest_copilot_review) || true
      cur_id=""
      if [[ -n "$cur_review" && "$cur_review" != "null" ]]; then
        cur_id=$(echo "$cur_review" | jq -r '.id')
      fi
      if [[ "$cur_id" != "$snapshot_id" ]]; then
        # New review landed — emit it (use LAST_KNOWN="" so no_change is never triggered)
        LAST_KNOWN=""
        _emit_review_status
        return
      fi
      # Still the same review — Copilot may not have posted yet; keep waiting
    fi
    >&2 echo "[$(date +%H:%M:%S)] Copilot reviewing... (${elapsed}s / ${WAIT_MAX}s)"
    local sleep_time=$((interval < (WAIT_MAX - elapsed) ? interval : (WAIT_MAX - elapsed)))
    sleep "$sleep_time"
    elapsed=$((elapsed + sleep_time))
    fetch_pr
  done

  # Copilot may have stalled — dismiss and re-request once, then wait again
  >&2 echo "Copilot appears stalled after ${WAIT_MAX}s — dismissing and re-requesting..."
  if ! gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
    -X DELETE --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null; then
    >&2 echo "Failed to dismiss existing Copilot review request."
    echo '{"status":"error","message":"Failed to dismiss existing Copilot review request"}' | tr -d '\000'
    return 1
  fi
  sleep 2
  if ! gh api "repos/${REPO}/pulls/${PR_NUMBER}/requested_reviewers" \
    -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null; then
    >&2 echo "Failed to re-request Copilot review — aborting cycle."
    echo '{"status":"error","message":"Failed to re-request Copilot review"}' | tr -d '\000'
    return 1
  fi
  >&2 echo "Re-requested. Waiting another ${WAIT_MAX}s..."
  sleep 5
  fetch_pr

  local elapsed2=0
  while [[ $elapsed2 -lt $WAIT_MAX ]]; do
    pending=$(is_copilot_pending)
    if [[ "$pending" -eq 0 ]]; then
      local cur_review2 cur_id2
      cur_review2=$(get_latest_copilot_review) || true
      cur_id2=""
      if [[ -n "$cur_review2" && "$cur_review2" != "null" ]]; then
        cur_id2=$(echo "$cur_review2" | jq -r '.id')
      fi
      if [[ "$cur_id2" != "$snapshot_id" ]]; then
        LAST_KNOWN=""
        _emit_review_status
        return
      fi
      # Same review as before stall recovery — keep waiting
    fi
    >&2 echo "[$(date +%H:%M:%S)] Copilot reviewing (retry)... (${elapsed2}s / ${WAIT_MAX}s)"
    local sleep_time2=$((interval < (WAIT_MAX - elapsed2) ? interval : (WAIT_MAX - elapsed2)))
    sleep "$sleep_time2"
    elapsed2=$((elapsed2 + sleep_time2))
    fetch_pr
  done

  echo '{"status":"pending","message":"Copilot still stalled after dismiss+re-request (total '"$((WAIT_MAX * 2))"'s)"}' | tr -d '\000'
}

# ─── Subcommand: watch ────────────────────────────────────────────────────────

cmd_watch() {
  if [[ -z "$REPO" ]]; then
    REPO=$(gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  fi
  if [[ -z "$REPO" ]]; then
    echo '{"status":"error","message":"--repo required for watch"}' | tr -d '\000'
    exit 1
  fi

  # Load or create watch file
  local watches="[]"
  if [[ -f "$WATCH_FILE" ]]; then
    watches=$(cat "$WATCH_FILE")
  fi

  # Check if already watched
  local exists
  exists=$(echo "$watches" | jq --arg repo "$REPO" --arg pr "$PR_NUMBER" \
    '[.[] | select(.repo == $repo and .pr == ($pr | tonumber))] | length')
  if [[ "$exists" -gt 0 ]]; then
    echo "{\"status\":\"already_watching\",\"repo\":\"$REPO\",\"pr\":$PR_NUMBER}" | tr -d '\000'
    return
  fi

  # Get current latest review ID so we know what's "new"
  local last_review_id="null"
  fetch_pr
  local latest
  latest=$(get_latest_copilot_review) || true
  if [[ -n "$latest" && "$latest" != "null" ]]; then
    last_review_id=$(echo "$latest" | jq '.id')
  fi

  # Add to watch list (under lock for safe concurrent access)
  watchfile_lock || {
    echo '{"status":"error","message":"Could not acquire watch file lock"}' | tr -d '\000'
    return 1
  }
  # Re-read inside lock to pick up any concurrent writes
  local watches_locked="[]"
  [[ -f "$WATCH_FILE" ]] && watches_locked=$(cat "$WATCH_FILE")
  local new_watches_locked
  if ! new_watches_locked=$(echo "$watches_locked" | jq --arg repo "$REPO" --arg pr "$PR_NUMBER" --argjson lrid "$last_review_id" \
    '. + [{"repo": $repo, "pr": ($pr | tonumber), "last_review_id": $lrid, "added_at": (now | todate), "retries": 0}]' 2>/dev/null); then
    watchfile_unlock
    echo '{"status":"error","message":"Failed to update watch list (invalid JSON?)"}' | tr -d '\000'
    return 1
  fi
  watchfile_write "$new_watches_locked" || { watchfile_unlock; echo '{"status":"error","message":"Failed to write watch file"}' | tr -d '\000'; return 1; }
  watchfile_unlock

  jq -n --arg repo "$REPO" --argjson pr "$PR_NUMBER" --argjson lrid "$last_review_id" \
    '{"status":"watching","repo":$repo,"pr":$pr,"last_review_id":$lrid}' | tr -d '\000'
}

# ─── Subcommand: unwatch ─────────────────────────────────────────────────────

cmd_unwatch() {
  if [[ -z "$REPO" ]]; then
    REPO=$(gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  fi

  if [[ ! -f "$WATCH_FILE" ]]; then
    echo '{"status":"not_found"}' | tr -d '\000'
    return
  fi

  local before=0 after="" after_count=0 current=""

  watchfile_lock || {
    echo '{"status":"error","message":"Could not acquire watch file lock"}' | tr -d '\000'
    return 1
  }
  # Read inside lock so before/after reflect the same snapshot
  [[ -f "$WATCH_FILE" ]] && current=$(cat "$WATCH_FILE") || current="[]"
  before=$(echo "$current" | jq 'length' 2>/dev/null) || before=0
  [[ "$before" =~ ^[0-9]+$ ]] || before=0

  local filtered
  if ! filtered=$(echo "$current" | jq --arg repo "$REPO" --arg pr "$PR_NUMBER" \
    '[.[] | select(.repo != $repo or .pr != ($pr | tonumber))]' 2>/dev/null); then
    watchfile_unlock
    echo '{"status":"error","message":"Failed to parse watch file"}' | tr -d '\000'
    return 1
  fi
  after="$filtered"
  watchfile_write "$after" || { watchfile_unlock; echo '{"status":"error","message":"Failed to write watch file"}' | tr -d '\000'; return 1; }
  watchfile_unlock

  after_count=$(echo "$after" | jq 'length' 2>/dev/null) || after_count=0
  [[ "$after_count" =~ ^[0-9]+$ ]] || after_count=0

  if [[ "$before" -eq "$after_count" ]]; then
    echo "{\"status\":\"not_found\",\"repo\":\"$REPO\",\"pr\":$PR_NUMBER}" | tr -d '\000'
  else
    echo "{\"status\":\"unwatched\",\"repo\":\"$REPO\",\"pr\":$PR_NUMBER}" | tr -d '\000'
  fi
}

# ─── Subcommand: poll-all ────────────────────────────────────────────────────
# --dry-run: print what would be fired to OpenClaw without mutating state

cmd_poll_all() {
  if [[ ! -f "$WATCH_FILE" ]]; then
    echo '{"watches":[],"results":[]}' | tr -d '\000'
    return
  fi

  local watches
  watches=$(cat "$WATCH_FILE")
  local count
  count=$(echo "$watches" | jq 'length')

  if [[ "$count" -eq 0 ]]; then
    echo '{"watches":[],"results":[]}' | tr -d '\000'
    return
  fi

  local results="[]"
  local updated_watches="$watches"

  for i in $(seq 0 $((count - 1))); do
    local repo pr last_rid
    repo=$(echo "$watches" | jq -r ".[$i].repo")
    pr=$(echo "$watches" | jq -r ".[$i].pr")
    last_rid=$(echo "$watches" | jq -r ".[$i].last_review_id")

    >&2 echo "Checking ${repo}#${pr}..."

    # Set globals for helper functions
    REPO="$repo"
    PR_NUMBER="$pr"
    LAST_KNOWN="$last_rid"

    # Fetch PR data
    PR_DATA=$(gh api "repos/${repo}/pulls/${pr}" 2>/dev/null) || {
      >&2 echo "  ✗ Failed to fetch PR"
      results=$(echo "$results" | jq --arg repo "$repo" --argjson pr "$pr" \
        '. + [{"repo":$repo,"pr":$pr,"status":"error","message":"Failed to fetch PR"}]')
      continue
    }

    # Check if PR is closed/merged → auto-unwatch
    local state merged
    state=$(echo "$PR_DATA" | jq -r '.state')
    merged=$(echo "$PR_DATA" | jq -r '.merged_at // empty')
    if [[ "$state" == "closed" ]]; then
      local close_status="closed"
      [[ -n "$merged" ]] && close_status="merged"
      results=$(echo "$results" | jq --arg repo "$repo" --argjson pr "$pr" --arg s "$close_status" \
        '. + [{"repo":$repo,"pr":$pr,"status":$s}]')
      updated_watches=$(echo "$updated_watches" | jq --arg repo "$repo" --argjson pr "$pr" \
        '[.[] | select(.repo != $repo or .pr != $pr)]')
      >&2 echo "  PR ${close_status} — unwatched"
      continue
    fi

    # Check if Copilot is still reviewing
    local pending
    pending=$(is_copilot_pending)
    if [[ "$pending" -gt 0 ]]; then
      results=$(echo "$results" | jq --arg repo "$repo" --argjson pr "$pr" \
        '. + [{"repo":$repo,"pr":$pr,"status":"pending"}]')
      >&2 echo "  Still pending"
      continue
    fi

    # Get latest review
    local latest
    latest=$(get_latest_copilot_review) || {
      results=$(echo "$results" | jq --arg repo "$repo" --argjson pr "$pr" \
        '. + [{"repo":$repo,"pr":$pr,"status":"error","message":"Failed to fetch reviews"}]')
      continue
    }

    if [[ -z "$latest" || "$latest" == "null" ]]; then
      results=$(echo "$results" | jq --arg repo "$repo" --argjson pr "$pr" \
        '. + [{"repo":$repo,"pr":$pr,"status":"no_reviews"}]')
      continue
    fi

    local rid rstate
    rid=$(echo "$latest" | jq -r '.id')
    rstate=$(echo "$latest" | jq -r '.state')

    # Check for Copilot error (review with empty body or error state)
    local review_body
    review_body=$(gh api "repos/${repo}/pulls/${pr}/reviews/${rid}" --jq '.body // ""' 2>/dev/null)
    if echo "$review_body" | grep -qi "encountered an error\|unable to review"; then
      if [[ "$NO_COLOR" -eq 0 ]]; then
        >&2 echo "  ❌ Copilot error — re-requesting review"
      else
        >&2 echo "  [error] Copilot error — re-requesting review"
      fi
      # Re-request review (skip in dry-run)
      if [[ "$DRY_RUN" -eq 0 ]]; then
        gh api "repos/${repo}/pulls/${pr}/requested_reviewers" \
          -X DELETE --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null 2>&1
        sleep 1
        gh api "repos/${repo}/pulls/${pr}/requested_reviewers" \
          -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}' >/dev/null 2>&1
      else
        >&2 echo "  [dry-run] Would re-request Copilot review for ${repo}#${pr}"
      fi

      local retry_msg="Copilot error — re-requested review"
      [[ "$DRY_RUN" -eq 1 ]] && retry_msg="Copilot error — would re-request review (dry-run)"

      # Increment retry counter (skip in dry-run)
      local retries
      retries=$(echo "$updated_watches" | jq --arg repo "$repo" --argjson pr "$pr" \
        '[.[] | select(.repo == $repo and .pr == $pr)][0].retries // 0')
      retries=$((retries + 1))
      if [[ "$DRY_RUN" -eq 0 ]]; then
        updated_watches=$(echo "$updated_watches" | jq --arg repo "$repo" --argjson pr "$pr" --argjson r "$retries" \
          '[.[] | if .repo == $repo and .pr == $pr then .retries = $r else . end]')
      fi

      results=$(echo "$results" | jq --arg repo "$repo" --argjson pr "$pr" --argjson r "$retries" --arg msg "$retry_msg" \
        '. + [{"repo":$repo,"pr":$pr,"status":"error_retried","retries":$r,"message":$msg}]')
      continue
    fi

    # Same review we already know about
    if [[ "$last_rid" != "null" && "$rid" == "$last_rid" ]]; then
      results=$(echo "$results" | jq --arg repo "$repo" --argjson pr "$pr" \
        '. + [{"repo":$repo,"pr":$pr,"status":"no_change"}]')
      >&2 echo "  No change"
      continue
    fi

    # New review!
    if [[ "$rstate" == "APPROVED" ]]; then
      # React 👀 to approved review (skip in dry-run)
      [[ "$DRY_RUN" -eq 0 ]] && react_eyes_to_review "$rid"
      results=$(echo "$results" | jq --arg repo "$repo" --argjson pr "$pr" --arg rid "$rid" \
        '. + [{"repo":$repo,"pr":$pr,"status":"approved","review_id":$rid}]')
      # Auto-unwatch on approval
      updated_watches=$(echo "$updated_watches" | jq --arg repo "$repo" --argjson pr "$pr" \
        '[.[] | select(.repo != $repo or .pr != $pr)]')
      if [[ "$NO_COLOR" -eq 0 ]]; then
        >&2 echo "  ✅ Approved — unwatched"
      else
        >&2 echo "  [ok] Approved — unwatched"
      fi
    else
      # New review with comments
      local comments comment_count
      comments=$(get_review_comments "$rid")
      comment_count=$(echo "$comments" | jq 'length')

      # React 👀 to review (skip in dry-run)
      if [[ "$comment_count" -gt 0 && "$DRY_RUN" -eq 0 ]]; then
        react_eyes_to_review "$rid"
      fi

      if [[ "$comment_count" -eq 0 ]]; then
        # Check for Copilot error in review body
        local review_body
        review_body=$(echo "$latest" | jq -r '.body // ""')
        if echo "$review_body" | grep -qiE 'encountered an error|unable to review|try again'; then
          # Copilot error — auto re-request review
          local copilot_msg="Copilot error — auto re-requested review"
          if [[ "$DRY_RUN" -eq 0 ]]; then
            if [[ "$NO_COLOR" -eq 0 ]]; then
              >&2 echo "  ⚠️ Copilot error — auto re-requesting review"
            else
              >&2 echo "  [warn] Copilot error — auto re-requesting review"
            fi
            local copilot_reviewer_body='{"reviewers":["copilot-pull-request-reviewer[bot]"]}'
            gh api "repos/$repo/pulls/$pr/requested_reviewers" -X DELETE --input - <<< "$copilot_reviewer_body" >/dev/null 2>&1 || true
            if gh api "repos/$repo/pulls/$pr/requested_reviewers" -X POST --input - <<< "$copilot_reviewer_body" >/dev/null 2>&1; then
              copilot_msg="Copilot error — auto re-requested review"
            else
              copilot_msg="Copilot error — failed to auto re-request review"
              if [[ "$NO_COLOR" -eq 0 ]]; then
                >&2 echo "  ⚠️ Failed to re-request Copilot review (GitHub API error)"
              else
                >&2 echo "  [warn] Failed to re-request Copilot review (GitHub API error)"
              fi
            fi
          else
            >&2 echo "  [dry-run] Would re-request Copilot review for ${repo}#${pr}"
            copilot_msg="Copilot error — would auto re-request review (dry-run)"
          fi
          results=$(echo "$results" | jq --arg repo "$repo" --argjson pr "$pr" --arg rid "$rid" --arg msg "$copilot_msg" \
            '. + [{"repo":$repo,"pr":$pr,"status":"copilot_error","review_id":$rid,"message":$msg}]')
          # Update last_review_id so we don't re-process this error
          updated_watches=$(echo "$updated_watches" | jq --arg repo "$repo" --argjson pr "$pr" --arg rid "$rid" \
            '[.[] | if .repo == $repo and .pr == ($pr | tonumber) then .last_review_id = ($rid | tonumber) else . end]')
        else
          # Clean review — no comments means all good
          [[ "$DRY_RUN" -eq 0 ]] && react_eyes_to_review "$rid"
          results=$(echo "$results" | jq --arg repo "$repo" --argjson pr "$pr" --arg rid "$rid" \
            '. + [{"repo":$repo,"pr":$pr,"status":"clean","review_id":$rid,"message":"Copilot reviewed with no new comments — ready to merge"}]')
          # Auto-unwatch on clean review
          updated_watches=$(echo "$updated_watches" | jq --arg repo "$repo" --argjson pr "$pr" \
            '[.[] | select(.repo != $repo or .pr != $pr)]')
          if [[ "$NO_COLOR" -eq 0 ]]; then
            >&2 echo "  ✅ Clean review (0 comments) — unwatched"
          else
            >&2 echo "  [ok] Clean review (0 comments) — unwatched"
          fi
        fi
      else
        results=$(echo "$results" | jq --arg repo "$repo" --argjson pr "$pr" --arg rid "$rid" \
          --argjson cc "$comment_count" --argjson comments "$comments" \
          '. + [{"repo":$repo,"pr":$pr,"status":"new_review","review_id":$rid,"comment_count":$cc,"comments":$comments}]')
        if [[ "$NO_COLOR" -eq 0 ]]; then
          >&2 echo "  🆕 New review: ${comment_count} comments"
        else
          >&2 echo "  [new] New review: ${comment_count} comments"
        fi
      fi

      # Update last_review_id
      updated_watches=$(echo "$updated_watches" | jq --arg repo "$repo" --argjson pr "$pr" --arg rid "$rid" \
        '[.[] | if .repo == $repo and .pr == $pr then .last_review_id = ($rid | tonumber) | .retries = 0 else . end]')
    fi
  done

  # Save updated watches (skip in dry-run mode)
  if [[ "$DRY_RUN" -eq 0 ]]; then
    if watchfile_lock; then
      # Re-read the on-disk file inside the lock so concurrent resolved_comments
      # updates (from resolve-comment) are not clobbered by our stale snapshot.
      # Also preserve any entries only present on disk (new PRs added concurrently).
      local disk_watches="[]"
      [[ -f "$WATCH_FILE" ]] && disk_watches=$(cat "$WATCH_FILE")
      local merged_watches
      merged_watches=$(jq -n \
        --argjson mem "$updated_watches" \
        --argjson disk "$disk_watches" \
        '($mem | map(
            . as $entry |
            ($disk[] | select(.repo == $entry.repo and .pr == $entry.pr) | .resolved_comments) as $rc |
            if $rc then .resolved_comments = $rc else . end
          )) as $mem_merged |
         ($disk | map(
            . as $d |
            select([$mem[] | select(.repo == $d.repo and .pr == $d.pr)] | length == 0)
          )) as $disk_only |
         ($mem_merged + $disk_only)')
      local write_status=0
      watchfile_write "$merged_watches" || write_status=$?
      watchfile_unlock
      if [[ "$write_status" -ne 0 ]]; then
        >&2 echo "Warning: could not write watch file — changes not saved"
      fi
    else
      >&2 echo "Warning: could not acquire watch file lock — changes not saved"
    fi
  else
    >&2 echo "[dry-run] Would write updated watch file (not saved)"
  fi

  # Output results (always use in-memory updated_watches, not on-disk file)
  jq -n --argjson results "$results" --argjson watches "$updated_watches" \
    '{"results":$results,"watches":$watches}' | tr -d '\000'
}

# ─── Portable watch-file locking ─────────────────────────────────────────────
# Use mkdir-based locking for macOS compatibility (flock is not available by default).
# The lock directory is created atomically; the caller must call watchfile_unlock.

WATCH_LOCK_DIR="${WATCH_FILE}.lock.d"

watchfile_lock() {
  local retries=20
  while ! mkdir "$WATCH_LOCK_DIR" 2>/dev/null; do
    # Stale lock recovery:
    # 1. If pid file exists and PID is dead → remove lock
    # 2. If pid file is missing/empty and lock dir is older than 60s → remove lock (crash recovery)
    local lock_pid=""
    lock_pid=$(cat "${WATCH_LOCK_DIR}/pid" 2>/dev/null || true)
    if [[ -n "$lock_pid" ]] && ! kill -0 "$lock_pid" 2>/dev/null; then
      >&2 echo "Removing stale watch file lock (PID '${lock_pid}' not alive)"
      rm -rf "$WATCH_LOCK_DIR"
      continue
    fi
    if [[ -z "$lock_pid" ]]; then
      # Check lock dir age for crash recovery (no pid = process died before writing it)
      local lock_age=0
      lock_age=$(( $(date +%s) - $(stat -f %m "$WATCH_LOCK_DIR" 2>/dev/null || stat -c %Y "$WATCH_LOCK_DIR" 2>/dev/null || echo 0) ))
      if [[ "$lock_age" -gt 60 ]]; then
        >&2 echo "Removing stale watch file lock (no PID, lock dir ${lock_age}s old)"
        rm -rf "$WATCH_LOCK_DIR"
        continue
      fi
    fi
    retries=$((retries - 1))
    if [[ $retries -le 0 ]]; then
      >&2 echo "Timed out waiting for watch file lock"
      return 1
    fi
    sleep 0.1
  done
  # Store PID so stale locks can be detected by future callers
  # If this write fails, release the lock immediately and return error
  if ! echo $$ > "${WATCH_LOCK_DIR}/pid" 2>/dev/null; then
    rm -rf "$WATCH_LOCK_DIR"
    >&2 echo "Failed to write PID to watch lock"
    return 1
  fi
}

watchfile_unlock() {
  rm -rf "$WATCH_LOCK_DIR"
}

watchfile_write() {
  # Atomic update: write to temp then mv
  local tmp_file="${WATCH_FILE}.tmp.$$"
  printf '%s\n' "$1" > "$tmp_file"
  mv "$tmp_file" "$WATCH_FILE"
}

# ─── Subcommand: resolve-comment ─────────────────────────────────────────────
# Record that a comment has been addressed at a given commit, so future runs
# skip re-fixing it.  Stored in ~/.pr-review-watches.json under
# resolved_comments: { "<comment_id>": "<commit_sha>" }

cmd_resolve_comment() {
  local cid="${COMMENT_ID:?Usage: pr-review.sh <pr> resolve-comment <comment_id> <commit_sha>}"
  local sha="${REPLY_BODY:?Usage: pr-review.sh <pr> resolve-comment <comment_id> <commit_sha>}"
  if [[ -z "$REPO" ]]; then
    REPO=$(gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  fi

  local resolution_status="resolved"

  watchfile_lock || {
    echo '{"status":"error","message":"Could not acquire watch file lock"}' | tr -d '\000'
    return 1
  }

  local watches="[]"
  [[ -f "$WATCH_FILE" ]] && watches=$(cat "$WATCH_FILE")

  # Check if this PR is in the watch list (with safe numeric fallback)
  local exists=0
  exists=$(echo "$watches" | jq --arg repo "$REPO" --argjson pr "$PR_NUMBER" \
    '[.[] | select(.repo == $repo and .pr == $pr)] | length' 2>/dev/null) || exists=0
  [[ "$exists" =~ ^[0-9]+$ ]] || exists=0

  if [[ "$exists" -eq 0 ]]; then
    # PR not in watch list — no-op on watch file
    resolution_status="not_watched"
  else
    # Merge the new resolved comment into the existing entry
    local new_watches
    if ! new_watches=$(echo "$watches" | jq \
      --arg repo "$REPO" --argjson pr "$PR_NUMBER" \
      --arg cid "$cid" --arg sha "$sha" \
      '[.[] | if .repo == $repo and .pr == $pr then
          .resolved_comments = ((.resolved_comments // {}) + {($cid): $sha})
        else . end]' 2>/dev/null); then
      watchfile_unlock
      echo '{"status":"error","message":"Failed to parse or update watch file"}' | tr -d '\000'
      return 1
    fi
    watches="$new_watches"
    if ! watchfile_write "$watches"; then
      watchfile_unlock
      echo '{"status":"error","message":"Failed to update watch file"}' | tr -d '\000'
      return 1
    fi
  fi

  watchfile_unlock

  if [[ "$resolution_status" == "not_watched" ]]; then
    echo "{\"status\":\"not_watched\",\"comment_id\":\"$cid\",\"commit\":\"$sha\"}" | tr -d '\000'
  else
    jq -n --arg cid "$cid" --arg sha "$sha" --arg repo "$REPO" --argjson pr "$PR_NUMBER" \
      '{"status":"resolved","repo":$repo,"pr":$pr,"comment_id":$cid,"commit":$sha}' | tr -d '\000'
  fi
}

# ─── Subcommand: clear-state ─────────────────────────────────────────────────

cmd_clear_state() {
  if [[ -z "$PR_NUMBER" ]]; then
    >&2 echo "Error: PR_NUMBER is required for clear-state"
    echo '{"status":"error","message":"PR_NUMBER is required"}' | tr -d '\000'
    exit 1
  fi
  local sf
  sf="$(state_file)"
  rm -f "$sf"
  echo '{"status":"cleared"}' | tr -d '\000'
}

# ─── Dispatch ─────────────────────────────────────────────────────────────────

case "$SUBCOMMAND" in
  status)         cmd_status ;;
  comments)       cmd_comments ;;
  reply)          cmd_reply ;;
  reply-all)      cmd_reply_all ;;
  request)        cmd_request ;;
  push)           cmd_push ;;
  cycle)          cmd_cycle ;;
  clear-state)    cmd_clear_state ;;
  resolve-comment) cmd_resolve_comment ;;
  watch)          cmd_watch ;;
  unwatch)        cmd_unwatch ;;
  poll-all)       cmd_poll_all ;;
  help|--help|-h)
    head -50 "$0" | grep '^#' | sed 's/^# \?//'
    ;;
  *)
    >&2 echo "Unknown subcommand: $SUBCOMMAND"
    >&2 echo "Available: status, comments, reply, reply-all, request, push, cycle, clear-state, watch, unwatch, poll-all"
    exit 1
    ;;
esac
