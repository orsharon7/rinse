#!/usr/bin/env bash
# pr-review-cron.sh — Cron PR review poller → system event / notification
# Usage: */2 * * * * /path/to/pr-review-cron.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PR_REVIEW="${SCRIPT_DIR}/pr-review.sh"
LOG="/tmp/pr-review-cron.log"

log() { echo "[$(date '+%H:%M:%S')] $*" >> "$LOG"; }

result=$($PR_REVIEW poll-all 2>/dev/null) || { log "poll-all failed"; exit 1; }

echo "$result" | jq -c '.results[]' 2>/dev/null | while read -r item; do
  status=$(echo "$item" | jq -r '.status')
  pr=$(echo "$item" | jq -r '.pr')
  repo=$(echo "$item" | jq -r '.repo')
  
  case "$status" in
    new_review)
      count=$(echo "$item" | jq -r '.comment_count')
      log "EVENT: ${repo}#${pr} — ${count} comments"
      ;;
    clean|approved)
      log "EVENT: ${repo}#${pr} — approved"
      ;;
    no_change|pending)
      log "No change: ${repo}#${pr}"
      ;;
  esac
done
