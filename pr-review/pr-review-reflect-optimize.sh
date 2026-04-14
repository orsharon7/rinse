#!/usr/bin/env bash
# pr-review-reflect-optimize.sh — Consolidate and compress AI coding rules
#
# Reads the <!-- BEGIN:COPILOT-RULES --> section from AGENTS.md and CLAUDE.md
# and runs an AI agent to deduplicate, merge, and trim the rules — producing a
# leaner version that preserves all meaningful guidance at 30-50% fewer tokens.
#
# Called automatically by pr-review-opencode.sh after --auto-merge completes.
#
# Usage (standalone):
#   ./pr-review-reflect-optimize.sh <pr_number> --repo <owner/repo> --cwd <path> [options]
#
# Usage (called from runner after merge):
#   ./pr-review-reflect-optimize.sh <pr_number> --repo <owner/repo> --cwd <path>
#
# Options:
#   --repo         <owner/repo>      GitHub repo
#   --cwd          <path>            Local repo path (used for git worktree source)
#   --main-branch  <branch>          Branch to push optimized rules to (default: main)
#   --model        <provider/model>  AI model for optimization (default: github-copilot/claude-sonnet-4.6)
#   --agent        <claude|opencode> Which CLI to use (default: opencode)
#   --dry-run                        Print prompt without running agent
#
set -euo pipefail

# LOGFILE is scoped per-repo after REPO is known (see below)
LOGFILE=""

log() {
  local ts
  ts=$(date '+%Y-%m-%d %H:%M:%S')
  if [[ -n "${LOGFILE:-}" ]]; then
    echo "[$ts] [optimize] $*" | tee -a "$LOGFILE"
  else
    echo "[$ts] [optimize] $*"
  fi
}

# Retry a command up to N times with exponential backoff.
retry() {
  local max="$1"; shift
  local attempt=1 delay=2
  while true; do
    if "$@"; then return 0; fi
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
MODEL="github-copilot/claude-sonnet-4.6"
AGENT_CLI="opencode"
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)          REPO="$2";          shift 2 ;;
    --cwd)           CWD="$2";           shift 2 ;;
    --main-branch)   MAIN_BRANCH="$2";   shift 2 ;;
    --model)         MODEL="$2";         shift 2 ;;
    --agent)         AGENT_CLI="$2";     shift 2 ;;
    --dry-run)       DRY_RUN=true;       shift ;;
    --skip-if-open-prs) SKIP_IF_OPEN_PRS=true; shift ;;
    *) >&2 echo "Unknown arg: $1"; exit 1 ;;
  esac
done

SKIP_IF_OPEN_PRS="${SKIP_IF_OPEN_PRS:-false}"

