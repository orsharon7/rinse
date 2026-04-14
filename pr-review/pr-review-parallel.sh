#!/usr/bin/env bash
# pr-review-parallel.sh — Orchestrate PR review cycles for multiple PRs
#
# Runs review cycles for N PRs using git worktrees for isolation.
# Supports both parallel (concurrent) and sequential execution.
#
# Usage:
#   ./pr-review-parallel.sh <pr_numbers...> [options]
#   ./pr-review-parallel.sh --all [options]
#
# Examples:
#   # Review PRs 1, 5, 12 in parallel (default)
#   ./pr-review-parallel.sh 1 5 12 --repo owner/repo --cwd /path/to/repo
#
#   # Review all open PRs sequentially
#   ./pr-review-parallel.sh --all --mode sequential --repo owner/repo --cwd /path/to/repo
#
#   # Parallel with concurrency limit and staggered start
#   ./pr-review-parallel.sh --all --max-parallel 3 --stagger 30 --repo owner/repo --cwd /path/to/repo
#
#   # Cleanup orphaned worktrees from previous crashes
#   ./pr-review-parallel.sh --cleanup --cwd /path/to/repo
#
# Options:
#   --repo  <owner/repo>     GitHub repo (default: auto-detect from --cwd)
#   --cwd   <path>           Local repo path (default: current directory)
#   --mode  <parallel|sequential>  Execution mode (default: parallel)
#   --max-parallel <N>       Max concurrent runners in parallel mode (default: 3)
#   --stagger <seconds>      Delay between launching each runner (default: 10)
#   --all                    Auto-discover all open PRs
#   --cleanup                Prune orphaned worktrees and exit
#   --dry-run                Show what would run without executing
#
# Runner options (passed through to the underlying runner):
#   --runner <opencode|claude>   Runner to use (default: opencode)
#   --model  <model>             Model override
#   --reflect                    Enable reflect agent
#   --reflect-main-branch <br>   Branch for reflect rules (default: main)
#   --auto-merge                 Auto-merge on approval
#   --wait-max <seconds>         Max wait per Copilot review cycle
#
set -euo pipefail

# Require Bash 4+ for associative arrays (declare -A)
if [[ "${BASH_VERSINFO[0]}" -lt 4 ]]; then
  echo "Error: pr-review-parallel.sh requires Bash 4+ (found ${BASH_VERSION}). On macOS, install via: brew install bash" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ─── Logging ──────────────────────────────────────────────────────────────────

LOGFILE="${HOME}/.pr-review/logs/parallel-orchestrator.log"
mkdir -p "$(dirname "$LOGFILE")"

log() {
  local ts
  ts=$(date '+%Y-%m-%d %H:%M:%S')
  echo "[$ts] [orchestrator] $*" | tee -a "$LOGFILE" >&2
}

# ─── Color helpers ─────────────────────────────────────────────────────────────

if [[ -t 2 ]]; then
  _R="\033[0m" _B="\033[1m" _D="\033[2m"
  _GREEN="\033[32m" _RED="\033[31m" _YELLOW="\033[33m"
  _CYAN="\033[36m" _BLUE="\033[34m" _MAGENTA="\033[35m"
else
  _R="" _B="" _D="" _GREEN="" _RED="" _YELLOW="" _CYAN="" _BLUE="" _MAGENTA=""
fi

banner() {
  local msg="$1"
  printf "\n%b━━━ %s ━━━%b\n" "${_B}${_BLUE}" "$msg" "${_R}" >&2
}

pr_label() {
  printf "%b#%-4s%b" "${_B}${_MAGENTA}" "$1" "${_R}"
}

# ─── Arg parsing ──────────────────────────────────────────────────────────────

PR_NUMBERS=()
ALL_PRS=false
CLEANUP=false
DRY_RUN=false
REPO=""
CWD="$(pwd)"
MODE="parallel"  # parallel | sequential
MAX_PARALLEL=3
STAGGER=10

