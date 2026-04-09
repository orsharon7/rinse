#!/usr/bin/env bash
# pr-review-ui.sh — Shared terminal UI for pr-review runners
#
# Source this file to get:
#   - ANSI color/style helpers
#   - Structured log() with colored severity levels
#   - Section header / divider functions
#   - Animated spinner (background process)
#   - Interactive post-success menu (merge, branch cleanup, open PR)
#
# Usage:
#   source "$(dirname "$0")/pr-review-ui.sh"
#
# All public functions are prefixed with ui_ to avoid namespace collisions.
# Respects NO_COLOR env var and non-interactive terminals (falls back gracefully).

# ─── TTY / color detection ────────────────────────────────────────────────────

_UI_TTY=false
_UI_COLOR=false

if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
  _UI_TTY=true
  _UI_COLOR=true
fi

# Override: force-disable interactive features (e.g. called in CI or with --no-interactive)
[[ "${PR_REVIEW_NO_INTERACTIVE:-}" == "true" ]] && _UI_TTY=false

# ─── ANSI codes ───────────────────────────────────────────────────────────────

if [[ "$_UI_COLOR" == true ]]; then
  C_RESET="\033[0m"
  C_BOLD="\033[1m"
  C_DIM="\033[2m"

  C_BLACK="\033[30m"
  C_RED="\033[31m"
  C_GREEN="\033[32m"
  C_YELLOW="\033[33m"
  C_BLUE="\033[34m"
  C_MAGENTA="\033[35m"
  C_CYAN="\033[36m"
  C_WHITE="\033[37m"

  C_BG_GREEN="\033[42m"
  C_BG_RED="\033[41m"
  C_BG_BLUE="\033[44m"

  # Named semantic colors
  C_SUCCESS="$C_GREEN"
  C_ERROR="$C_RED"
  C_WARN="$C_YELLOW"
  C_INFO="$C_CYAN"
  C_MUTED="$C_DIM"
  C_HEADER="$C_BOLD$C_BLUE"
  C_ACCENT="$C_MAGENTA"
else
  C_RESET="" C_BOLD="" C_DIM=""
  C_BLACK="" C_RED="" C_GREEN="" C_YELLOW="" C_BLUE="" C_MAGENTA="" C_CYAN="" C_WHITE=""
  C_BG_GREEN="" C_BG_RED="" C_BG_BLUE=""
  C_SUCCESS="" C_ERROR="" C_WARN="" C_INFO="" C_MUTED="" C_HEADER="" C_ACCENT=""
fi

# ─── Helpers ──────────────────────────────────────────────────────────────────

_ui_print() { printf "%b\n" "$*"; }
_ui_print_n() { printf "%b" "$*"; }  # no newline

ui_ts() { date '+%H:%M:%S'; }
ui_ts_full() { date '+%Y-%m-%d %H:%M:%S'; }

# ─── log() — replaces plain log() in runner scripts ──────────────────────────
#
# Usage: log "message"          → info
#        log "✅ ..." → plain (emoji carries intent)
#
# Also appends to $LOGFILE (must be set in caller).

log() {
  local msg="$*"
  local ts
  ts=$(ui_ts_full)

  # Detect severity from leading emoji / keyword for coloring
  local color="$C_RESET"
  case "$msg" in
    "✅"*|"🎉"*)       color="$C_SUCCESS$C_BOLD" ;;
    "❌"*)              color="$C_ERROR$C_BOLD" ;;
    "⚠️"*)             color="$C_WARN" ;;
    "⏳"*|"🔍"*)       color="$C_MUTED" ;;
    "📨"*)             color="$C_INFO" ;;
    "━━━"*)            color="$C_HEADER" ;;
    "🚀"*)             color="$C_ACCENT$C_BOLD" ;;
    "💬"*)             color="$C_CYAN" ;;
    "   "*|"→"*)       color="$C_MUTED" ;;
  esac

  # Colored output to terminal
  _ui_print "${C_DIM}[${ts}]${C_RESET} ${color}${msg}${C_RESET}"

  # Plain text to log file
  [[ -n "${LOGFILE:-}" ]] && echo "[$ts] $msg" >> "$LOGFILE"
}

