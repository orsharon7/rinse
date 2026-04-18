#!/usr/bin/env bash
# rinse-backfill-sessions.sh — Synthesise historical session records from RINSE
# PR-review logs so that `rinse stats` can include past cycles.
#
# Usage:
#   ./rinse-backfill-sessions.sh [--repo <owner/repo>] [--logs-dir <path>]
#
# Options:
#   --repo      <owner/repo>   Repo to attribute sessions to (default: auto-detect)
#   --logs-dir  <path>         Override log directory (default: ~/.pr-review/logs)
#   --dry-run                  Print what would be written without writing
#
# How it works:
#   1. Scans .log files in ~/.pr-review/logs/
#   2. Extracts PR number, start/end timestamps, iteration count, and comment
#      counts per iteration from PR-review log lines emitted by
#      pr-review-opencode.sh
#   3. Writes one JSON session file per matching log into ~/.rinse/sessions/
#      (skips if a session file for that repo+PR+start already exists)
#
set -euo pipefail

LOGS_DIR="${HOME}/.pr-review/logs"
REPO=""
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)     REPO="$2";     shift 2 ;;
    --logs-dir) LOGS_DIR="$2"; shift 2 ;;
    --dry-run)  DRY_RUN=true;  shift ;;
    --help|-h)
      head -25 "$0" | grep '^#' | sed 's/^# \?//'; exit 0 ;;
    *) >&2 echo "Unknown arg: $1"; exit 1 ;;
  esac
done

if [[ ! -d "$LOGS_DIR" ]]; then
  echo "No log directory found at ${LOGS_DIR} — nothing to backfill."
  exit 0
fi