# Runner pass-through options
RUNNER="opencode"
RUNNER_MODEL=""
RUNNER_REFLECT=false
RUNNER_REFLECT_BRANCH=""
RUNNER_AUTO_MERGE=false
RUNNER_WAIT_MAX=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --all)                  ALL_PRS=true;                shift ;;
    --cleanup)              CLEANUP=true;                shift ;;
    --dry-run)              DRY_RUN=true;                shift ;;
    --repo)                 REPO="$2";                   shift 2 ;;
    --cwd)                  CWD="$2";                    shift 2 ;;
    --mode)                 MODE="$2";                   shift 2 ;;
    --max-parallel)         MAX_PARALLEL="$2";           shift 2 ;;
    --stagger)              STAGGER="$2";                shift 2 ;;
    --runner)               RUNNER="$2";                 shift 2 ;;
    --model)                RUNNER_MODEL="$2";           shift 2 ;;
    --reflect)              RUNNER_REFLECT=true;         shift ;;
    --reflect-main-branch)  RUNNER_REFLECT_BRANCH="$2";  shift 2 ;;
    --auto-merge)           RUNNER_AUTO_MERGE=true;      shift ;;
    --wait-max)             RUNNER_WAIT_MAX="$2";        shift 2 ;;
    --help|-h)
      head -40 "$0" | grep '^#' | sed 's/^# \?//'
      exit 0
      ;;
    -*)
      >&2 echo "Unknown flag: $1"
      exit 1
      ;;
    *)
      # Positional args are PR numbers
      PR_NUMBERS+=("$1")
      shift
      ;;
  esac
done

# Validate mode
case "$MODE" in
  parallel|sequential) ;;
  *) >&2 echo "Invalid --mode: $MODE (must be 'parallel' or 'sequential')"; exit 1 ;;
esac

# Validate MAX_PARALLEL is numeric and >= 1
if ! [[ "$MAX_PARALLEL" =~ ^[0-9]+$ ]] || [[ "$MAX_PARALLEL" -lt 1 ]]; then
  >&2 echo "--max-parallel must be a number >= 1"
  exit 1
fi

# Validate STAGGER is numeric and >= 0
if ! [[ "$STAGGER" =~ ^[0-9]+$ ]]; then
  >&2 echo "--stagger must be a non-negative integer"
  exit 1
fi

# ─── Repo detection ──────────────────────────────────────────────────────────

if [[ -z "$REPO" ]]; then
  REPO=$(cd "$CWD" && gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || echo "")
  if [[ -z "$REPO" ]]; then
    >&2 echo "Could not detect repo. Use --repo owner/repo or run from inside a git repo."
    exit 1
  fi
fi

REPO_SLUG="${REPO//\//_}"

# ─── Cleanup subcommand ──────────────────────────────────────────────────────

if [[ "$CLEANUP" == true ]]; then
  banner "Cleaning up orphaned worktrees"
  WORKTREE_BASE="/tmp/pr-review-worktrees/${REPO_SLUG}"
  if [[ -d "$WORKTREE_BASE" ]]; then
    for wt in "$WORKTREE_BASE"/pr-*; do
      [[ -d "$wt" ]] || continue
      log "Removing orphaned worktree: $wt"
      git -C "$CWD" worktree remove --force "$wt" 2>/dev/null || true
      rm -rf "$wt" 2>/dev/null || true
    done
    git -C "$CWD" worktree prune 2>/dev/null || true
    log "✓ Cleanup complete"
  else
    log "No worktrees to clean up"
  fi
  exit 0
fi

# ─── Discover PRs ────────────────────────────────────────────────────────────

if [[ "$ALL_PRS" == true ]]; then
  log "Discovering open PRs for ${REPO}..."
  PR_NUMBERS=()
  while IFS= read -r num; do
    [[ -n "$num" ]] && PR_NUMBERS+=("$num")
  done < <(gh pr list --repo "$REPO" --json number --jq '.[].number' 2>/dev/null)
fi

