#!/usr/bin/env bash
# pr-review-session.sh — Crash recovery & cross-machine deduplication library
#
# Provides two independent features:
#
# 1. CRASH RECOVERY (session files)
#    Each runner writes a persistent session file under ~/.pr-review/sessions/.
#    On startup, if a session file for the same PR exists with a dead PID on
#    this host, the runner recovers and resumes from the last-known review state.
#
# 2. CROSS-MACHINE DEDUPLICATION (GitHub PR labels + lock comment)
#    When a runner starts it:
#      a. Checks for an existing lock comment (containing the hidden marker) on
#         the PR. The `rinse:running` label is added as a visible signal but is
#         NOT used as the primary lock check (_gh_lock_label_exists is available
#         but not wired into the acquisition flow).
#      b. If no active lock comment is found, adds the label and posts a hidden
#         lock comment with metadata (hostname, PID, timestamp, lock_id).
#      c. Sleeps briefly and re-reads the comment to verify it won the race.
#      d. If another runner already holds the lock (and it is not stale), this
#         runner exits cleanly with RC=2.
#    On exit (clean or crash) the lock is released: label removed, lock comment
#    deleted.  Stale locks (default: 4 h) are automatically stolen.
#
# Usage — source this file then call the functions:
#
#   source "$(dirname "$0")/pr-review-session.sh"
#
#   # On startup:
#   session_init     "$REPO" "$PR_NUMBER"   # set globals
#   session_recover                          # returns 0 if crash-recovery detected
#   gh_lock_acquire || exit 2               # returns 2 if another machine holds the lock
#
#   # During the loop (each iteration):
#   session_update "$iter" "$last_review_id"
#
#   # On exit (put in trap):
#   session_clear
#   gh_lock_release
#
# Requires: jq, gh (GitHub CLI), hostname, date
# This file must NOT call exit itself — it only defines functions.

# ─── Configuration ────────────────────────────────────────────────────────────

# Directory for persistent session files (crash recovery)
SESSION_BASE_DIR="${PR_REVIEW_SESSION_DIR:-${HOME}/.pr-review/sessions}"

# Lock timeout: treat a held lock as stale after this many seconds (default: 4 h)
RINSE_LOCK_TIMEOUT="${RINSE_LOCK_TIMEOUT:-14400}"

# Label used on the GitHub PR to signal a runner is active
RINSE_RUNNING_LABEL="${RINSE_RUNNING_LABEL:-rinse:running}"

# Magic marker in the lock comment body (must not appear in normal PR comments)
# The full comment body is: <!-- rinse-lock-metadata\n<json>\n-->
# so the metadata JSON is hidden from the PR conversation.
_RINSE_LOCK_MARKER="<!-- rinse-lock-metadata"

# ─── Internal globals (set by session_init) ───────────────────────────────────

_SESSION_REPO=""       # owner/repo
_SESSION_PR=""         # PR number (string)
_SESSION_FILE=""       # full path to the session JSON file
_SESSION_HOSTNAME=""   # $(hostname)
_SESSION_PID=$$        # this runner's PID
_LOCK_COMMENT_ID=""    # GitHub comment ID of the lock comment we created

# ─── Session: init ────────────────────────────────────────────────────────────

# session_init <repo> <pr_number>
# Must be called before any other function.
session_init() {
  _SESSION_REPO="${1:?session_init: repo required}"
  _SESSION_PR="${2:?session_init: pr_number required}"
  _SESSION_HOSTNAME="$(hostname -s 2>/dev/null || hostname)"
  _SESSION_PID=$$

  local slug="${_SESSION_REPO//\//_}"
  mkdir -p "$SESSION_BASE_DIR"
  _SESSION_FILE="${SESSION_BASE_DIR}/${slug}-pr-${_SESSION_PR}.json"
}

# ─── Session: write ───────────────────────────────────────────────────────────