# Auto-detect repo from current directory if not supplied.
if [[ -z "$REPO" ]]; then
  REPO=$(gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  if [[ -z "$REPO" ]]; then
    >&2 echo "Could not detect repo. Use --repo owner/repo."
    exit 1
  fi
fi

SESSIONS_DIR="${HOME}/.rinse/sessions"
if [[ "$DRY_RUN" == false ]]; then
  mkdir -p "$SESSIONS_DIR"
  chmod 700 "$SESSIONS_DIR"
fi

# UUID generator (same approach as pr-review-opencode.sh)
_gen_uuid() {
  local raw
  raw=$(od -An -tx1 -N16 /dev/urandom 2>/dev/null | tr -d ' \n') || raw=""
  if [[ ${#raw} -eq 32 ]]; then
    raw="${raw:0:12}4${raw:13:3}$(printf '%x' "$(( (16#${raw:16:1} & 0x3) | 0x8 ))")${raw:17:3}${raw:20:12}"
    printf '%s-%s-%s-%s-%s\n' "${raw:0:8}" "${raw:8:4}" "${raw:12:4}" "${raw:16:4}" "${raw:20:12}"
  else
    printf '%08x-%04x-4%03x-%04x-%012x\n' \
      "$(date +%s)" "$RANDOM" "$(( RANDOM & 0xfff ))" \
      "$(( (RANDOM & 0x3fff) | 0x8000 ))" "$(( RANDOM * RANDOM * RANDOM ))"
  fi
}

processed=0
skipped=0

shopt -s nullglob
for logfile in "${LOGS_DIR}"/*.log; do
  [[ -f "$logfile" ]] || continue

  log_basename=$(basename "$logfile")

  # Only backfill main cycle logs. Skip known auxiliary logs (for example
  # *-reflect.log) and require the PR review loop start marker to be present.
  [[ "$log_basename" == *-reflect.log ]] && continue
  grep -qE 'Starting .*PR review loop' "$logfile" 2>/dev/null || continue

  # Extract PR number from filename: <repo_slug>-pr-<N>.log
  pr_num=$(basename "$logfile" .log | grep -oE 'pr-[0-9]+' | grep -oE '[0-9]+' | head -1 || echo "")
  [[ -z "$pr_num" ]] && continue

  # ── Handle multi-run log files ────────────────────────────────────────────
  # pr-review-opencode.sh appends to the same log file across runs (via tee -a),
  # so a single .log file can contain multiple sessions separated by the start
  # marker.  Backfilling the whole file as one session would produce incorrect
  # duration/iteration/comment counts.  Instead, detect multiple markers, emit
  # a warning, and process only the most-recent segment.
  marker_count=$(grep -cE 'Starting .*PR review loop' "$logfile" 2>/dev/null || echo "0")
  if [[ "$marker_count" -gt 1 ]]; then
    >&2 echo "⚠️  ${log_basename}: ${marker_count} session starts found — backfilling only the most recent segment."
    # Find the line number of the last occurrence of the start marker.
    last_marker_line=$(grep -nE 'Starting .*PR review loop' "$logfile" 2>/dev/null | tail -1 | cut -d: -f1)
    # Build a temp file containing only that last segment.
    segment_file=$(mktemp /tmp/rinse_segment_XXXXXX.log)
    tail -n +"$last_marker_line" "$logfile" > "$segment_file"
    logfile="$segment_file"
    _cleanup_segment() { rm -f "$segment_file"; }
    trap '_cleanup_segment' EXIT
  fi

  # ── Parse log timestamps ──────────────────────────────────────────────────
  # Log lines typically start with a timestamp pattern like:
  #   [2026-04-17 14:00:01] 🚀 Starting opencode PR review loop
  # Only extract leading bracketed timestamps written by the logger so we
  # don't accidentally pick up ISO-like timestamps from arbitrary command output.
  first_ts=$(grep -oE '^\[[0-9]{4}-[0-9]{2}-[0-9]{2}[ T][0-9]{2}:[0-9]{2}:[0-9]{2}\]' "$logfile" 2>/dev/null | sed 's/^\[//; s/\]$//' | head -1 || echo "")
  last_ts=$(grep  -oE '^\[[0-9]{4}-[0-9]{2}-[0-9]{2}[ T][0-9]{2}:[0-9]{2}:[0-9]{2}\]' "$logfile" 2>/dev/null | sed 's/^\[//; s/\]$//' | tail -1 || echo "")

  # Fallback to file mtime when timestamps not in log.
  if [[ -z "$first_ts" ]]; then
    first_ts=$(stat -f "%Sm" -t "%Y-%m-%d %H:%M:%S" "$logfile" 2>/dev/null \
      || stat -c "%y" "$logfile" 2>/dev/null | cut -c1-19 \
      || date "+%Y-%m-%d %H:%M:%S")
    last_ts="$first_ts"
  fi

  # Convert local timestamps to UTC before formatting as RFC-3339Z.
  # Log lines are written with local time (date '+%Y-%m-%d %H:%M:%S'), so we
  # must convert through an epoch to get an accurate UTC representation.
  _ts_to_utc() {
    local ts="$1"
    local epoch
    epoch=$(date -j -f "%Y-%m-%d %H:%M:%S" "$ts" "+%s" 2>/dev/null) \
      || epoch=$(date --date="$ts" "+%s" 2>/dev/null) \
      || { echo "${ts/ /T}Z"; return; }
    date -u -r "$epoch" "+%Y-%m-%dT%H:%M:%SZ" 2>/dev/null \
      || date -u --date="@${epoch}" "+%Y-%m-%dT%H:%M:%SZ" 2>/dev/null \
      || echo "${ts/ /T}Z"
  }
  started_at=$(_ts_to_utc "$first_ts")
  ended_at=$(_ts_to_utc "$last_ts")

  # Compute session filename using the Go stats convention:
  # YYYYMMDD-HHMMSS-<repo>-PR<N>-<session_id>.json
  session_id="$(_gen_uuid)"
  repo_slug="${REPO//\//-}"
  date_part="${started_at:0:10}"
  date_part="${date_part//-/}"
  time_part="${started_at:11:8}"
  time_part="${time_part//:/}"
  started_slug="${date_part}-${time_part}"
  session_prefix="${SESSIONS_DIR}/${started_slug}-${repo_slug}-PR${pr_num}"
  legacy_session_fname="${session_prefix}.json"
  session_fname="${session_prefix}-${session_id}.json"

  if [[ -f "$legacy_session_fname" ]] || compgen -G "${session_prefix}-*.json" > /dev/null; then
    (( skipped++ )) || true
    continue
  fi

  # ── Parse iteration/comment counts from log ───────────────────────────────
  # Look for lines like: "💬 N comment(s) in review", and also terminal
  # success lines that imply a completed review with zero comments.
  declare -a comments_arr=()
  while IFS= read -r line; do
    if [[ "$line" =~ ([0-9]+)[[:space:]]+comment\(s\)[[:space:]]+in[[:space:]]+review ]]; then
      comments_arr+=("${BASH_REMATCH[1]}")
    elif [[ "$line" =~ Clean[[:space:]]+review.*0[[:space:]]+comments ]] || [[ "$line" =~ Copilot[[:space:]]+APPROVED ]]; then
      comments_arr+=("0")
    fi
  done < "$logfile"

  total_comments=0
  for c in "${comments_arr[@]+"${comments_arr[@]}"}"; do
    total_comments=$(( total_comments + c ))
  done

  iterations="${#comments_arr[@]}"

  # Build JSON array.
  comments_json="["
  first_elem=true
  for c in "${comments_arr[@]+"${comments_arr[@]}"}"; do
    [[ "$first_elem" == true ]] && first_elem=false || comments_json+=","
    comments_json+="$c"
  done
  comments_json+="]"

  # ── Determine outcome from log ────────────────────────────────────────────
  outcome="aborted"
  review_approved=false
  merge_confirmed=false

  if grep -Eq 'APPROVED|Clean review' "$logfile" 2>/dev/null; then
    review_approved=true
  fi

  # Only treat terminal success messages as proof that the PR was merged.
  # Pre-merge intent lines such as "Auto-merging..." or "squash" may be
  # emitted before `gh pr merge` runs and therefore cannot confirm success.
  if grep -Eq '✅ Merged, remote branch deleted|🎉 PR merged!|PR merged!' "$logfile" 2>/dev/null; then
    merge_confirmed=true
  fi

  if [[ "$merge_confirmed" == true ]]; then
    outcome="merged"
  elif [[ "$review_approved" == true ]]; then
    outcome="approved"
  elif grep -q "max iterations" "$logfile" 2>/dev/null; then
    outcome="max_iterations"
  elif grep -q "opencode exited with code" "$logfile" 2>/dev/null; then
    outcome="error"
  fi

  # ── Compute durations ─────────────────────────────────────────────────────
  start_epoch=$(date -j -f "%Y-%m-%dT%H:%M:%SZ" "$started_at" "+%s" 2>/dev/null \
    || date --date="$started_at" "+%s" 2>/dev/null \
    || echo "0")
  end_epoch=$(date -j -f "%Y-%m-%dT%H:%M:%SZ" "$ended_at" "+%s" 2>/dev/null \
    || date --date="$ended_at" "+%s" 2>/dev/null \
    || echo "0")
  duration_seconds=$(( end_epoch - start_epoch ))
  [[ $duration_seconds -lt 0 ]] && duration_seconds=0
  estimated_saved=$(( total_comments * 240 ))

  if [[ "$DRY_RUN" == true ]]; then
    echo "[DRY RUN] Would write: ${session_fname}"
    echo "          PR #${pr_num}  iterations=${iterations}  comments=${total_comments}  outcome=${outcome}"
    (( processed++ )) || true
    continue
  fi

  bk_approved="false"
  [[ "$outcome" == "approved" || "$outcome" == "merged" ]] && bk_approved="true"

  tmp_session_fname="$(mktemp "$(dirname "$session_fname")/.tmp_session_XXXXXX.json")"
  if jq -n \
    --arg session_id     "$session_id" \
    --arg repo           "$REPO" \
    --arg pr             "$pr_num" \
    --arg pr_title       "" \
    --arg started_at     "$started_at" \
    --arg ended_at       "$ended_at" \
    --arg runner         "opencode" \
    --arg model          "unknown" \
    --arg outcome        "$outcome" \
    --argjson approved   "$bk_approved" \
    --argjson iterations "$iterations" \
    --argjson comments   "$comments_json" \
    --argjson total      "$total_comments" \
    --argjson saved      "$estimated_saved" \
    --argjson duration   "$duration_seconds" \
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
    }' > "$tmp_session_fname"; then
    mv "$tmp_session_fname" "$session_fname"
    chmod 600 "$session_fname"
  else
    rm -f "$tmp_session_fname"
    >&2 echo "⚠️  jq failed — skipping ${session_fname}"
    continue
  fi

  echo "✅ Backfilled: ${session_fname}  (PR #${pr_num}, ${outcome}, ${total_comments} comments)"
  (( processed++ )) || true
done

echo ""
echo "Backfill complete — ${processed} session(s) created, ${skipped} skipped (already exist)."