if [[ ${#PR_NUMBERS[@]} -eq 0 ]]; then
  >&2 echo "No PRs to review. Provide PR numbers as arguments or use --all."
  exit 1
fi

# ─── Build runner command ─────────────────────────────────────────────────────

build_runner_cmd() {
  local pr_num="$1"
  local script

  case "$RUNNER" in
    opencode) script="${SCRIPT_DIR}/pr-review-opencode.sh" ;;
    claude)   script="${SCRIPT_DIR}/pr-review-claude-v2.sh" ;;
    *) >&2 echo "Unknown runner: $RUNNER"; exit 1 ;;
  esac

  local cmd=("$script" "$pr_num"
    --repo "$REPO"
    --cwd "$CWD"
    --worktree
    --repo-root "$CWD"
    --no-interactive
  )

  [[ -n "$RUNNER_MODEL" ]]          && cmd+=(--model "$RUNNER_MODEL")
  [[ "$RUNNER_REFLECT" == true ]]   && cmd+=(--reflect)
  [[ -n "$RUNNER_REFLECT_BRANCH" ]] && cmd+=(--reflect-main-branch "$RUNNER_REFLECT_BRANCH")
  [[ "$RUNNER_AUTO_MERGE" == true ]] && cmd+=(--auto-merge)
  [[ -n "$RUNNER_WAIT_MAX" ]]       && cmd+=(--wait-max "$RUNNER_WAIT_MAX")

  # Print each element on its own line (for safe reconstitution as an array)
  printf '%s\n' "${cmd[@]}"
}

# ─── Lock helpers (prevent duplicate runs of same PR) ─────────────────────────

LOCK_DIR="/tmp/pr-review-locks/${REPO_SLUG}"
mkdir -p "$LOCK_DIR"

# Write owner PID to the pidfile. PGID is intentionally not stored: background
# jobs inherit the orchestrator's process group, so a PGID check would consider
# a stale lock active as long as the orchestrator is running.
_write_lock_metadata() {
  local pidfile="$1"
  cat > "$pidfile" <<EOF
owner_pid=$$
EOF
}

# Returns 0 (active) if the owner_pid in the pidfile is still alive.
# Also accepts legacy single-integer pidfiles written by older versions.
_lock_is_active() {
  local pidfile="$1"
  local line owner_pid=""

  [[ -f "$pidfile" ]] || return 1

  while IFS= read -r line; do
    case "$line" in
      owner_pid=*) owner_pid="${line#owner_pid=}" ;;
      pgid=*) ;;  # ignored: PGID check removed (see _write_lock_metadata)
      *)
        # Legacy: plain integer
        if [[ -z "$owner_pid" && "$line" =~ ^[0-9]+$ ]]; then
          owner_pid="$line"
        fi
        ;;
    esac
  done < "$pidfile"

  if [[ -n "$owner_pid" ]] && kill -0 "$owner_pid" 2>/dev/null; then
    return 0
  fi
  return 1
}

acquire_lock() {
  local pr_num="$1"
  local lockdir="${LOCK_DIR}/pr-${pr_num}.lock"
  local pidfile="${lockdir}/pid"

  if mkdir "$lockdir" 2>/dev/null; then
    _write_lock_metadata "$pidfile"
    return 0
  fi

  if _lock_is_active "$pidfile"; then
    return 1  # Another runner is active for this PR
  fi

  # Stale lock — remove it and retry atomic acquisition once
  rm -rf "$lockdir"
  if mkdir "$lockdir" 2>/dev/null; then
    _write_lock_metadata "$pidfile"
    return 0
  fi

  return 1
}

release_lock() {
  local pr_num="$1"
  rm -rf "${LOCK_DIR}/pr-${pr_num}.lock"
}

# ─── Startup ─────────────────────────────────────────────────────────────────

banner "PR Review Orchestrator — ${REPO}"
log "Mode:        ${MODE}"
log "PRs:         ${PR_NUMBERS[*]}"
log "Runner:      ${RUNNER}"
log "Max parallel: ${MAX_PARALLEL}"
log "Stagger:     ${STAGGER}s"
log "Repo path:   ${CWD}"