# session_update <iter> <last_review_id>
# Call at the top of every loop iteration to keep state fresh.
session_update() {
  local iter="${1:-0}"
  local last_rid="${2:-}"
  [[ -z "$_SESSION_FILE" ]] && return 0

  jq -n \
    --arg hostname "$_SESSION_HOSTNAME" \
    --argjson pid "$_SESSION_PID" \
    --arg started_at "$(
      if [[ -f "$_SESSION_FILE" ]]; then
        jq -r '.started_at // ""' "$_SESSION_FILE" 2>/dev/null || echo ""
      else
        echo ""
      fi
    )" \
    --arg updated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --argjson iter "$iter" \
    --arg last_review_id "$last_rid" \
    --arg status "running" \
    '{
      hostname:       $hostname,
      pid:            $pid,
      started_at:     (if $started_at == "" then $updated_at else $started_at end),
      updated_at:     $updated_at,
      iter:           $iter,
      last_review_id: $last_review_id,
      status:         $status
    }' > "$_SESSION_FILE"
}

# ─── Session: recover ─────────────────────────────────────────────────────────

# session_recover
# Returns 0 if a previous crash was detected and RECOVER_REVIEW_ID is set.
# Returns 1 if no crash detected (normal startup).
#
# After calling this, inspect:
#   RECOVER_REVIEW_ID — last_review_id from the crashed session (may be empty)
#   RECOVER_ITER      — last iter from the crashed session
session_recover() {
  RECOVER_REVIEW_ID=""
  RECOVER_ITER=0

  [[ -f "$_SESSION_FILE" ]] || return 1

  local pid hostname status
  pid=$(jq -r '.pid // 0' "$_SESSION_FILE" 2>/dev/null || echo 0)
  hostname=$(jq -r '.hostname // ""' "$_SESSION_FILE" 2>/dev/null || echo "")
  status=$(jq -r '.status // "unknown"' "$_SESSION_FILE" 2>/dev/null || echo "unknown")

  # Only recover sessions from this host (cross-host crash → gh_lock will handle dedup)
  if [[ "$hostname" != "$_SESSION_HOSTNAME" ]]; then
    return 1
  fi

  # If the recorded PID is still alive, there is a live duplicate on this machine.
  if [[ "$pid" -gt 0 ]] && kill -0 "$pid" 2>/dev/null; then
    return 1  # another live process — not a crash
  fi

  if [[ "$status" == "done" ]]; then
    return 1  # clean exit — nothing to recover
  fi

  # PID is dead but status is "running" → this was a crash
  RECOVER_REVIEW_ID=$(jq -r '.last_review_id // ""' "$_SESSION_FILE" 2>/dev/null || echo "")
  RECOVER_ITER=$(jq -r '.iter // 0' "$_SESSION_FILE" 2>/dev/null || echo 0)

  return 0
}

# ─── Session: clear ───────────────────────────────────────────────────────────

# session_clear
# Mark session done and remove the file.
session_clear() {
  [[ -z "$_SESSION_FILE" ]] && return 0
  if [[ -f "$_SESSION_FILE" ]]; then
    local tmp
    if tmp=$(jq '.status = "done"' "$_SESSION_FILE" 2>/dev/null); then
      echo "$tmp" > "$_SESSION_FILE" || true
    fi
    rm -f "$_SESSION_FILE"
  fi
}

# ─── GH lock: helpers ─────────────────────────────────────────────────────────

_gh_lock_label_exists() {
  gh api "repos/${_SESSION_REPO}/issues/${_SESSION_PR}/labels" \
    --jq "[.[] | select(.name == \"${RINSE_RUNNING_LABEL}\")] | length > 0" \
    2>/dev/null || echo "false"
}

_gh_lock_ensure_label_created() {
  # Create the label in the repo if it doesn't exist yet (idempotent)
  gh api "repos/${_SESSION_REPO}/labels" \
    -X POST \
    -f name="${RINSE_RUNNING_LABEL}" \
    -f color="e4e669" \
    -f description="RINSE is actively reviewing this PR" \
    2>/dev/null || true
}

_gh_lock_add_label() {
  gh api "repos/${_SESSION_REPO}/issues/${_SESSION_PR}/labels" \
    -X POST \
    -f "labels[]=${RINSE_RUNNING_LABEL}" \
    >/dev/null 2>&1 || true
}

