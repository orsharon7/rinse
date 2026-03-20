#!/usr/bin/env bash
# pr-review-cron.sh — Cron PR review poller → OpenClaw system event
# Usage: */2 * * * * /path/to/pr-review-cron.sh
set -uo pipefail
export PATH="/home/luli/.nvm/versions/node/v22.22.1/bin:$PATH"

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
      openclaw system event --mode now --text "PR #${pr} review landed: ${count} comments. Fix and continue cycle." 2>/dev/null
      # Telegram fallback
      curl -s "https://api.telegram.org/bot8540775929:AAHbve8r2qZ3D4byIhixEcHtgWFQWXHay9c/sendMessage" \
        -d "chat_id=5559631374" -d "text=🔍 PR #${pr} review: ${count} comments" > /dev/null 2>&1
      log "EVENT: ${repo}#${pr} — ${count} comments"
      ;;
    clean|approved)
      openclaw system event --mode now --text "PR #${pr} — Copilot approved (0 comments). Ready to merge." 2>/dev/null
      curl -s "https://api.telegram.org/bot8540775929:AAHbve8r2qZ3D4byIhixEcHtgWFQWXHay9c/sendMessage" \
        -d "chat_id=5559631374" -d "text=✅ PR #${pr} approved! Ready to merge." > /dev/null 2>&1
      log "EVENT: ${repo}#${pr} — approved"
      ;;
    no_change|pending)
      log "No change: ${repo}#${pr}"
      ;;
  esac
done