# ─── Section headers ──────────────────────────────────────────────────────────

ui_header() {
  local title="$1"
  local width=70
  local line
  line=$(printf '━%.0s' $(seq 1 $width))
  echo ""
  _ui_print "${C_HEADER}${line}${C_RESET}"
  _ui_print "${C_HEADER}  ${title}${C_RESET}"
  _ui_print "${C_HEADER}${line}${C_RESET}"
  echo ""
}

ui_divider() {
  local width=70
  local line
  line=$(printf '─%.0s' $(seq 1 $width))
  _ui_print "${C_MUTED}${line}${C_RESET}"
}

ui_iter_header() {
  local iter="$1"
  local ts
  ts=$(ui_ts)
  local pad
  pad=$(printf '━%.0s' $(seq 1 55))
  _ui_print "\n${C_HEADER}━━━ Iteration ${iter}  ${C_DIM}${ts}${C_RESET}${C_HEADER}  ${pad}${C_RESET}"
}

# ─── Spinner ──────────────────────────────────────────────────────────────────
#
# ui_spinner_start "message"  → prints spinning animation, returns PID in UI_SPINNER_PID
# ui_spinner_stop [ok|fail]   → stops spinner, prints final status line

UI_SPINNER_PID=""
_UI_SPINNER_MSG=""

ui_spinner_start() {
  [[ "$_UI_TTY" != true ]] && return 0
  _UI_SPINNER_MSG="$1"
  local frames=('⠋' '⠙' '⠹' '⠸' '⠼' '⠴' '⠦' '⠧' '⠇' '⠏')
  (
    local i=0
    while true; do
      local f="${frames[$((i % ${#frames[@]}))]}"
      printf "\r${C_CYAN}${f}${C_RESET}  ${_UI_SPINNER_MSG}   " >&2
      sleep 0.1
      i=$(( i + 1 ))
    done
  ) &
  UI_SPINNER_PID=$!
  disown "$UI_SPINNER_PID" 2>/dev/null || true
}

ui_spinner_stop() {
  [[ -z "$UI_SPINNER_PID" ]] && return 0
  kill "$UI_SPINNER_PID" 2>/dev/null || true
  UI_SPINNER_PID=""
  # Clear the spinner line
  printf "\r\033[2K" >&2
  local status="${1:-ok}"
  if [[ "$status" == "ok" ]]; then
    _ui_print "${C_SUCCESS}✓${C_RESET}  ${_UI_SPINNER_MSG}"
  else
    _ui_print "${C_ERROR}✗${C_RESET}  ${_UI_SPINNER_MSG}"
  fi
}

# ─── Wait progress bar ────────────────────────────────────────────────────────
#
# Replaces the plain "⏳ Copilot reviewing... (Xs / Ys)" log line with an
# inline progress bar. Called once per poll tick by wait_for_review().

ui_wait_tick() {
  local elapsed="$1"
  local max="$2"
  local msg="${3:-Copilot reviewing}"

  if [[ "$_UI_TTY" != true ]]; then
    log "   ⏳ ${msg}... (${elapsed}s / ${max}s)"
    return
  fi

  local bar_width=30
  local filled=$(( elapsed * bar_width / max ))
  [[ $filled -gt $bar_width ]] && filled=$bar_width
  local empty=$(( bar_width - filled ))

  local bar=""
  [[ $filled -gt 0 ]] && bar+=$(printf '█%.0s' $(seq 1 $filled))
  [[ $empty -gt 0 ]]  && bar+=$(printf '░%.0s' $(seq 1 $empty))

  local pct=$(( elapsed * 100 / max ))
  printf "\r${C_CYAN}⏳${C_RESET}  ${msg}  ${C_MUTED}[${C_CYAN}${bar}${C_MUTED}]${C_RESET}  ${C_DIM}${elapsed}s / ${max}s  (${pct}%%)${C_RESET}   " >&2
  # Also append to log file without ANSI
  [[ -n "${LOGFILE:-}" ]] && echo "[$(ui_ts_full)]    ⏳ ${msg}... (${elapsed}s / ${max}s)" >> "$LOGFILE"
}

