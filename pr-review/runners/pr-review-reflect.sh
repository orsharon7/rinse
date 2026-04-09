#!/usr/bin/env bash
# pr-review-reflect.sh — Extract coding rules from Copilot review comments
#
# Reads Copilot review comments for a PR, runs an AI agent to identify patterns,
# and appends NEW rules to AGENTS.md and CLAUDE.md in the project repo.
#
# Both files are read automatically by AI coding agents:
#   AGENTS.md  — opencode (primary), Codex, other agents
#   CLAUDE.md  — Claude Code (primary), opencode (fallback)
#
# Rules are written inside a clearly delimited section so existing content
# is never touched:
#   <!-- BEGIN:COPILOT-RULES -->
#   ...auto-maintained rules...
#   <!-- END:COPILOT-RULES -->
#
# Usage (standalone):
#   ./pr-review-reflect.sh <pr_number> --repo <owner/repo> --cwd <path> [options]
#
# Usage (called from runner):
#   REFLECT_COMMENTS_JSON="..." ./pr-review-reflect.sh <pr_number> ...
#
# Options:
#   --repo  <owner/repo>         GitHub repo
#   --cwd   <path>               Local repo path (where AGENTS.md / CLAUDE.md live)
#   --review-id <id>             Specific review ID to analyse (default: latest Copilot review)
#   --model <provider/model>     AI model for reflection (default: github-copilot/claude-sonnet-4.6)
#   --agent <claude|opencode>    Which CLI to use (default: opencode)
#   --dry-run                    Print prompt without running agent
#
set -euo pipefail

LOGFILE="${HOME}/.pr-review-reflect.log"

log() {
  local ts
  ts=$(date '+%Y-%m-%d %H:%M:%S')
  echo "[$ts] [reflect] $*" | tee -a "$LOGFILE"
}

# ─── Args ─────────────────────────────────────────────────────────────────────

if [[ $# -lt 1 || "$1" == "--help" || "$1" == "-h" ]]; then
  head -35 "$0" | grep '^#' | sed 's/^# \?//'; exit 0
fi

PR_NUMBER="$1"; shift

REPO=""
CWD="$(pwd)"
REVIEW_ID=""
MODEL="github-copilot/claude-sonnet-4.6"
AGENT_CLI="opencode"
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)       REPO="$2";       shift 2 ;;
    --cwd)        CWD="$2";        shift 2 ;;
    --review-id)  REVIEW_ID="$2";  shift 2 ;;
    --model)      MODEL="$2";      shift 2 ;;
    --agent)      AGENT_CLI="$2";  shift 2 ;;
    --dry-run)    DRY_RUN=true;    shift ;;
    *) >&2 echo "Unknown arg: $1"; exit 1 ;;
  esac
done