_gh_lock_remove_label() {
  # URL-encode the colon in the label name
  local encoded_label="${RINSE_RUNNING_LABEL//:/%3A}"
  gh api "repos/${_SESSION_REPO}/issues/${_SESSION_PR}/labels/${encoded_label}" \
    -X DELETE >/dev/null 2>&1 || true
}

# _gh_lock_find_comment
# Prints the comment object JSON if the lock comment exists, empty string otherwise.
_gh_lock_find_comment() {
  gh api --paginate "repos/${_SESSION_REPO}/issues/${_SESSION_PR}/comments" \
    --jq "[.[] | select(.body | contains(\"${_RINSE_LOCK_MARKER}\"))] | last // empty" \
    2>/dev/null || echo ""
}

# _gh_lock_parse_metadata <comment_body>
# Outputs the embedded JSON blob from the lock comment body.
_gh_lock_parse_metadata() {
  local body="$1"
  # Best-effort extraction: print the line immediately after the marker line
  # if present and not the closing "-->" line; otherwise print nothing.
  printf '%s\n' "$body" | awk -v marker="${_RINSE_LOCK_MARKER}" '
    found {
      if ($0 != "-->") print
      exit
    }
    index($0, marker) { found=1 }
  '
}

_gh_lock_is_stale() {
  local locked_at="$1"  # ISO8601 UTC
  [[ -z "$locked_at" ]] && return 0  # treat unparseable as stale

  local now_epoch locked_epoch age
  now_epoch=$(date -u +%s 2>/dev/null || echo 0)

  # Portable epoch parsing: try GNU date then BSD date
  if date --version >/dev/null 2>&1; then
    # GNU date
    locked_epoch=$(date -u -d "$locked_at" +%s 2>/dev/null || echo 0)
  else
    # BSD date (macOS)
    locked_epoch=$(date -u -jf "%Y-%m-%dT%H:%M:%SZ" "$locked_at" +%s 2>/dev/null || echo 0)
  fi

  if [[ "$locked_epoch" -eq 0 || "$now_epoch" -eq 0 ]]; then
    return 0  # parse failure → treat as stale
  fi

  age=$(( now_epoch - locked_epoch ))
  [[ "$age" -gt "$RINSE_LOCK_TIMEOUT" ]]
}

# ─── GH lock: acquire ────────────────────────────────────────────────────────

