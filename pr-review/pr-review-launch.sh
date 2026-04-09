#!/usr/bin/env bash
# pr-review-launch.sh — Interactive launcher for the PR review cycle
#
# A next-gen TUI entry point. Walks you through all options step-by-step,
# shows a confirmation summary, then hands off to the selected runner.
#
# Usage:
#   ./pr-review-launch.sh
#   ./pr-review-launch.sh <pr_number>          # skip the PR number prompt
#   ./pr-review-launch.sh <pr_number> --repo owner/repo --cwd /path
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Shared state for _fc_draw / _field_choice (avoids nested function declarations)
_fc_options=()
_fc_n=0
_fc_selected=0

# ─── Source shared UI primitives ──────────────────────────────────────────────

# shellcheck source=pr-review-ui.sh
source "${SCRIPT_DIR}/pr-review-ui.sh"

# ─── Terminal geometry ────────────────────────────────────────────────────────

_term_width() {
  local w
  w=$(tput cols 2>/dev/null || echo 80)
  [[ $w -lt 40 ]] && w=40
  [[ $w -gt 120 ]] && w=120
  echo "$w"
}

_center() {
  local text="$1"
  local width="${2:-$(_term_width)}"
  local visible_len="${#text}"
  local pad=$(( (width - visible_len) / 2 ))
  [[ $pad -lt 0 ]] && pad=0
  printf "%${pad}s%s\n" "" "$text"
}

_hline() {
  local char="${1:-─}"
  local width
  width=$(_term_width)
  printf '%s\n' "$(printf "${char}%.0s" $(seq 1 "$width"))"
}

# ─── Splash screen ────────────────────────────────────────────────────────────

_splash() {
  local w
  w=$(_term_width)

  clear

  echo ""
  _ui_print "${C_BOLD}${C_BLUE}$(_hline '━')${C_RESET}"
  echo ""
  _ui_print "${C_BOLD}${C_WHITE}$(_center "pr-review" "$w")${C_RESET}"
  _ui_print "${C_DIM}$(_center "GitHub Copilot PR Review Automation" "$w")${C_RESET}"
  echo ""
  _ui_print "${C_BOLD}${C_BLUE}$(_hline '━')${C_RESET}"
  echo ""
}

# ─── Input primitives ─────────────────────────────────────────────────────────

# _field_text <label> <default> → prints prompt, reads free-text, echos result
# If user hits enter with no input, returns <default>.
_field_text() {
  local label="$1"
  local default="$2"

  local hint=""
  [[ -n "$default" ]] && hint="${C_DIM}  (${default})${C_RESET}"

  printf "%b" "\n${C_BOLD}${C_WHITE}  ${label}${C_RESET}${hint}\n" >&2
  printf "%b" "  ${C_CYAN}›${C_RESET} " >&2

  local val
  IFS= read -r val </dev/tty 2>/dev/null || val=""
  val="${val:-$default}"
  echo "$val"
}

# _field_choice <label> <selected_index> <option1> <option2> ...
# Arrow-key selection. Returns 0-based index on stdout.
#
# Uses _fc_options / _fc_n / _fc_selected as shared state with _fc_draw.
_fc_draw() {
  for (( i=0; i<_fc_n; i++ )); do
    if [[ $i -eq $_fc_selected ]]; then
      printf "  %b\n" "${C_CYAN}${C_BOLD}▶  ${_fc_options[$i]}${C_RESET}" >&2
    else
      printf "  %b\n" "${C_DIM}   ${_fc_options[$i]}${C_RESET}" >&2
    fi
  done
}

_field_choice() {
  local label="$1"
  local initial="$2"
  shift 2
  _fc_options=("$@")
  _fc_n=${#_fc_options[@]}
  _fc_selected="$initial"

  printf "%b" "\n${C_BOLD}${C_WHITE}  ${label}${C_RESET}\n" >&2
  printf "%b" "  ${C_DIM}↑↓ to move, Enter to confirm${C_RESET}\n" >&2
  echo "" >&2

  # Hide cursor
  printf "\033[?25l" >&2
  trap 'printf "\033[?25h" >&2' RETURN

  _fc_draw

  while true; do
    local key seq
    IFS= read -r -s -n1 key </dev/tty 2>/dev/null || key=""
    if [[ "$key" == $'\x1b' ]]; then
      IFS= read -r -s -n2 seq </dev/tty 2>/dev/null || seq=""
      key="${key}${seq}"
    fi

    case "$key" in
      $'\x1b[A'|$'\x1bOA'|'k')
        _fc_selected=$(( (_fc_selected - 1 + _fc_n) % _fc_n ))
        ;;
      $'\x1b[B'|$'\x1bOB'|'j')
        _fc_selected=$(( (_fc_selected + 1) % _fc_n ))
        ;;
      $'\n'|$'\r'|'')
        printf "\033[?25h" >&2
        # Collapse to single selected line
        printf "\033[%dA\033[J" $_fc_n >&2
        printf "  %b\n" "${C_CYAN}▶  ${C_BOLD}${_fc_options[$_fc_selected]}${C_RESET}" >&2
        echo "$_fc_selected"
        return
        ;;
      'q'|$'\x03')
        printf "\033[?25h" >&2
        printf "\033[%dA\033[J" $_fc_n >&2
        echo "$_fc_selected"
        return
        ;;
    esac

    printf "\033[%dA" $_fc_n >&2
    _fc_draw
  done
}

