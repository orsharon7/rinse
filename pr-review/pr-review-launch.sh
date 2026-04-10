#!/usr/bin/env bash
# pr-review-launch.sh — Interactive TUI launcher for the PR review cycle
#
# Requires: gum (brew install gum)
#
# Usage:
#   ./pr-review-launch.sh
#   ./pr-review-launch.sh <pr_number>
#   ./pr-review-launch.sh <pr_number> --repo owner/repo --cwd /path/to/repo
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ─── Preflight: require gum ───────────────────────────────────────────────────

if ! command -v gum >/dev/null 2>&1; then
  printf "\033[31merror:\033[0m pr-review-launch.sh requires gum.\n"
  printf "  brew install gum\n"
  printf "  https://github.com/charmbracelet/gum\n"
  exit 1
fi

if [[ ! -t 0 || ! -t 1 ]]; then
  echo "pr-review-launch.sh requires an interactive terminal." >&2
  exit 1
fi

# ─── Source shared UI primitives ──────────────────────────────────────────────

# shellcheck source=pr-review-ui.sh
source "${SCRIPT_DIR}/pr-review-ui.sh"

# ─── Theme ────────────────────────────────────────────────────────────────────
# All gum calls share these env vars — set once, apply everywhere.
export GUM_INPUT_CURSOR_FOREGROUND="$GUM_ACCENT"
export GUM_INPUT_PROMPT_FOREGROUND="$GUM_MUTED"
export GUM_CHOOSE_CURSOR_FOREGROUND="$GUM_ACCENT"
export GUM_CHOOSE_SELECTED_FOREGROUND="$GUM_ACCENT"
export GUM_CHOOSE_CURSOR="▶ "
export GUM_CHOOSE_UNSELECTED_PREFIX="  "
export GUM_CONFIRM_PROMPT_FOREGROUND="$GUM_ACCENT"

# ─── Helpers ──────────────────────────────────────────────────────────────────

_label() {
  gum style --bold --foreground "$GUM_ACCENT" "$1"
}

_muted() {
  gum style --foreground "$GUM_MUTED" "$1"
}

_hline() {
  local w
  w=$(tput cols 2>/dev/null || echo 80)
  gum style --foreground "$GUM_MUTED" "$(printf '─%.0s' $(seq 1 "$w"))"
}

_detect_repo()    { gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || echo ""; }
_detect_cwd()     { pwd; }
_detect_default_branch() {
  gh repo view "$1" --json defaultBranchRef -q '.defaultBranchRef.name' 2>/dev/null || echo "main"
}
_detect_open_prs() {
  gh pr list --json number,title,headRefName --limit 10 \
    --jq '.[] | "#\(.number)  \(.headRefName)  — \(.title | .[0:55])"' 2>/dev/null || true
}

# ─── Splash ───────────────────────────────────────────────────────────────────

_splash() {
  clear
  echo ""
  local w
  w=$(tput cols 2>/dev/null || echo 80)

  gum style \
    --bold \
    --foreground "$GUM_ACCENT" \
    --border double \
    --border-foreground "$GUM_ACCENT" \
    --align center \
    --width $(( w - 4 )) \
    --padding "1 4" \
    "pr-review" \
    "" \
    "$(gum style --foreground "$GUM_MUTED" --italic "GitHub Copilot PR Review Automation")"
  echo ""
}

# ─── Wizard ───────────────────────────────────────────────────────────────────