# gh_lock_acquire
# Returns 0 on success (we hold the lock).
# Returns 1 if another active runner holds the lock (caller should exit 2).
# Returns 0 with a warning if GH API is unavailable (degrade gracefully).
gh_lock_acquire() {
  [[ -z "$_SESSION_REPO" ]] && { >&2 echo "[rinse-lock] session_init not called"; return 0; }

  local lock_id
  lock_id="$(date -u +%Y%m%dT%H%M%S)-${_SESSION_PID}"

  # Ensure the label exists in the repo
  _gh_lock_ensure_label_created

  # ── Phase 1: Check existing lock ──────────────────────────────────────────
  local existing_comment existing_meta
  existing_comment=$(_gh_lock_find_comment)

  if [[ -n "$existing_comment" ]]; then
    local body
    body=$(echo "$existing_comment" | jq -r '.body // ""')
    existing_meta=$(_gh_lock_parse_metadata "$body")

    if [[ -n "$existing_meta" ]]; then
      local existing_host existing_pid existing_locked_at
      existing_host=$(echo "$existing_meta" | jq -r '.hostname // ""' 2>/dev/null || printf '%s' "")
      existing_pid=$(echo "$existing_meta" | jq -r '.pid // 0' 2>/dev/null || printf '%s' "0")
      existing_locked_at=$(echo "$existing_meta" | jq -r '.locked_at // ""' 2>/dev/null || printf '%s' "")

      # Is this our own lock (same host, same PID)? Re-use it.
      if [[ "$existing_host" == "$_SESSION_HOSTNAME" && "$existing_pid" == "$_SESSION_PID" ]]; then
        _LOCK_COMMENT_ID=$(echo "$existing_comment" | jq -r '.id')
        return 0
      fi

      # Is the lock stale?
      if _gh_lock_is_stale "$existing_locked_at"; then
        >&2 echo "[rinse-lock] Stale lock from ${existing_host} (PID ${existing_pid}, locked at ${existing_locked_at}) — stealing"
        # Delete the stale comment so we can replace it
        local stale_id
        stale_id=$(echo "$existing_comment" | jq -r '.id')
        gh api "repos/${_SESSION_REPO}/issues/comments/${stale_id}" -X DELETE >/dev/null 2>&1 || true
      else
        >&2 echo "[rinse-lock] PR #${_SESSION_PR} is already locked by ${existing_host} (PID ${existing_pid})"
        >&2 echo "[rinse-lock] Lock held since ${existing_locked_at} — skipping to avoid duplicate run"
        return 1
      fi
    fi
  fi

  # ── Phase 2: Add label + post lock comment ────────────────────────────────
  local locked_at
  locked_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  local meta_json
  meta_json=$(jq -n \
    --arg hostname "$_SESSION_HOSTNAME" \
    --argjson pid "$_SESSION_PID" \
    --arg locked_at "$locked_at" \
    --arg lock_id "$lock_id" \
    '{hostname: $hostname, pid: $pid, locked_at: $locked_at, lock_id: $lock_id}')

  local comment_body
  comment_body="$(printf '%s\n%s\n%s' "$_RINSE_LOCK_MARKER" "$meta_json" "-->")"

  _gh_lock_add_label

  local created_comment
  created_comment=$(gh api "repos/${_SESSION_REPO}/issues/${_SESSION_PR}/comments" \
    -X POST \
    -f body="$comment_body" \
    2>/dev/null) || {
    >&2 echo "[rinse-lock] Failed to post lock comment — degrading to local-only dedup"
    return 0
  }

  _LOCK_COMMENT_ID=$(echo "$created_comment" | jq -r '.id')

  # ── Phase 3: Race-check (wait 3 s, re-read, verify we still hold the lock) ─
  sleep 3

  local verify_comment verify_meta verify_lid
  verify_comment=$(_gh_lock_find_comment)
  if [[ -n "$verify_comment" ]]; then
    verify_meta=$(_gh_lock_parse_metadata "$(echo "$verify_comment" | jq -r '.body // ""')")
    verify_lid=$(echo "$verify_meta" | jq -r '.lock_id // ""' 2>/dev/null || printf '%s' "")
    if [[ "$verify_lid" != "$lock_id" ]]; then
      # Another runner's comment is now the canonical one — we lost the race
      >&2 echo "[rinse-lock] Lost the acquisition race (another runner's comment took precedence)"
      # Clean up our own comment
      gh api "repos/${_SESSION_REPO}/issues/comments/${_LOCK_COMMENT_ID}" -X DELETE >/dev/null 2>&1 || true
      _LOCK_COMMENT_ID=""
      _gh_lock_remove_label
      return 1
    fi
  fi

  return 0
}

# ─── GH lock: release ────────────────────────────────────────────────────────

# gh_lock_release
# Remove the running label and delete the lock comment.
# Safe to call multiple times (idempotent).
gh_lock_release() {
  [[ -z "$_SESSION_REPO" ]] && return 0

  _gh_lock_remove_label

  if [[ -n "$_LOCK_COMMENT_ID" ]]; then
    gh api "repos/${_SESSION_REPO}/issues/comments/${_LOCK_COMMENT_ID}" \
      -X DELETE >/dev/null 2>&1 || true
    _LOCK_COMMENT_ID=""
  else
    # Fallback: find and delete any lock comment left by this host/PID
    local comment
    comment=$(_gh_lock_find_comment)
    if [[ -n "$comment" ]]; then
      local body meta host pid
      body=$(echo "$comment" | jq -r '.body // ""')
      meta=$(_gh_lock_parse_metadata "$body")
      host=$(echo "$meta" | jq -r '.hostname // ""')
      pid=$(echo "$meta" | jq -r '.pid // 0')
      if [[ "$host" == "$_SESSION_HOSTNAME" && "$pid" == "$_SESSION_PID" ]]; then
        local cid
        cid=$(echo "$comment" | jq -r '.id')
        gh api "repos/${_SESSION_REPO}/issues/comments/${cid}" -X DELETE >/dev/null 2>&1 || true
      fi
    fi
  fi
}