if [[ -z "$REPO" ]]; then
  REPO=$(cd "$CWD" && gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  [[ -z "$REPO" ]] && { >&2 echo "Cannot detect repo. Use --repo."; exit 1; }
fi

# ─── Get comments ─────────────────────────────────────────────────────────────

# Use pre-fetched comments if passed via env (from runner scripts)
if [[ -n "${REFLECT_COMMENTS_JSON:-}" ]]; then
  comments_json="$REFLECT_COMMENTS_JSON"
  log "Using pre-fetched comments from env"
else
  # Fetch from GitHub
  if [[ -z "$REVIEW_ID" ]]; then
    REVIEW_ID=$(gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews?per_page=100" \
      --jq '[.[] | select(.user.login | test("copilot"; "i"))] | sort_by(.submitted_at) | last | .id' \
      2>/dev/null || echo "")
    [[ -z "$REVIEW_ID" || "$REVIEW_ID" == "null" ]] && { log "No Copilot review found — nothing to reflect on"; exit 0; }
  fi

  comments_json=$(gh api --paginate "repos/${REPO}/pulls/${PR_NUMBER}/reviews/${REVIEW_ID}/comments" \
    --jq '[.[] | select(.in_reply_to_id == null) | {path, line: .original_line, body}]' \
    2>/dev/null || echo "[]")
fi

comment_count=$(echo "$comments_json" | jq 'length')
if [[ "$comment_count" -eq 0 ]]; then
  log "No top-level comments to reflect on — skipping"
  exit 0
fi

log "Reflecting on ${comment_count} comment(s) for ${REPO}#${PR_NUMBER}..."

# ─── Ensure rule files exist ──────────────────────────────────────────────────

AGENTS_FILE="${CWD}/AGENTS.md"
CLAUDE_FILE="${CWD}/CLAUDE.md"

for f in "$AGENTS_FILE" "$CLAUDE_FILE"; do
  if [[ ! -f "$f" ]]; then
    basename_f=$(basename "$f")
    cat > "$f" << EOF
# ${basename_f%.*}

Project instructions for AI coding agents.

EOF
    log "Created ${f}"
  fi
done

# ─── Build reflection prompt ──────────────────────────────────────────────────

agents_current=$(cat "$AGENTS_FILE")
claude_current=$(cat "$CLAUDE_FILE")

read -r -d '' PROMPT << PROMPT_EOF || true
You are a code quality analyst. Your job is to extract reusable coding rules from GitHub Copilot review comments and add them permanently to this project's AI agent instruction files.

## Copilot review comments to analyse (PR #${PR_NUMBER} in ${REPO}):
\`\`\`json
${comments_json}
\`\`\`

## Current AGENTS.md:
\`\`\`markdown
${agents_current}
\`\`\`

## Current CLAUDE.md:
\`\`\`markdown
${claude_current}
\`\`\`

## Your task

1. Read the Copilot comments and identify **recurring patterns and principles** — not just individual fixes, but the underlying rules that would have prevented these issues (e.g. "always validate user input at API boundaries", "never import unused modules", "use chunked reads for uploads").

2. Write 3-10 concise, actionable rules in imperative style:
   - "Always ..."
   - "Never ..."
   - "Use X for Y"
   - "When Z, ensure W"

3. Check the existing \`<!-- BEGIN:COPILOT-RULES -->\` section in each file (if present) and **do not duplicate rules that already exist there**.

4. Update BOTH files by replacing the rules section between the markers (create the section if it doesn't exist):

For **${AGENTS_FILE}**:
- Find \`<!-- BEGIN:COPILOT-RULES -->\` and \`<!-- END:COPILOT-RULES -->\` markers
- Replace the content between them with the merged (existing + new) rules
- If markers don't exist, append the entire section to the end of the file

For **${CLAUDE_FILE}**:
- Same process — keep it in sync with AGENTS.md rules section

The section format must be exactly:
\`\`\`
<!-- BEGIN:COPILOT-RULES -->
## Coding Guidelines (AI-maintained)
*Auto-updated by pr-review-reflect — do not edit this section manually.*
*Last updated: $(date '+%Y-%m-%d') from PR #${PR_NUMBER} review*

### [Category e.g. Security & Validation]
- Rule

### [Category e.g. Code Quality]
- Rule

<!-- END:COPILOT-RULES -->
\`\`\`

5. After updating both files, commit and push:
\`\`\`bash
cd "${CWD}" && git add AGENTS.md CLAUDE.md && git commit -m "chore: update AI coding rules from Copilot review #${PR_NUMBER}" && git push
\`\`\`

## Rules for this task
- Only write rules that are generalizable beyond this PR — skip one-off fixes
- Keep rules concise (one line each)
- Group under clear category headers
- Preserve all existing file content outside the markers
- If no genuinely new rules can be extracted, do not commit
PROMPT_EOF

# ─── Run agent ────────────────────────────────────────────────────────────────

if [[ "$DRY_RUN" == true ]]; then
  log "[DRY RUN] Prompt:"
  echo "$PROMPT"
  exit 0
fi

case "$AGENT_CLI" in
  opencode)
    oc_exit=0
    opencode run --model "$MODEL" --dir "$CWD" "$PROMPT" 2>&1 | tee -a "$LOGFILE" || oc_exit=$?
    [[ $oc_exit -ne 0 ]] && { log "⚠️  opencode exited ${oc_exit}"; exit 1; }
    ;;
  claude)
    cl_exit=0
    (cd "$CWD" && claude --print --dangerously-skip-permissions --model "$MODEL" "$PROMPT") \
      2>&1 | tee -a "$LOGFILE" || cl_exit=$?
    [[ $cl_exit -ne 0 ]] && { log "⚠️  claude exited ${cl_exit}"; exit 1; }
    ;;
  *)
    >&2 echo "Unknown --agent: $AGENT_CLI (use 'opencode' or 'claude')"; exit 1 ;;
esac

log "✓ Reflection complete — AGENTS.md and CLAUDE.md updated"
