#!/usr/bin/env bash
# pr-review-reflect.sh — Extract coding rules from Copilot review comments
#
# Reads Copilot review comments for a PR, runs an AI agent to identify patterns,
# and appends NEW rules to AGENTS.md in the project repo.
#
# AGENTS.md is the cross-agent standard (GitHub Copilot, opencode, Cursor, etc.).
# CLAUDE.md is a symlink to AGENTS.md for Claude Code compatibility.
#
# Rules are written inside a clearly delimited section so existing content
# is never touched:
#   <!-- BEGIN:COPILOT-RULES -->
#   ...auto-maintained rules...
#   <!-- END:COPILOT-RULES -->
#
# The reflection is committed and pushed directly to main (not the PR branch)
# using a git worktree, so it never triggers another Copilot review cycle.
#
# Usage (standalone):
#   ./pr-review-reflect.sh <pr_number> --repo <owner/repo> --cwd <path> [options]
#
# Usage (called from runner):
#   REFLECT_COMMENTS_JSON="..." ./pr-review-reflect.sh <pr_number> ...
#
# Options:
#   --repo         <owner/repo>      GitHub repo
#   --cwd          <path>            Local repo path (PR branch working tree)
#   --main-branch  <branch>          Branch to push rules to (default: main)
#   --review-id    <id>              Specific review ID to analyse (default: latest Copilot review)
#   --model        <provider/model>  AI model for reflection (default: github-copilot/claude-sonnet-4.6)
#   --agent        <claude|opencode> Which CLI to use (default: opencode)
#   --dry-run                        Print prompt without running agent
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# LOGFILE is scoped per-repo after REPO is known (see below)
LOGFILE=""

log() {
  local ts
  ts=$(date '+%Y-%m-%d %H:%M:%S')
  if [[ -n "${LOGFILE:-}" ]]; then
    echo "[$ts] [reflect] $*" | tee -a "$LOGFILE"
  else
    echo "[$ts] [reflect] $*"
  fi
}

# Retry a command up to N times with exponential backoff.
# Usage: retry <max_attempts> <command> [args...]
retry() {
  local max="$1"; shift
  local attempt=1 delay=2
  while true; do
    if "$@"; then
      return 0
    fi
    if [[ $attempt -ge $max ]]; then
      log "⚠️  Command failed after ${max} attempts: $*"
      return 1
    fi
    log "   Retry ${attempt}/${max} in ${delay}s..."
    sleep "$delay"
    attempt=$(( attempt + 1 ))
    delay=$(( delay * 2 ))
  done
}

# ─── Args ─────────────────────────────────────────────────────────────────────

if [[ $# -lt 1 || "$1" == "--help" || "$1" == "-h" ]]; then
  head -35 "$0" | grep '^#' | sed 's/^# \?//'; exit 0
fi

PR_NUMBER="$1"; shift

REPO=""
CWD="$(pwd)"
MAIN_BRANCH="main"
REVIEW_ID=""
MODEL="github-copilot/claude-sonnet-4.6"
AGENT_CLI="opencode"
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)          REPO="$2";          shift 2 ;;
    --cwd)           CWD="$2";           shift 2 ;;
    --main-branch)   MAIN_BRANCH="$2";   shift 2 ;;
    --review-id)     REVIEW_ID="$2";     shift 2 ;;
    --model)         MODEL="$2";         shift 2 ;;
    --agent)         AGENT_CLI="$2";     shift 2 ;;
    --dry-run)       DRY_RUN=true;       shift ;;
    *) >&2 echo "Unknown arg: $1"; exit 1 ;;
  esac
done

