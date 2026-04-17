#!/usr/bin/env bash
# backfill-sessions.sh — Extract metrics from today's RINSE log files and write
# them to ~/.rinse/stats.json so that `rinse stats` shows real data.
#
# Usage:
#   scripts/backfill-sessions.sh [--log-dir /tmp] [--dry-run] [--repo owner/repo]
#
# By default reads all /tmp/rinse-pr*.log files and appends JSON Lines records
# to ~/.rinse/stats.json (same schema as pr-review-stats.sh).

set -euo pipefail

# ─── Defaults ─────────────────────────────────────────────────────────────────

LOG_DIR="/tmp"
DRY_RUN=false
REPO="orsharon7/rinse"
MODEL="github-copilot/claude-sonnet-4.6"
RINSE_DIR="${HOME}/.rinse"
STATS_FILE="${RINSE_DIR}/stats.json"
SCHEMA_VERSION=1

# ─── Argument parsing ─────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
  case "$1" in
    --log-dir)    LOG_DIR="$2"; shift 2 ;;
    --dry-run)    DRY_RUN=true; shift ;;
    --repo)       REPO="$2"; shift 2 ;;
    --stats-file) STATS_FILE="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,10p' "$0" | sed 's/^# \?//'
      exit 0 ;;
    *)
      >&2 echo "Unknown argument: $1"
      exit 1 ;;
  esac
done

mkdir -p "$RINSE_DIR"

# ─── Python parser script (written to temp file) ──────────────────────────────

_PARSER=$(mktemp /tmp/rinse_backfill_parser.XXXXXX.py)
trap 'rm -f "$_PARSER"' EXIT

cat > "$_PARSER" << 'PYEOF'
#!/usr/bin/env python3
"""
Parse a single RINSE log file and emit a JSON Lines record to stdout.
Exit 0 always; output starts with "OK " + json or "SKIP " + reason.
"""
import sys, re, json
from datetime import datetime, timezone

log_file    = sys.argv[1]
pr_number   = int(sys.argv[2])
repo        = sys.argv[3]
model       = sys.argv[4]
schema_ver  = int(sys.argv[5])

try:
    with open(log_file, 'r', errors='replace') as f:
        content = f.read()
except Exception as e:
    print(f"SKIP cannot read file: {e}")
    sys.exit(0)

# ── Timestamps ────────────────────────────────────────────────────────────────
ts_pattern = re.compile(r'\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\]')
timestamps = ts_pattern.findall(content)

if not timestamps:
    print("SKIP no timestamps found")
    sys.exit(0)

fmt = "%Y-%m-%d %H:%M:%S"
first_dt = datetime.strptime(timestamps[0], fmt).replace(tzinfo=timezone.utc)
last_dt  = datetime.strptime(timestamps[-1], fmt).replace(tzinfo=timezone.utc)
duration = int((last_dt - first_dt).total_seconds())
timestamp_iso = first_dt.strftime("%Y-%m-%dT%H:%M:%SZ")

# ── Iterations ────────────────────────────────────────────────────────────────
iterations = len(re.findall(r'━━━ Iteration', content))

# ── Comments resolved ─────────────────────────────────────────────────────────
# Lines like: "💬  4 comment(s) in review 412345"
comment_counts = re.findall(r'💬\s+(\d+)\s+comment', content)
comments_resolved = sum(int(n) for n in comment_counts)

# ── Branch name ───────────────────────────────────────────────────────────────
branch_m = re.search(r'branch:\s*([^)\n]+)', content)
branch = branch_m.group(1).strip() if branch_m else "unknown"

# ── Outcome ───────────────────────────────────────────────────────────────────
if re.search(r'Merged', content):
    outcome = "merged"
elif re.search(r'PR closed', content):
    outcome = "closed"
elif re.search(r'aborted', content, re.IGNORECASE):
    outcome = "aborted"
elif re.search(r'[Cc]lean review|0 comments.*ready to merge', content):
    outcome = "clean"
elif re.search(r'[Aa]pproved', content) and iterations > 0:
    outcome = "approved"
else:
    outcome = "error"

record = {
    "schema_version":    schema_ver,
    "timestamp":         timestamp_iso,
    "repo":              repo,
    "pr_number":         pr_number,
    "model":             model,
    "duration_seconds":  duration,
    "iterations":        iterations,
    "comments_resolved": comments_resolved,
    "outcome":           outcome,
    "_branch":           branch,
}
print("OK " + json.dumps(record))
PYEOF

# ─── Collect log files ────────────────────────────────────────────────────────

shopt -s nullglob
log_files=("${LOG_DIR}"/rinse-pr*.log)
shopt -u nullglob

if [[ ${#log_files[@]} -eq 0 ]]; then
  echo "No log files found matching ${LOG_DIR}/rinse-pr*.log"
  exit 0
fi

echo "Backfilling ${#log_files[@]} log file(s) → ${STATS_FILE}"
[[ "$DRY_RUN" == true ]] && echo "(dry-run: no writes)"
echo ""

records_written=0
records_skipped=0

for log_file in "${log_files[@]}"; do
  filename=$(basename "$log_file")

  # PR number from filename (e.g. rinse-pr45.log or rinse-pr20-cycle.log)
  pr_number=$(python3 -c "
import re, sys
m = re.search(r'pr(\d+)', sys.argv[1])
print(m.group(1) if m else '')
" "$filename")

  if [[ -z "$pr_number" ]]; then
    echo "  SKIP $filename — cannot parse PR number"
    (( records_skipped++ )) || true
    continue
  fi

  result=$(python3 "$_PARSER" "$log_file" "$pr_number" "$REPO" "$MODEL" "$SCHEMA_VERSION")
  status="${result:0:2}"

  if [[ "$status" == "SK" ]]; then
    reason="${result#SKIP }"
    echo "  SKIP PR #${pr_number} — ${reason}"
    (( records_skipped++ )) || true
    continue
  fi

  # Strip "OK " prefix
  record="${result#OK }"

  # Extract display fields with python
  read -r outcome iters comments dur branch < <(python3 -c "
import sys, json
d = json.loads(sys.argv[1])
print(d['outcome'], d['iterations'], d['comments_resolved'], d['duration_seconds'], d['_branch'])
" "$record")

  # Strip _branch before writing to stats file
  stats_record=$(python3 -c "
import sys, json
d = json.loads(sys.argv[1])
d.pop('_branch', None)
print(json.dumps(d))
" "$record")

  printf "  PR #%-4s  %-8s  %3s iter  %3s comments  %5ss  %s\n" \
    "$pr_number" "$outcome" "$iters" "$comments" "$dur" "$branch"

  if [[ "$DRY_RUN" == false ]]; then
    echo "$stats_record" >> "$STATS_FILE"
  fi

  (( records_written++ )) || true
done

echo ""
echo "Done: ${records_written} record(s) written, ${records_skipped} skipped."
if [[ "$DRY_RUN" == false && $records_written -gt 0 ]]; then
  echo "Run 'scripts/pr-review-stats.sh show' to view stats."
fi