if [[ -z "$REPO" ]]; then
  REPO=$(cd "$CWD" && gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  [[ -z "$REPO" ]] && { >&2 echo "Cannot detect repo. Use --repo."; exit 1; }
fi

# ─── Scoped log (per-repo isolation for parallel runs) ────────────────────────

REPO_SLUG="${REPO//\//_}"  # owner/repo → owner_repo
LOGFILE="${HOME}/.pr-review/logs/${REPO_SLUG}-pr-${PR_NUMBER}-reflect-optimize.log"
mkdir -p "$(dirname "$LOGFILE")"

# ─── Skip early if open PRs exist (mid-cycle guard) ─────────────────────────

if [[ "$SKIP_IF_OPEN_PRS" == true ]]; then
  open_count=$(gh pr list --repo "$REPO" --base "$MAIN_BRANCH" --state open --json number --jq 'length' 2>/dev/null || echo 0)
  if [[ "$open_count" -gt 0 ]]; then
    log "Skipping mid-cycle push — ${open_count} open PR(s) against ${MAIN_BRANCH} (would cause merge conflict)"
    exit 0
  fi
fi

# ─── Set up git worktree on main ─────────────────────────────────────────────

WORKTREE_DIR=$(mktemp -d "${TMPDIR:-/tmp}/pr-reflect-optimize.XXXXXX")

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

AGENTS_FILE="${WORKTREE_DIR}/AGENTS.md"
CLAUDE_FILE="${WORKTREE_DIR}/CLAUDE.md"

# ─── Check that at least one file has a COPILOT-RULES section ────────────────

has_rules=false
for f in "$AGENTS_FILE" "$CLAUDE_FILE"; do
  [[ -f "$f" ]] && grep -q 'BEGIN:COPILOT-RULES' "$f" && has_rules=true
done

if [[ "$has_rules" == false ]]; then
  log "No <!-- BEGIN:COPILOT-RULES --> section found in either file — nothing to optimize"
  exit 0
fi

# ─── Read current file content ────────────────────────────────────────────────

agents_current=""
claude_current=""
[[ -f "$AGENTS_FILE" ]] && agents_current=$(cat "$AGENTS_FILE")
[[ -f "$CLAUDE_FILE" ]]  && claude_current=$(cat "$CLAUDE_FILE")

# ─── Build optimization prompt ────────────────────────────────────────────────

read -r -d '' PROMPT << PROMPT_EOF || true
You are a technical editor tasked with transforming AI coding-rule files into lean, actionable practices after PR #${PR_NUMBER} in ${REPO} was merged.

The files below each contain a rules section bounded by:
  <!-- BEGIN:COPILOT-RULES -->
  ...
  <!-- END:COPILOT-RULES -->

Your job is to rewrite the content *between* those markers (keeping the markers themselves) so that every rule is a **clear, terse practice** — not verbose contextual guidance.

## What a practice looks like

A practice is a single imperative sentence that an agent can follow without needing surrounding explanation. It states **what to do** (or not do) and **why only when the reason is non-obvious**.

### Good (practice)
- Use \`strings.Builder\`; never \`+=\` in a loop.
- Validate numeric CLI params as integers ≥ 0 before arithmetic/\`sleep\`.
- Close async streams via \`await stream.aclose()\` in \`try/finally\`.

### Bad (verbose context — what to avoid)
- When writing Go code that builds strings iteratively, you should always use strings.Builder instead of using += because += creates a new string allocation on every iteration which leads to O(n²) performance.
- It's important to validate numeric CLI parameters. Parameters like --stagger should be validated as integers that are greater than or equal to zero before they are used in arithmetic operations or passed to sleep, since invalid values can cause unexpected behavior.

## Transformation rules

1. **Convert to practices** — rewrite each rule as a terse imperative. Strip preambles, hedge words, and explanations that restate the imperative.
2. **Deduplicate** — if two bullets say the same thing, keep the clearest one.
3. **Merge overlapping rules** — if two rules cover the same root concern, combine into one concise practice.
4. **Compact examples** — inline short code examples with backticks. Drop multi-line code blocks unless the pattern genuinely needs them.
5. **Use sub-bullets sparingly** — bold-labeled sub-bullets (e.g. \`**Locking:**\`) are fine for grouping 2-3 tightly related practices under a theme. Never nest deeper than one level.
6. **Regroup** — organize under the most logical category headers; merge headers if that reduces repetition.
7. **Reorder** — highest-impact / most frequently relevant practices first within each category.
8. **Preserve substance** — do NOT drop guidance that is meaningfully distinct. When in doubt, keep it.
9. **Update the datestamp** — change the "*Last updated: ...*" line to: $(date '+%Y-%m-%d') from PR #${PR_NUMBER} review (optimized)

## Current AGENTS.md:
\`\`\`markdown
${agents_current}
\`\`\`

## Current CLAUDE.md:
\`\`\`markdown
${claude_current}
\`\`\`

## Output instructions

Rewrite **both** files in full, keeping all content outside the COPILOT-RULES markers exactly as-is.
Only the content *between* the markers (and the datestamp line) should change.
Both files must have identical COPILOT-RULES content.
Do NOT run any git commands — the script will handle committing and pushing.
PROMPT_EOF

# ─── Run agent ────────────────────────────────────────────────────────────────

if [[ "$DRY_RUN" == true ]]; then
  log "[DRY RUN] Worktree: ${WORKTREE_DIR} (branch: ${MAIN_BRANCH})"
  log "[DRY RUN] Prompt:"
  echo "$PROMPT"
  exit 0
fi

log "Running ${AGENT_CLI} optimization pass for ${REPO}#${PR_NUMBER} (model: ${MODEL})..."

agent_exit=0
case "$AGENT_CLI" in
  opencode)
    (cd "$WORKTREE_DIR" && opencode run --model "$MODEL" "$PROMPT") 2>&1 | tee -a "$LOGFILE" || agent_exit=$?
    ;;
  claude)
    (cd "$WORKTREE_DIR" && claude --print --dangerously-skip-permissions --model "$MODEL" "$PROMPT") \
      2>&1 | tee -a "$LOGFILE" || agent_exit=$?
    ;;
  *)
    >&2 echo "Unknown --agent: $AGENT_CLI (use 'opencode' or 'claude')"; exit 1 ;;
esac

# ─── Validate agent output ────────────────────────────────────────────────────

changed=$(git -C "$WORKTREE_DIR" status --porcelain AGENTS.md CLAUDE.md)

if [[ $agent_exit -ne 0 ]]; then
  if [[ -n "$changed" ]]; then
    log "⚠️  ${AGENT_CLI} exited ${agent_exit} but produced file changes — validating output"
  else
    log "⚠️  ${AGENT_CLI} exited ${agent_exit} with no file changes"
    exit 1
  fi
fi

if [[ -z "$changed" ]]; then
  log "No changes to AGENTS.md or CLAUDE.md — rules already compact"
  exit 0
fi

# Verify COPILOT-RULES markers survived the rewrite
for f in "$AGENTS_FILE" "$CLAUDE_FILE"; do
  if [[ -f "$f" ]] && ! grep -q 'BEGIN:COPILOT-RULES' "$f"; then
    log "⚠️  ${f##*/} is missing BEGIN:COPILOT-RULES marker after agent rewrite — aborting"
    git -C "$WORKTREE_DIR" checkout -- AGENTS.md CLAUDE.md 2>/dev/null
    exit 1
  fi
  if [[ -f "$f" ]] && ! grep -q 'END:COPILOT-RULES' "$f"; then
    log "⚠️  ${f##*/} is missing END:COPILOT-RULES marker after agent rewrite — aborting"
    git -C "$WORKTREE_DIR" checkout -- AGENTS.md CLAUDE.md 2>/dev/null
    exit 1
  fi
done

log "Committing optimized rules to ${MAIN_BRANCH}..."
git -C "$WORKTREE_DIR" add AGENTS.md CLAUDE.md

# Rough measure: lines removed (negative diff lines inside the rules section)
lines_removed=$(git -C "$WORKTREE_DIR" diff --cached AGENTS.md CLAUDE.md \
  | grep '^-' | grep -v '^---' | wc -l | tr -d ' ' 2>/dev/null || echo "?")

git -C "$WORKTREE_DIR" commit \
  -m "chore: optimize AI coding rules after PR #${PR_NUMBER} merge [skip ci]"
retry 3 git -C "$WORKTREE_DIR" push origin "HEAD:${MAIN_BRANCH}"

log "✓ Optimization complete — ~${lines_removed} line(s) removed, pushed to ${MAIN_BRANCH}"