if [[ -z "$REPO" ]]; then
  REPO=$(cd "$CWD" && gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  [[ -z "$REPO" ]] && { >&2 echo "Cannot detect repo. Use --repo."; exit 1; }
fi

# ─── Scoped log (per-repo isolation for parallel runs) ────────────────────────

REPO_SLUG="${REPO//\//_}"  # owner/repo → owner_repo
LOGFILE="${HOME}/.pr-review/logs/${REPO_SLUG}-pr-${PR_NUMBER}-reflect.log"
mkdir -p "$(dirname "$LOGFILE")"

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

# ─── Set up git worktree on main ─────────────────────────────────────────────
#
# We write AGENTS.md into a worktree checked out on $MAIN_BRANCH,
# not into $CWD (the PR branch). This means:
#   • The reflection commit never lands on the PR branch
#   • Copilot won't re-review the rule files → no infinite loop
#   • Rules are available on main immediately, before the PR merges
#   • CLAUDE.md is a symlink to AGENTS.md — no separate write needed

WORKTREE_DIR=$(mktemp -d "${TMPDIR:-/tmp}/pr-reflect-worktree.XXXXXX")

cleanup_worktree() {
  log "Cleaning up worktree at ${WORKTREE_DIR}..."
  git -C "$CWD" worktree remove --force "$WORKTREE_DIR" 2>/dev/null || true
  rm -rf "$WORKTREE_DIR"
}
trap cleanup_worktree EXIT

# Prune stale worktree references left by previous crashed runs
git -C "$CWD" worktree prune 2>/dev/null || true

log "Fetching ${MAIN_BRANCH} and creating worktree at ${WORKTREE_DIR}..."
retry 3 bash -c 'set -euo pipefail; git -C "$1" fetch origin "$2" 2>&1 | tee -a "$3"' _ "$CWD" "$MAIN_BRANCH" "$LOGFILE"
# Use --detach to avoid conflicts with an already checked-out branch
git -C "$CWD" worktree add --detach "$WORKTREE_DIR" "origin/${MAIN_BRANCH}" 2>&1 | tee -a "$LOGFILE"

# Point rule file at the worktree (main), not the PR branch
AGENTS_FILE="${WORKTREE_DIR}/AGENTS.md"

# ─── Ensure rule file exists ───────────────────────────────────────────────────

if [[ ! -f "$AGENTS_FILE" ]]; then
  cat > "$AGENTS_FILE" << EOF
# AGENTS

Project instructions for AI coding agents.

EOF
  log "Created ${AGENTS_FILE}"
fi

# Ensure CLAUDE.md is a symlink pointing to AGENTS.md for Claude Code compatibility
# shellcheck source=_symlink-helper.sh
source "${SCRIPT_DIR}/_symlink-helper.sh"
ensure_claude_symlink "$WORKTREE_DIR"

# ─── Build reflection prompt ──────────────────────────────────────────────────

agents_current=$(cat "$AGENTS_FILE")

read -r -d '' PROMPT << PROMPT_EOF || true
You are a code quality analyst. Your job is to extract reusable coding rules from GitHub Copilot review comments and add them permanently to this project's AI agent instruction file, AGENTS.md.

## Copilot review comments to analyse (PR #${PR_NUMBER} in ${REPO}):
\`\`\`json
${comments_json}
\`\`\`

## Current AGENTS.md:
\`\`\`markdown
${agents_current}
\`\`\`

## Your task

1. Read the Copilot comments and identify **recurring patterns and principles** — not just individual fixes, but the underlying rules that would have prevented these issues (e.g. "always validate user input at API boundaries", "never import unused modules", "use chunked reads for uploads").

2. Write 3-10 concise, actionable rules in imperative style:
   - "Always ..."
   - "Never ..."
   - "Use X for Y"
   - "When Z, ensure W"

3. Check the existing \`<!-- BEGIN:COPILOT-RULES -->\` section in **${AGENTS_FILE}** (if present) and **do not duplicate rules that already exist there**.

4. Update **${AGENTS_FILE}** by replacing the rules section between the markers (create the section if it doesn't exist):
- Find \`<!-- BEGIN:COPILOT-RULES -->\` and \`<!-- END:COPILOT-RULES -->\` markers
- Replace the content between them with the merged (existing + new) rules
- If markers don't exist, append the entire section to the end of the file

Note: CLAUDE.md is a symlink to AGENTS.md — do NOT write to it separately.

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

## Rules for this task
- Only write rules that are generalizable beyond this PR — skip one-off fixes
- Keep rules concise (one line each)
- Group under clear category headers
- Preserve all existing file content outside the markers
- If no genuinely new rules can be extracted, make no changes
- Do NOT run any git commands — the script will handle committing and pushing
PROMPT_EOF

# ─── Run agent ────────────────────────────────────────────────────────────────

if [[ "$DRY_RUN" == true ]]; then
  log "[DRY RUN] Worktree: ${WORKTREE_DIR} (branch: ${MAIN_BRANCH})"
  log "[DRY RUN] Prompt:"
  echo "$PROMPT"
  exit 0
fi

case "$AGENT_CLI" in
  opencode)
    oc_exit=0
    (cd "$WORKTREE_DIR" && opencode run --model "$MODEL" "$PROMPT") 2>&1 | tee -a "$LOGFILE" || oc_exit=$?
    [[ $oc_exit -ne 0 ]] && { log "⚠️  opencode exited ${oc_exit}"; exit 1; }
    ;;
  claude)
    cl_exit=0
    (cd "$WORKTREE_DIR" && claude --print --dangerously-skip-permissions --model "$MODEL" "$PROMPT") \
      2>&1 | tee -a "$LOGFILE" || cl_exit=$?
    [[ $cl_exit -ne 0 ]] && { log "⚠️  claude exited ${cl_exit}"; exit 1; }
    ;;
  *)
    >&2 echo "Unknown --agent: $AGENT_CLI (use 'opencode' or 'claude')"; exit 1 ;;
esac

# ─── Commit and push from worktree to main ────────────────────────────────────
#
# The agent only edits files — the script owns git operations.
# This runs in the worktree (main branch), completely isolated from the PR branch.

changed=$(git -C "$WORKTREE_DIR" status --porcelain AGENTS.md CLAUDE.md)
if [[ -z "$changed" ]]; then
  log "No changes to AGENTS.md or CLAUDE.md — nothing to commit"
  exit 0
fi

if [[ ! -f "$WORKTREE_DIR/AGENTS.md" ]]; then
  log "❌ AGENTS.md no longer exists after agent execution; it may have been deleted or renamed. Refusing to stage/commit."
  exit 1
fi

# Re-enforce the symlink invariant immediately before staging so any agent
# damage to CLAUDE.md is repaired at commit time (not just at script startup).
ensure_claude_symlink "$WORKTREE_DIR"
log "Committing updated rules to ${MAIN_BRANCH}..."

git -C "$WORKTREE_DIR" add AGENTS.md
# Stage CLAUDE.md if it is a symlink and has uncommitted changes (e.g. repaired by ensure_claude_symlink).
if [[ -L "$WORKTREE_DIR/CLAUDE.md" ]] && \
   [[ -n "$(git -C "$WORKTREE_DIR" status --porcelain -- CLAUDE.md)" ]]; then
  git -C "$WORKTREE_DIR" add CLAUDE.md
fi

# Count added markdown bullet lines (lines starting with "- ") in the staged AGENTS.md diff
rules_added=$(git -C "$WORKTREE_DIR" diff --cached AGENTS.md \
  | grep '^+' | grep -v '^+++' | grep -c '^\+- ' 2>/dev/null || echo "0")

git -C "$WORKTREE_DIR" commit -m "chore: update AI coding rules from Copilot review #${PR_NUMBER} [skip ci]"
retry 3 git -C "$WORKTREE_DIR" push origin "HEAD:${MAIN_BRANCH}"

log "✓ Reflection complete — +${rules_added} rule(s) pushed to ${MAIN_BRANCH}"