if [[ "$DRY_RUN" == true ]]; then
  log "[DRY RUN] Commands that would run:"
  for pr in "${PR_NUMBERS[@]}"; do
    log "  PR #${pr}: $(build_runner_cmd "$pr" | tr '\n' ' ')"
  done
  exit 0
fi

# ─── Signal handling ──────────────────────────────────────────────────────────

declare -A CHILD_PIDS  # PR number → PID

CLEANUP_DONE=false

cleanup_all() {
  local interrupted="${1:-false}"
  local had_active_children=false

  if [[ "$CLEANUP_DONE" == true ]]; then
    return
  fi
  CLEANUP_DONE=true

  for pr in "${!CHILD_PIDS[@]}"; do
    local pid="${CHILD_PIDS[$pr]}"
    if kill -0 "$pid" 2>/dev/null; then
      had_active_children=true
      if [[ "$interrupted" == true ]]; then
        log "🛑 Shutting down — killing all runners..."
        interrupted=logged
      fi
      log "   Killing PR #${pr} (PID ${pid})"
      kill "$pid" 2>/dev/null || true
      # Wait up to 10 s for graceful exit, then escalate to SIGKILL before
      # releasing the lock — prevents another instance from starting a duplicate
      # run while the first runner is still alive and mutating the worktree.
      local waited=0
      while kill -0 "$pid" 2>/dev/null && [[ $waited -lt 10 ]]; do
        sleep 1
        waited=$((waited + 1))
      done
      if kill -0 "$pid" 2>/dev/null; then
        log "   PR #${pr} (PID ${pid}) did not exit; sending SIGKILL"
        kill -9 "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
      fi
    fi
    release_lock "$pr"
  done

  if [[ "$had_active_children" == true ]]; then
    # Give children a moment to clean up their worktrees
    sleep 2
    # Prune any leftover worktrees
    git -C "$CWD" worktree prune 2>/dev/null || true
    if [[ "$interrupted" == logged ]]; then
      log "✓ Shutdown complete"
    fi
  fi
}

handle_sigint() {
  cleanup_all true
  exit 130
}

handle_sigterm() {
  cleanup_all true
  exit 143
}

trap 'cleanup_all false' EXIT
trap handle_sigint INT
trap handle_sigterm TERM

# ─── Run a single PR (background) ────────────────────────────────────────────

declare -A PR_EXIT_CODES
declare -A PR_LOG_FILES

run_single_pr() {
  local pr_num="$1"

  if ! acquire_lock "$pr_num"; then
    log "⚠️  PR #${pr_num} is already running (locked) — skipping"
    PR_EXIT_CODES["$pr_num"]="skipped"
    return 0
  fi

  local pr_log="${HOME}/.pr-review/logs/${REPO_SLUG}-pr-${pr_num}.log"
  PR_LOG_FILES["$pr_num"]="$pr_log"

  # Build the command as a proper array (no eval, safe for paths with spaces)
  local cmd_args=()
  while IFS= read -r arg; do
    cmd_args+=("$arg")
  done < <(build_runner_cmd "$pr_num")

  log "🚀 Launching PR #${pr_num}..."
  "${cmd_args[@]}" &
  local pid=$!
  CHILD_PIDS["$pr_num"]=$pid
  # Overwrite the pidfile with the child runner's PID so stale-lock detection
  # reflects the running job. PGID is not stored: the child inherits the
  # orchestrator's process group, so a PGID check would keep stale locks alive.
  cat > "${LOCK_DIR}/pr-${pr_num}.lock/pid" <<EOF
owner_pid=${pid}
EOF
  log "   PID ${pid} → log: ${pr_log}"

  return 0
}