main() {
  _splash

  # Parse any pre-filled CLI args
  local arg_pr="" arg_repo="" arg_cwd=""
  if [[ $# -ge 1 && "$1" =~ ^[0-9]+$ ]]; then arg_pr="$1"; shift; fi
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --repo) arg_repo="$2"; shift 2 ;;
      --cwd)  arg_cwd="$2";  shift 2 ;;
      *)      shift ;;
    esac
  done

  # ── Repository ───────────────────────────────────────────────────────────────
  local detected_repo="${arg_repo:-$(_detect_repo)}"

  _label "  Repository  (owner/repo)"
  local repo
  repo=$(gum input \
    --placeholder "owner/repo" \
    --value "$detected_repo" \
    --prompt "> " \
    --width 60)
  if [[ -z "$repo" ]]; then
    gum style --foreground "$GUM_ERROR" "Repository is required."; exit 1
  fi

  # Detect default branch silently in background while showing next prompt
  local detected_default_branch
  detected_default_branch=$(_detect_default_branch "$repo")

  echo ""

  # ── PR number ────────────────────────────────────────────────────────────────
  local open_prs
  open_prs=$(_detect_open_prs)
  if [[ -n "$open_prs" ]]; then
    _muted "  Open PRs:"
    while IFS= read -r line; do
      _muted "    ${line}"
    done <<< "$open_prs"
    echo ""
  fi

  _label "  PR number"
  local pr
  pr=$(gum input \
    --placeholder "42" \
    --value "$arg_pr" \
    --prompt "> " \
    --width 20)
  if [[ -z "$pr" || ! "$pr" =~ ^[0-9]+$ ]]; then
    gum style --foreground "$GUM_ERROR" "PR number must be a positive integer."; exit 1
  fi

  echo ""

  # ── Local repo path ──────────────────────────────────────────────────────────
  local detected_cwd="${arg_cwd:-$(_detect_cwd)}"

  _label "  Local repo path"
  local cwd
  cwd=$(gum input \
    --placeholder "/path/to/repo" \
    --value "$detected_cwd" \
    --prompt "> " \
    --width 80)
  [[ -z "$cwd" ]] && cwd="$detected_cwd"
  if [[ ! -d "$cwd" ]]; then
    gum style --foreground "$GUM_WARN" "Warning: directory '${cwd}' does not exist — continuing anyway."
  fi

  echo ""

  # ── Runner ───────────────────────────────────────────────────────────────────
  _label "  Runner"
  local runner_choice
  runner_choice=$(printf '%s\n' \
    "opencode  (GitHub Copilot — no API key needed)" \
    "claude v2  (Claude Code — requires Anthropic API key)" \
    "claude v1  (legacy)" \
    | gum choose --height 6)

  local runner_script runner_label default_model
  case "$runner_choice" in
    opencode*)
      runner_script="${SCRIPT_DIR}/pr-review-opencode.sh"
      runner_label="opencode"
      default_model="github-copilot/claude-sonnet-4.6"
      ;;
    "claude v2"*)
      runner_script="${SCRIPT_DIR}/pr-review-claude-v2.sh"
      runner_label="claude v2"
      default_model="claude-sonnet-4-6"
      ;;
    *)
      runner_script="${SCRIPT_DIR}/pr-review-claude.sh"
      runner_label="claude v1"
      default_model=""
      ;;
  esac

  echo ""

  # ── Model override ───────────────────────────────────────────────────────────
  _label "  Model  $(gum style --foreground "$GUM_MUTED" "(leave blank for default: ${default_model})")"
  local model
  model=$(gum input \
    --placeholder "$default_model" \
    --value "" \
    --prompt "> " \
    --width 60)

  echo ""

  # ── Reflection ───────────────────────────────────────────────────────────────
  local reflect="false"
  local reflect_branch="$detected_default_branch"

  if [[ "$runner_label" != "claude v1" ]]; then
    _label "  Reflection agent"
    _muted "  Extracts coding rules from Copilot comments → pushes to ${detected_default_branch}"
    echo ""
    if gum confirm "Enable reflection?" \
      --affirmative "Yes" \
      --negative "No" \
      --default=false; then
      reflect="true"
      echo ""
      _label "  Branch to push reflection rules to"
      reflect_branch=$(gum input \
        --placeholder "$detected_default_branch" \
        --value "$detected_default_branch" \
        --prompt "> " \
        --width 40)
      [[ -z "$reflect_branch" ]] && reflect_branch="$detected_default_branch"
    fi
  else
    _muted "  Reflection not available for claude v1 — skipping."
  fi

  echo ""

  # ── Advanced ─────────────────────────────────────────────────────────────────
  _label "  Max wait per Copilot review  $(gum style --foreground "$GUM_MUTED" "(seconds)")"
  local wait_max
  wait_max=$(gum input \
    --placeholder "300" \
    --value "300" \
    --prompt "> " \
    --width 20)
  [[ -z "$wait_max" || ! "$wait_max" =~ ^[0-9]+$ || "$wait_max" -lt 1 ]] && wait_max=300

  echo ""

  local dry_run="false"
  if gum confirm "Dry run?  (print command, don't execute)" \
    --affirmative "Yes" \
    --negative "No" \
    --default=false; then
    dry_run="true"
  fi

  # ── Summary ──────────────────────────────────────────────────────────────────
  echo ""
  _hline

  gum style --bold "  Review your settings"

  _hline
  echo ""

  local rows=(
    "PR|#${pr}"
    "Repository|${repo}"
    "Local path|${cwd}"
    "Runner|${runner_label}"
    "Model|${model:-${default_model} (default)}"
    "Reflection|${reflect}"
  )
  [[ "$reflect" == "true" ]] && rows+=("Reflect branch|${reflect_branch}")
  rows+=("Wait max|${wait_max}s")
  [[ "$dry_run" == "true" ]] && rows+=("Dry run|yes")

  for row in "${rows[@]}"; do
    local key="${row%%|*}"
    local val="${row##*|}"
    printf "  %s  %s\n" \
      "$(gum style --foreground "$GUM_MUTED" "$(printf '%-16s' "$key")")" \
      "$(gum style --bold "$val")"
  done

  echo ""
  _hline
  echo ""

  # ── Confirm ──────────────────────────────────────────────────────────────────
  if ! gum confirm "Launch PR review cycle?" \
    --affirmative "Launch" \
    --negative "Abort" \
    --default=true; then
    echo ""
    _muted "  Aborted."
    exit 0
  fi

  # ── Build & exec ─────────────────────────────────────────────────────────────
  local cmd=("$runner_script" "$pr" "--repo" "$repo" "--cwd" "$cwd" "--wait-max" "$wait_max")
  [[ -n "$model" ]] && cmd+=("--model" "$model")
  [[ "$reflect" == "true" ]] && cmd+=("--reflect" "--reflect-main-branch" "$reflect_branch")
  [[ "$dry_run" == "true" ]] && cmd+=("--dry-run")

  # Launch banner
  clear
  echo ""
  local w
  w=$(tput cols 2>/dev/null || echo 80)
  gum style \
    --bold \
    --foreground "$GUM_ACCENT" \
    --border normal \
    --border-foreground "$GUM_ACCENT" \
    --padding "0 2" \
    --width $(( w - 4 )) \
    "PR #${pr}  ·  ${repo}  ·  ${runner_label}"
  echo ""

  exec "${cmd[@]}"
}

main "$@"
