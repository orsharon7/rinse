#!/usr/bin/env bash
# backfill-sessions.sh — Extract metrics from /tmp/rinse-pr*.log files
# and write session JSON files to ~/.rinse/sessions/
#
# Usage: ./scripts/backfill-sessions.sh [--dry-run] [--overwrite]

set -euo pipefail

SESSIONS_DIR="${HOME}/.rinse/sessions"
LOG_GLOB="/tmp/rinse-pr*.log"
DRY_RUN=false
OVERWRITE=false

for arg in "$@"; do
  case "$arg" in
    --dry-run)   DRY_RUN=true ;;
    --overwrite) OVERWRITE=true ;;
    *) echo "Unknown argument: $arg"; exit 1 ;;
  esac
done

mkdir -p "$SESSIONS_DIR"

shopt -s nullglob
log_files=($LOG_GLOB)

if [[ ${#log_files[@]} -eq 0 ]]; then
  echo "No log files found matching $LOG_GLOB"
  exit 0
fi

echo "Found ${#log_files[@]} log file(s) in /tmp/"
created=0
skipped=0
failed=0

for log_file in "${log_files[@]}"; do
  filename="$(basename "$log_file")"

  # Extract PR number from filename: rinse-pr45.log or rinse-pr45-cycle.log
  pr_num="$(echo "$filename" | grep -oE 'pr[0-9]+' | grep -oE '[0-9]+')"
  if [[ -z "$pr_num" ]]; then
    echo "  [SKIP] Cannot parse PR number from: $filename"
    ((failed++)) || true
    continue
  fi

  # Skip logs with no useful content (< 100 bytes)
  log_size="$(wc -c < "$log_file")"
  if [[ "$log_size" -lt 100 ]]; then
    echo "  [SKIP] PR #${pr_num}: log too small (${log_size} bytes) — likely failed run"
    ((failed++)) || true
    continue
  fi

  # Check if session already exists (any file matching *-PR${pr_num}.json)
  existing="$(ls "${SESSIONS_DIR}"/*-PR${pr_num}.json 2>/dev/null | head -1 || true)"
  if [[ -n "$existing" && "$OVERWRITE" == "false" ]]; then
    echo "  [SKIP] PR #${pr_num}: session already exists at $(basename "$existing")"
    ((skipped++)) || true
    continue
  fi

  # Extract timestamps: first and last [YYYY-MM-DD HH:MM:SS] lines
  # Use -a to handle logs with binary/unicode box-drawing characters
  ts_pattern='\[20[0-9][0-9]-[0-9][0-9]-[0-9][0-9] [0-9][0-9]:[0-9][0-9]:[0-9][0-9]\]'
  start_line="$(grep -a -m1 -E "$ts_pattern" "$log_file" 2>/dev/null || true)"
  end_line="$(grep -a -E "$ts_pattern" "$log_file" 2>/dev/null | tail -1 || true)"

  if [[ -z "$start_line" || -z "$end_line" ]]; then
    echo "  [SKIP] PR #${pr_num}: no timestamps found in log"
    ((failed++)) || true
    continue
  fi

  # Parse timestamps → ISO 8601: [2026-04-17 12:17:52] some text
  start_ts="$(echo "$start_line" | grep -oE '20[0-9]{2}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}')"
  end_ts="$(echo "$end_line" | grep -oE '20[0-9]{2}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}')"
  started_at="${start_ts/ /T}Z"
  ended_at="${end_ts/ /T}Z"

  # Date prefix for filename: YYYYMMDD-HHMMSS
  date_prefix="$(echo "$start_ts" | tr -d ':-' | tr ' ' '-')"

  # Extract repo from log line like: "   Repo:        orsharon7/rinse#45"
  repo_raw="$(grep -a -E 'Repo:' "$log_file" 2>/dev/null | head -1 | sed 's/.*Repo:[[:space:]]*//' | awk '{print $1}' || true)"
  repo="$(echo "$repo_raw" | sed "s/#${pr_num}//")"
  if [[ -z "$repo" ]]; then
    repo="orsharon7/rinse"
  fi

  # Extract model
  model="$(grep -a -E 'Model:' "$log_file" 2>/dev/null | head -1 | sed 's/.*Model:[[:space:]]*//' | awk '{print $1}' || true)"
  if [[ -z "$model" ]]; then
    model="github-copilot/claude-sonnet-4.6"
  fi

  # Count iterations (lines with ━━━ Iteration N)
  iterations="$(grep -a -c 'Iteration' "$log_file" 2>/dev/null || echo 0)"
  # Narrow to actual iteration header lines only
  iterations="$(grep -a -E '^.{0,10}Iteration [0-9]+' "$log_file" 2>/dev/null | wc -l | tr -d ' ' || echo 0)"

  # Count total comments: sum numbers from "N comment(s) in review NNNN" lines
  # Deduplicate by review ID to avoid double-counting (log prints same line twice)
  total_comments=0
  while IFS=' ' read -r cnt _rest; do
    if [[ "$cnt" =~ ^[0-9]+$ ]]; then
      total_comments=$((total_comments + cnt))
    fi
  done < <(grep -a -E '[0-9]+ comment\(s\) in review [0-9]+' "$log_file" 2>/dev/null \
    | grep -oE '[0-9]+ comment\(s\) in review [0-9]+' \
    | sort -u \
    | grep -oE '^[0-9]+' || true)

  # Determine approved: PR merged
  approved=false
  if grep -a -qE 'PR merged|merged successfully|✅.*merge' "$log_file" 2>/dev/null; then
    approved=true
  fi

  # Repo slug for filename
  repo_slug="$(echo "$repo" | tr '/' '-')"
  out_file="${SESSIONS_DIR}/${date_prefix}-${repo_slug}-PR${pr_num}.json"

  # Build JSON
  json="{
  \"started_at\": \"${started_at}\",
  \"ended_at\": \"${ended_at}\",
  \"repo\": \"${repo}\",
  \"pr\": \"${pr_num}\",
  \"runner\": \"opencode\",
  \"model\": \"${model}\",
  \"total_comments\": ${total_comments},
  \"iterations\": ${iterations},
  \"approved\": ${approved}
}"

  if [[ "$DRY_RUN" == "true" ]]; then
    echo "  [DRY-RUN] PR #${pr_num}: would write $(basename "$out_file")"
    echo "$json"
  else
    echo "$json" > "$out_file"
    echo "  [OK] PR #${pr_num}: wrote $(basename "$out_file") (iterations=${iterations}, comments=${total_comments}, approved=${approved})"
    ((created++)) || true
  fi
done

echo ""
echo "Done. created=${created}, skipped=${skipped}, failed=${failed}"