ui_wait_clear() {
  [[ "$_UI_TTY" == true ]] && printf "\r\033[2K" >&2
}

# ─── Badge helpers ────────────────────────────────────────────────────────────

ui_badge() {
  local label="$1" color="${2:-$C_BG_BLUE$C_WHITE}"
  _ui_print_n " ${color}${C_BOLD} ${label} ${C_RESET} "
}

ui_success_banner() {
  local pr="$1" repo="$2"
  echo ""
  _ui_print "${C_BG_GREEN}${C_BLACK}${C_BOLD}  ✅  PR #${pr} — ready to merge  ${C_RESET}"
  _ui_print "${C_MUTED}  ${repo}${C_RESET}"
  echo ""
}

# ─── Interactive post-success menu ────────────────────────────────────────────
#
# ui_merge_menu <pr_number> <repo> <cwd>
#
# Presents arrow-key selectable options when the terminal is interactive,
# falls back to a numbered prompt otherwise (e.g. piped / CI).
#
# Options:
#   1) Merge PR, delete remote branch, delete local branch, switch to main
#   2) Merge PR only
#   3) Open PR in browser
#   4) Do nothing

ui_merge_menu() {
  local pr="$1"
  local repo="$2"
  local cwd="$3"

  # Detect current local branch
  local local_branch
  local_branch=$(git -C "$cwd" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")

  # Detect default branch (main / master)
  local default_branch
  default_branch=$(git -C "$cwd" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null \
    | sed 's|refs/remotes/origin/||' || echo "main")

  ui_success_banner "$pr" "$repo"

  local options=(
    "Merge PR + delete remote branch + delete local branch → ${default_branch}"
    "Merge PR only"
    "Open PR in browser"
    "Do nothing (exit)"
  )

  local choice
  if [[ "$_UI_TTY" == true ]]; then
    choice=$(_ui_arrow_menu "${options[@]}")
  else
    choice=$(_ui_numbered_menu "${options[@]}")
  fi

  case "$choice" in
    0) _ui_do_full_cleanup "$pr" "$repo" "$cwd" "$local_branch" "$default_branch" ;;
    1) _ui_do_merge_only   "$pr" "$repo" ;;
    2) _ui_do_open_browser "$pr" "$repo" ;;
    3) _ui_print "${C_MUTED}Exiting without action.${C_RESET}" ;;
  esac
}

# ─── Arrow-key menu (interactive TTY) ────────────────────────────────────────

_ui_arrow_menu() {
  local options=("$@")
  local n=${#options[@]}
  local selected=0

  # Hide cursor
  printf "\033[?25l" >&2
  trap 'printf "\033[?25h" >&2' RETURN

  _ui_draw_menu() {
    # Move cursor up to redraw
    for (( i=0; i<n; i++ )); do
      if [[ $i -eq $selected ]]; then
        printf "  ${C_CYAN}${C_BOLD}▶  ${options[$i]}${C_RESET}\n" >&2
      else
        printf "  ${C_MUTED}   ${options[$i]}${C_RESET}\n" >&2
      fi
    done
  }

  # Print hint
  _ui_print "${C_DIM}  Use ↑↓ arrows and Enter to select${C_RESET}" >&2
  echo "" >&2

  _ui_draw_menu

  while true; do
    # Read one key sequence
    local key
    IFS= read -r -s -n1 key <&2 2>/dev/null || key=""

    if [[ "$key" == $'\x1b' ]]; then
      local seq
      IFS= read -r -s -n2 seq <&2 2>/dev/null || seq=""
      key="${key}${seq}"
    fi

    case "$key" in
      $'\x1b[A'|$'\x1bOA'|'k')  # up
        selected=$(( (selected - 1 + n) % n ))
        ;;
      $'\x1b[B'|$'\x1bOB'|'j')  # down
        selected=$(( (selected + 1) % n ))
        ;;
      $'\n'|$'\r'|'')            # enter
        # Move up n lines and clear them
        printf "\033[%dA\033[J" $(( n + 2 )) >&2
        _ui_print "  ${C_CYAN}▶${C_RESET}  ${C_BOLD}${options[$selected]}${C_RESET}" >&2
        echo $selected
        return
        ;;
      'q'|$'\x03')               # q or Ctrl-C
        printf "\033[?25h" >&2
        printf "\033[%dA\033[J" $(( n + 2 )) >&2
        echo 3  # "Do nothing"
        return
        ;;
    esac

    # Redraw: move cursor up n lines, then redraw
    printf "\033[%dA" $n >&2
    _ui_draw_menu
  done
}