# _field_toggle <label> <default: true|false> → prints prompt, returns true/false
_field_toggle() {
  local label="$1"
  local default="$2"

  local initial=0
  [[ "$default" == "true" ]] && initial=1

  local idx
  idx=$(_field_choice "$label" "$initial" "No" "Yes")
  [[ "$idx" == "1" ]] && echo "true" || echo "false"
}

# ─── Auto-detect helpers ──────────────────────────────────────────────────────

_detect_repo() {
  gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || echo ""
}

_detect_cwd() {
  pwd
}

_detect_open_prs() {
  gh pr list --json number,title,headRefName --limit 10 2>/dev/null \
    | jq -r '.[] | "#\(.number)  \(.headRefName)  — \(.title | .[0:50])"' 2>/dev/null \
    || echo ""
}

_detect_default_branch() {
  local repo="$1"
  gh repo view "$repo" --json defaultBranchRef -q '.defaultBranchRef.name' 2>/dev/null || echo "main"
}

# ─── Summary box ─────────────────────────────────────────────────────────────

_summary_box() {
  local pr="$1"
  local repo="$2"
  local cwd="$3"
  local runner="$4"
  local model="$5"
  local reflect="$6"
  local reflect_branch="$7"
  local wait_max="$8"
  local dry_run="$9"

  local w
  w=$(_term_width)
  local inner=$(( w - 4 ))

  _row() {
    local key="$1" val="$2"
    printf "  %b%-18s%b %b%s%b\n" \
      "${C_DIM}" "$key" "${C_RESET}" \
      "${C_WHITE}" "$val" "${C_RESET}" >&2
  }

  echo "" >&2
  _ui_print "${C_BOLD}${C_BLUE}$(_hline '─')${C_RESET}" >&2
  _ui_print "${C_BOLD}  Review your settings${C_RESET}" >&2
  _ui_print "${C_BLUE}$(_hline '─')${C_RESET}" >&2
  echo "" >&2

  _row "PR"           "#${pr}"
  _row "Repository"   "$repo"
  _row "Local path"   "$cwd"
  _row "Runner"       "$runner"
  _row "Model"        "${model:-(runner default)}"
  _row "Reflection"   "$reflect"
  [[ "$reflect" == "true" ]] && _row "Reflect branch" "$reflect_branch"
  _row "Wait max"     "${wait_max}s"
  [[ "$dry_run" == "true" ]] && _row "Dry run"       "yes"

  echo "" >&2
  _ui_print "${C_BLUE}$(_hline '─')${C_RESET}" >&2
  echo "" >&2
}

# ─── Launch banner (printed after form, before runner takes over) ──────────────

_launch_banner() {
  local pr="$1" repo="$2" runner="$3"
  clear
  echo ""
  _ui_print "${C_BOLD}${C_BLUE}$(_hline '━')${C_RESET}"
  _ui_print "${C_BOLD}${C_WHITE}  PR #${pr}  ${C_DIM}·  ${repo}  ·  ${runner}${C_RESET}"
  _ui_print "${C_BLUE}$(_hline '━')${C_RESET}"
  echo ""
}

# ─── Wizard ───────────────────────────────────────────────────────────────────