# Wait for a specific PR to complete
wait_for_pr() {
  local pr_num="$1"
  local pid="${CHILD_PIDS[$pr_num]:-}"

  if [[ -z "$pid" ]]; then
    return 0
  fi

  if wait "$pid" 2>/dev/null; then
    PR_EXIT_CODES["$pr_num"]=0
    printf "  %b %-6s %b✓ completed%b\n" "$(pr_label "$pr_num")" "" "${_GREEN}" "${_R}" >&2
  else
    local ec=$?
    PR_EXIT_CODES["$pr_num"]=$ec
    printf "  %b %-6s %b✗ exited %d%b\n" "$(pr_label "$pr_num")" "" "${_RED}" "$ec" "${_R}" >&2
  fi

  release_lock "$pr_num"
  unset 'CHILD_PIDS[$pr_num]'
}

# ─── Active job count ─────────────────────────────────────────────────────────

active_jobs() {
  local count=0
  for pr in "${!CHILD_PIDS[@]}"; do
    local pid="${CHILD_PIDS[$pr]}"
    if kill -0 "$pid" 2>/dev/null; then
      count=$((count + 1))
    fi
  done
  echo "$count"
}

# ─── Execution ────────────────────────────────────────────────────────────────

if [[ "$MODE" == "sequential" ]]; then
  banner "Sequential execution — ${#PR_NUMBERS[@]} PRs"
  for pr in "${PR_NUMBERS[@]}"; do
    printf "\n%b── PR #%s ──────────────────────────────────%b\n" \
      "${_B}${_CYAN}" "$pr" "${_R}" >&2
    run_single_pr "$pr"
    wait_for_pr "$pr"
  done

else
  # Parallel execution with semaphore
  banner "Parallel execution — ${#PR_NUMBERS[@]} PRs (max ${MAX_PARALLEL} concurrent)"

  launched=0
  for pr in "${PR_NUMBERS[@]}"; do
    # Semaphore: wait until we have a slot
    while [[ $(active_jobs) -ge $MAX_PARALLEL ]]; do
      # Reap any completed jobs
      for running_pr in "${!CHILD_PIDS[@]}"; do
        pid="${CHILD_PIDS[$running_pr]}"
        if ! kill -0 "$pid" 2>/dev/null; then
          wait_for_pr "$running_pr"
        fi
      done
      sleep 1
    done

    run_single_pr "$pr"
    launched=$((launched + 1))

    # Stagger start (skip for the last PR)
    if [[ $launched -lt ${#PR_NUMBERS[@]} && "$STAGGER" -gt 0 ]]; then
      log "   ⏳ Staggering ${STAGGER}s before next launch..."
      sleep "$STAGGER"
    fi
  done

  # Wait for remaining jobs
  banner "Waiting for all runners to complete"
  for pr in "${!CHILD_PIDS[@]}"; do
    wait_for_pr "$pr"
  done
fi

# ─── Summary ─────────────────────────────────────────────────────────────────

banner "Results"
total=${#PR_NUMBERS[@]}
succeeded=0
failed=0
skipped=0

for pr in "${PR_NUMBERS[@]}"; do
  local_ec="${PR_EXIT_CODES[$pr]:-unknown}"
  case "$local_ec" in
    0)
      succeeded=$((succeeded + 1))
      printf "  %b  %b✓%b  OK\n" "$(pr_label "$pr")" "${_GREEN}" "${_R}" >&2
      ;;
    skipped)
      skipped=$((skipped + 1))
      printf "  %b  %b○%b  skipped (locked)\n" "$(pr_label "$pr")" "${_YELLOW}" "${_R}" >&2
      ;;
    *)
      failed=$((failed + 1))
      printf "  %b  %b✗%b  exit %s\n" "$(pr_label "$pr")" "${_RED}" "${_R}" "$local_ec" >&2
      ;;
  esac
done

echo "" >&2
printf "%b%d total  ·  %b%d succeeded%b  ·  %b%d failed%b  ·  %b%d skipped%b\n" \
  "${_B}" "$total" \
  "${_GREEN}" "$succeeded" "${_R}" \
  "${_RED}" "$failed" "${_R}" \
  "${_YELLOW}" "$skipped" "${_R}" >&2

# Prune worktrees left behind
git -C "$CWD" worktree prune 2>/dev/null || true

[[ $failed -gt 0 ]] && exit 1
exit 0