# ─── Numbered fallback menu (non-interactive / piped) ─────────────────────────

_ui_numbered_menu() {
  local options=("$@")
  local n=${#options[@]}

  echo "" >&2
  _ui_print "${C_BOLD}What would you like to do?${C_RESET}" >&2
  for (( i=0; i<n; i++ )); do
    _ui_print "  ${C_CYAN}$((i+1))${C_RESET}  ${options[$i]}" >&2
  done
  echo "" >&2

  local input
  while true; do
    printf "${C_DIM}Enter choice [1-${n}]:${C_RESET} " >&2
    read -r input </dev/tty 2>/dev/null || input="4"
    if [[ "$input" =~ ^[0-9]+$ && "$input" -ge 1 && "$input" -le $n ]]; then
      echo $(( input - 1 ))
      return
    fi
    _ui_print "${C_WARN}Please enter a number between 1 and ${n}.${C_RESET}" >&2
  done
}

# ─── Actions ──────────────────────────────────────────────────────────────────

_ui_do_merge_only() {
  local pr="$1" repo="$2"
  log "🔀 Merging PR #${pr}..."
  if gh pr merge "$pr" --repo "$repo" --merge --delete-branch=false; then
    log "✅ PR #${pr} merged."
  else
    log "❌ Merge failed — check gh output above."
  fi
}

_ui_do_full_cleanup() {
  local pr="$1" repo="$2" cwd="$3" local_branch="$4" default_branch="$5"

  log "🔀 Merging PR #${pr} (with remote branch deletion)..."
  if ! gh pr merge "$pr" --repo "$repo" --merge; then
    log "❌ Merge failed — aborting cleanup."
    return 1
  fi
  log "✅ PR #${pr} merged."

  # Delete local branch and switch to default
  if [[ -n "$local_branch" && "$local_branch" != "$default_branch" ]]; then
    log "🔄 Switching to ${default_branch}..."
    if git -C "$cwd" checkout "$default_branch" 2>/dev/null; then
      log "   Pulling latest ${default_branch}..."
      git -C "$cwd" pull --ff-only origin "$default_branch" 2>/dev/null || true
      log "🗑  Deleting local branch: ${local_branch}"
      git -C "$cwd" branch -d "$local_branch" 2>/dev/null \
        || git -C "$cwd" branch -D "$local_branch" 2>/dev/null \
        || log "⚠️  Could not delete local branch ${local_branch} (may already be gone)"
    else
      log "⚠️  Could not switch to ${default_branch} — skipping local branch deletion"
    fi
  else
    log "${C_MUTED}   (already on ${default_branch} or no local branch detected — skipping)${C_RESET}"
  fi

  echo ""
  _ui_print "${C_SUCCESS}${C_BOLD}  All done! On ${default_branch}, PR merged, branches cleaned up.${C_RESET}"
  echo ""
}

_ui_do_open_browser() {
  local pr="$1" repo="$2"
  gh pr view "$pr" --repo "$repo" --web 2>/dev/null \
    || _ui_print "${C_WARN}Could not open browser — try: gh pr view ${pr} --repo ${repo} --web${C_RESET}"
}