main() {
  # Require interactive terminal
  if [[ ! -t 0 || ! -t 1 ]]; then
    echo "pr-review-launch.sh requires an interactive terminal." >&2
    exit 1
  fi

  _splash

  # ── Sniff CLI args for pre-filled values ────────────────────────────────────
  local arg_pr=""
  local arg_repo=""
  local arg_cwd=""

  if [[ $# -ge 1 && "$1" =~ ^[0-9]+$ ]]; then
    arg_pr="$1"; shift
  fi
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --repo) arg_repo="$2"; shift 2 ;;
      --cwd)  arg_cwd="$2";  shift 2 ;;
      *)      shift ;;
    esac
  done

  # ── Step 1: Repository ──────────────────────────────────────────────────────
  local detected_repo
  detected_repo="${arg_repo:-$(_detect_repo)}"

  _ui_print "${C_DIM}  Step 1 of 7${C_RESET}"
  local repo
  repo=$(_field_text "GitHub repository  (owner/repo)" "$detected_repo")
  if [[ -z "$repo" ]]; then
    _ui_print "${C_ERROR}  Repository is required.${C_RESET}"
    exit 1
  fi

  # Detect default branch for this repo (used as reflect-branch default in step 6b)
  local detected_default_branch
  detected_default_branch=$(_detect_default_branch "$repo")

  # ── Step 2: PR number ───────────────────────────────────────────────────────
  _ui_print "\n${C_DIM}  Step 2 of 7${C_RESET}"

  # Show open PRs as context if we can
  local open_prs
  open_prs=$(_detect_open_prs)
  if [[ -n "$open_prs" ]]; then
    echo "" >&2
    _ui_print "${C_DIM}  Open PRs:${C_RESET}"
    while IFS= read -r line; do
      _ui_print "  ${C_DIM}${line}${C_RESET}"
    done <<< "$open_prs"
  fi

  local pr
  pr=$(_field_text "PR number" "$arg_pr")
  if [[ -z "$pr" || ! "$pr" =~ ^[0-9]+$ ]]; then
    _ui_print "${C_ERROR}  PR number must be a positive integer.${C_RESET}"
    exit 1
  fi

  # ── Step 3: Local repo path ─────────────────────────────────────────────────
  _ui_print "\n${C_DIM}  Step 3 of 7${C_RESET}"
  local detected_cwd="${arg_cwd:-$(_detect_cwd)}"
  local cwd
  cwd=$(_field_text "Local repo path  (cwd)" "$detected_cwd")
  if [[ ! -d "$cwd" ]]; then
    _ui_print "${C_WARN}  Warning: directory '${cwd}' does not exist — continuing anyway.${C_RESET}"
  fi

  # ── Step 4: Runner ──────────────────────────────────────────────────────────
  _ui_print "\n${C_DIM}  Step 4 of 7${C_RESET}"
  local runner_idx
  runner_idx=$(_field_choice "Runner" 0 \
    "opencode  (GitHub Copilot — no API key needed)" \
    "claude v2  (Claude Code — requires Anthropic API key)" \
    "claude v1  (legacy)")

  local runner_script runner_label default_model
  case "$runner_idx" in
    0) runner_script="${SCRIPT_DIR}/pr-review-opencode.sh";    runner_label="opencode";    default_model="github-copilot/claude-sonnet-4.6" ;;
    1) runner_script="${SCRIPT_DIR}/pr-review-claude-v2.sh";   runner_label="claude v2";   default_model="claude-sonnet-4-6" ;;
    2) runner_script="${SCRIPT_DIR}/pr-review-claude.sh";      runner_label="claude v1";   default_model="" ;;
  esac

  # ── Step 5: Model (optional override) ───────────────────────────────────────
  _ui_print "\n${C_DIM}  Step 5 of 7${C_RESET}"
  local model
  model=$(_field_text "Model override  (leave blank for default)" "")
  # Empty means no override; later command construction should only add --model
  # when the user explicitly entered a non-empty value.

  # ── Step 6: Reflection ──────────────────────────────────────────────────────
  _ui_print "\n${C_DIM}  Step 6 of 7${C_RESET}"

  local reflect
  if [[ "$runner_label" == "claude v1" ]]; then
    # v1 does not support --reflect
    _ui_print "  ${C_DIM}Reflection not available for claude v1 — skipping.${C_RESET}"
    reflect="false"
  else
    reflect=$(_field_toggle "Enable reflection agent?  (extracts coding rules → pushes to main)" "false")
  fi

  local reflect_branch="$detected_default_branch"
  if [[ "$reflect" == "true" ]]; then
    _ui_print "\n${C_DIM}  Step 6b of 7${C_RESET}"
    reflect_branch=$(_field_text "Branch to push reflection rules to" "$detected_default_branch")
  fi

  # ── Step 7: Advanced options ─────────────────────────────────────────────────
  _ui_print "\n${C_DIM}  Step 7 of 7${C_RESET}"
  local wait_max
  wait_max=$(_field_text "Max wait per Copilot review  (seconds)" "300")
  [[ -z "$wait_max" || ! "$wait_max" =~ ^[0-9]+$ ]] && wait_max=300

  local dry_run
  dry_run=$(_field_toggle "Dry run?  (print command, don't execute)" "false")

  # ── Summary + confirm ────────────────────────────────────────────────────────
  _summary_box "$pr" "$repo" "$cwd" "$runner_label" "$model" "$reflect" "$reflect_branch" "$wait_max" "$dry_run"

  local confirm_idx
  confirm_idx=$(_field_choice "Ready?" 0 \
    "Launch" \
    "Abort")

  if [[ "$confirm_idx" != "0" ]]; then
    echo ""
    _ui_print "${C_MUTED}  Aborted.${C_RESET}"
    exit 0
  fi

  # ── Build command ─────────────────────────────────────────────────────────────
  local cmd=("$runner_script" "$pr" "--repo" "$repo" "--cwd" "$cwd" "--wait-max" "$wait_max")

  [[ -n "$model" ]] && cmd+=("--model" "$model")
  [[ "$reflect" == "true" ]] && cmd+=("--reflect" "--reflect-main-branch" "$reflect_branch")
  [[ "$dry_run" == "true" ]] && cmd+=("--dry-run")

  _launch_banner "$pr" "$repo" "$runner_label"

  # Hand off — runner takes over the terminal from here
  exec "${cmd[@]}"
}

main "$@"
