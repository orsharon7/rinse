#!/usr/bin/env bash
# pr-review-ui.sh — Shared terminal UI for pr-review runners
#
# Requires: gum (https://github.com/charmbracelet/gum)
#   brew install gum
#
# Source this file to get:
#   - log()           structured log lines via gum log (fallback: plain)
#   - ui_header()     bold section header
#   - ui_iter_header() per-iteration banner
#   - ui_wait_tick()  inline progress bar while waiting for Copilot
#   - ui_wait_clear() clear the progress line
#   - ui_merge_menu() post-success action menu via gum choose
#   - ui_reflect_log() print a reflect agent status line (inline, no cursor tricks)
#
# All public functions are prefixed ui_. Respects NO_COLOR and --no-interactive.

# ─── Preflight ────────────────────────────────────────────────────────────────

_UI_GUM=false
if command -v gum >/dev/null 2>&1; then
  _UI_GUM=true
fi

_UI_TTY=false
if [[ -t 1 && -z "${NO_COLOR:-}" && "${PR_REVIEW_NO_INTERACTIVE:-}" != "true" ]]; then
  _UI_TTY=true
fi

# ─── gum theme ────────────────────────────────────────────────────────────────
# Consistent accent color (Catppuccin-ish purple) used across all gum calls.
# Override by setting GUM_ACCENT before sourcing.
GUM_ACCENT="${GUM_ACCENT:-99}"        # 256-color: bright purple
GUM_MUTED="${GUM_MUTED:-240}"         # 256-color: dim grey
GUM_SUCCESS="${GUM_SUCCESS:-76}"      # 256-color: green
GUM_ERROR="${GUM_ERROR:-196}"         # 256-color: red
GUM_WARN="${GUM_WARN:-214}"           # 256-color: orange

# Minimal ANSI fallbacks (used only where gum isn't available / non-TTY)
if [[ "$_UI_TTY" == true ]]; then
  _R="\033[0m" _B="\033[1m" _D="\033[2m"
  _GREEN="\033[32m" _RED="\033[31m" _YELLOW="\033[33m"
  _CYAN="\033[36m"  _BLUE="\033[34m" _MAGENTA="\033[35m"
else
  _R="" _B="" _D="" _GREEN="" _RED="" _YELLOW="" _CYAN="" _BLUE="" _MAGENTA=""
fi

_ui_print()   { printf "%b\n" "$*"; }
ui_ts()       { date '+%H:%M:%S'; }
ui_ts_full()  { date '+%Y-%m-%d %H:%M:%S'; }

# ─── log() ───────────────────────────────────────────────────────────────────
#
# Routes to gum log when available, otherwise falls back to timestamped plain text.
# gum log level is inferred from the leading emoji in the message.

log() {
  local msg="$*"
  local ts
  ts=$(ui_ts_full)

  # Always write plain text to logfile
  [[ -n "${LOGFILE:-}" ]] && echo "[$ts] $msg" >> "$LOGFILE"

  if [[ "$_UI_GUM" == true && "$_UI_TTY" == true ]]; then
    # Map leading emoji → gum log level
    local level="info"
    case "$msg" in
      "✅"*|"🎉"*)   level="info" ;;
      "❌"*)          level="error" ;;
      "⚠️"*)         level="warn" ;;
      "🚀"*)          level="info" ;;
      "   "*|"→"*)   level="debug" ;;
    esac
    gum log \
      --time "15:04:05" \
      --level "$level" \
      --prefix "" \
      -- "$msg"
  else
    printf "%b\n" "${_D}[${ts}]${_R} ${msg}"
  fi
}

# ─── Section headers ──────────────────────────────────────────────────────────

ui_header() {
  local title="$1"
  echo ""
  if [[ "$_UI_GUM" == true && "$_UI_TTY" == true ]]; then
    local w
    w=$(tput cols 2>/dev/null || echo 80)
    gum style \
      --bold \
      --foreground "$GUM_ACCENT" \
      --border-foreground "$GUM_ACCENT" \
      --border normal \
      --padding "0 1" \
      --width $(( w - 2 )) \
      "$title"
  else
    local w=70
    local line
    line=$(printf '━%.0s' $(seq 1 $w))
    _ui_print "${_B}${_BLUE}${line}${_R}"
    _ui_print "${_B}${_BLUE}  ${title}${_R}"
    _ui_print "${_B}${_BLUE}${line}${_R}"
  fi
  echo ""
}

ui_divider() {
  if [[ "$_UI_GUM" == true && "$_UI_TTY" == true ]]; then
    local w
    w=$(tput cols 2>/dev/null || echo 80)
    gum style --foreground "$GUM_MUTED" "$(printf '─%.0s' $(seq 1 $w))"
  else
    _ui_print "${_D}$(printf '─%.0s' $(seq 1 70))${_R}"
  fi
}

ui_iter_header() {
  local iter="$1"
  local ts
  ts=$(ui_ts)
  echo ""
  if [[ "$_UI_GUM" == true && "$_UI_TTY" == true ]]; then
    local w
    w=$(tput cols 2>/dev/null || echo 80)
    local label="  Iteration ${iter}    ${ts}  "
    local pad=$(( w - ${#label} - 2 ))
    [[ $pad -lt 0 ]] && pad=0
    local line
    line=$(printf '━%.0s' $(seq 1 $pad))
    gum style --bold --foreground "$GUM_ACCENT" "━━━${label}${line}"
  else
    local pad
    pad=$(printf '━%.0s' $(seq 1 55))
    _ui_print "${_B}${_BLUE}━━━ Iteration ${iter}  ${_D}${ts}${_R}${_B}${_BLUE}  ${pad}${_R}"
  fi
}

# ─── Wait progress bar ────────────────────────────────────────────────────────
#
# Called once per poll tick. Renders an inline ░█ bar (no gum — needs \r rewrite).

ui_wait_tick() {
  local elapsed="$1"
  local max="$2"
  local msg="${3:-Copilot reviewing}"

  # Clamp max to avoid divide-by-zero; guard against non-numeric values
  if ! [[ "$max" =~ ^[0-9]+$ ]] || [[ "$max" -lt 1 ]]; then
    max=1
  fi

  if [[ "$_UI_TTY" != true ]]; then
    log "   ⏳ ${msg}... (${elapsed}s / ${max}s)"
    return
  fi

  local bar_width=30
  local filled=$(( elapsed * bar_width / max ))
  [[ $filled -gt $bar_width ]] && filled=$bar_width
  local empty=$(( bar_width - filled ))
  [[ $empty -lt 0 ]] && empty=0

  local bar=""
  [[ $filled -gt 0 ]] && bar+=$(printf '█%.0s' $(seq 1 $filled))
  [[ $empty  -gt 0 ]] && bar+=$(printf '░%.0s' $(seq 1 $empty))

  local pct=$(( elapsed * 100 / max ))

  if [[ "$_UI_GUM" == true ]]; then
    printf "\r${_CYAN}⏳${_R}  %s  ${_D}[${_CYAN}%s${_D}]${_R}  ${_D}%ds / %ds (%d%%)${_R}   " \
      "$msg" "$bar" "$elapsed" "$max" "$pct" >&2
  else
    printf "\r⏳  %s  [%s]  %ds / %ds (%d%%)   " "$msg" "$bar" "$elapsed" "$max" "$pct" >&2
  fi

  [[ -n "${LOGFILE:-}" ]] && echo "[$(ui_ts_full)]    ⏳ ${msg}... (${elapsed}s / ${max}s)" >> "$LOGFILE"
}

ui_wait_clear() {
  [[ "$_UI_TTY" == true ]] && printf "\r\033[2K" >&2
}

# ─── Success banner ───────────────────────────────────────────────────────────

ui_success_banner() {
  local pr="$1" repo="$2"
  echo ""
  if [[ "$_UI_GUM" == true && "$_UI_TTY" == true ]]; then
    gum style \
      --bold \
      --foreground "0" \
      --background "$GUM_SUCCESS" \
      --padding "0 2" \
      "✅  PR #${pr} — approved"
    gum style --foreground "$GUM_MUTED" "  ${repo}"
  else
    _ui_print "${_GREEN}${_B}  ✅  PR #${pr} — approved  ${_R}"
    _ui_print "${_D}  ${repo}${_R}"
  fi
  echo ""
}

# ─── Reflect agent inline status ──────────────────────────────────────────────
#
# Called from runners to print a styled one-liner about reflection activity.
# No cursor tricks — just a clearly prefixed log line in the normal stream.

ui_reflect_log() {
  local msg="$1"
  local ok="${2:-true}"  # "true" | "false" | "skip"

  if [[ "$_UI_GUM" == true && "$_UI_TTY" == true ]]; then
    local prefix_color="$GUM_ACCENT"
    local icon="◎"
    case "$ok" in
      false) prefix_color="$GUM_ERROR"; icon="✗" ;;
      skip)  prefix_color="$GUM_MUTED"; icon="○" ;;
    esac
    local prefix
    prefix=$(gum style --foreground "$prefix_color" "${icon} reflect │")
    printf "%s %s\n" "$prefix" "$msg"
  else
    local icon="◎"
    case "$ok" in
      false) icon="✗" ;;
      skip)  icon="○" ;;
    esac
    printf "%b\n" "${_D}${icon} reflect │ ${msg}${_R}"
  fi

  [[ -n "${LOGFILE:-}" ]] && echo "[$(ui_ts_full)] [reflect] $msg" >> "$LOGFILE"
}

# ─── Post-success merge menu ──────────────────────────────────────────────────
#
# ui_merge_menu <pr_number> <repo> <cwd>

ui_merge_menu() {
  local pr="$1" repo="$2" cwd="$3"

  # When running under the TUI (non-interactive mode), skip the bash menu entirely.
  # The TUI presents its own Bubble Tea post-cycle menu after the runner exits.
  if [[ "${PR_REVIEW_NO_INTERACTIVE:-}" == "true" ]]; then
    return 0
  fi

  local local_branch default_branch
  local_branch=$(git -C "$cwd" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
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
  if [[ "$_UI_GUM" == true && "$_UI_TTY" == true ]]; then
    # gum choose returns the selected string — map back to index
    local selected
    selected=$(printf '%s\n' "${options[@]}" | gum choose \
      --cursor="▶ " \
      --cursor-prefix="  " \
      --selected-prefix="▶ " \
      --unselected-prefix="  " \
      --header="" \
      --height=$(( ${#options[@]} + 2 )))
    # Map string back to index
    local i
    for i in "${!options[@]}"; do
      [[ "${options[$i]}" == "$selected" ]] && choice=$i && break
    done
    choice="${choice:-3}"
  elif [[ "$_UI_TTY" == true ]]; then
    choice=$(_ui_arrow_menu "${options[@]}")
  else
    choice=$(_ui_numbered_menu "${options[@]}")
  fi

  case "$choice" in
    0) _ui_do_full_cleanup "$pr" "$repo" "$cwd" "$local_branch" "$default_branch" ;;
    1) _ui_do_merge_only   "$pr" "$repo" ;;
    2) _ui_do_open_browser "$pr" "$repo" ;;
    3) _ui_print "${_D}Exiting without action.${_R}" ;;
  esac
}

# ─── Stall menu (used by wait_for_review in runners) ─────────────────────────
#
# Returns 0-based index of selected option on stdout.

_ui_arrow_menu() {
  local options=("$@")
  local n=${#options[@]}

  if [[ "$_UI_GUM" == true ]]; then
    local selected
    selected=$(printf '%s\n' "${options[@]}" | gum choose \
      --cursor="▶ " \
      --cursor-prefix="  " \
      --selected-prefix="▶ " \
      --unselected-prefix="  " \
      --header="" \
      --height=$(( n + 2 )))
    local i
    for i in "${!options[@]}"; do
      [[ "${options[$i]}" == "$selected" ]] && echo "$i" && return
    done
    echo $(( n - 1 ))  # last option as fallback
    return
  fi

  # Fallback: raw ANSI arrow-key menu
  local selected=0
  printf "\033[?25l" >&2
  trap 'printf "\033[?25h" >&2' RETURN

  _draw() {
    for (( i=0; i<n; i++ )); do
      if [[ $i -eq $selected ]]; then
        printf "  ${_CYAN}${_B}▶  ${options[$i]}${_R}\n" >&2
      else
        printf "  ${_D}   ${options[$i]}${_R}\n" >&2
      fi
    done
  }

  printf "%b" "  ${_D}↑↓ to move, Enter to confirm${_R}\n\n" >&2
  _draw

  while true; do
    local key seq
    IFS= read -r -s -n1 key </dev/tty 2>/dev/null || key=""
    if [[ "$key" == $'\x1b' ]]; then
      IFS= read -r -s -n2 seq </dev/tty 2>/dev/null || seq=""
      key="${key}${seq}"
    fi
    case "$key" in
      $'\x1b[A'|$'\x1bOA'|'k') selected=$(( (selected - 1 + n) % n )) ;;
      $'\x1b[B'|$'\x1bOB'|'j') selected=$(( (selected + 1) % n )) ;;
      $'\n'|$'\r'|'')
        printf "\033[?25h" >&2
        printf "\033[%dA\033[J" $(( n + 2 )) >&2
        printf "  ${_CYAN}▶${_R}  ${_B}${options[$selected]}${_R}\n" >&2
        echo $selected; return ;;
      'q'|$'\x03')
        printf "\033[?25h" >&2
        printf "\033[%dA\033[J" $(( n + 2 )) >&2
        echo $(( n - 1 )); return ;;
    esac
    printf "\033[%dA" $n >&2
    _draw
  done
}

_ui_numbered_menu() {
  local options=("$@")
  local n=${#options[@]}
  echo "" >&2
  _ui_print "${_B}What would you like to do?${_R}" >&2
  for (( i=0; i<n; i++ )); do
    _ui_print "  ${_CYAN}$((i+1))${_R}  ${options[$i]}" >&2
  done
  echo "" >&2
  local input
  while true; do
    printf "${_D}Enter choice [1-${n}]:${_R} " >&2
    read -r input </dev/tty 2>/dev/null || input="$n"
    if [[ "$input" =~ ^[0-9]+$ && "$input" -ge 1 && "$input" -le $n ]]; then
      echo $(( input - 1 )); return
    fi
    _ui_print "${_YELLOW}Please enter a number between 1 and ${n}.${_R}" >&2
  done
}

# ─── Actions ──────────────────────────────────────────────────────────────────

_ui_do_merge_only() {
  local pr="$1" repo="$2"
  log "🔀 Merging PR #${pr}..."
  if gh pr merge "$pr" --repo "$repo" --merge; then
    log "✅ PR #${pr} merged."
  else
    log "❌ Merge failed — check gh output above."
  fi
}

_ui_do_full_cleanup() {
  local pr="$1" repo="$2" cwd="$3" local_branch="$4" default_branch="$5"
  log "🔀 Merging PR #${pr} (with remote branch deletion)..."
  if ! gh pr merge "$pr" --repo "$repo" --merge --delete-branch; then
    log "❌ Merge failed — aborting cleanup."
    return 1
  fi
  log "✅ PR #${pr} merged."
  if [[ -n "$local_branch" && "$local_branch" != "$default_branch" ]]; then
    log "🔄 Switching to ${default_branch}..."
    if git -C "$cwd" checkout "$default_branch" 2>/dev/null; then
      log "   Pulling latest ${default_branch}..."
      git -C "$cwd" pull --ff-only origin "$default_branch" 2>/dev/null || true
      log "🗑  Deleting local branch: ${local_branch}"
      git -C "$cwd" branch -d "$local_branch" 2>/dev/null \
        || git -C "$cwd" branch -D "$local_branch" 2>/dev/null \
        || log "⚠️  Could not delete local branch ${local_branch}"
    else
      log "⚠️  Could not switch to ${default_branch} — skipping local branch deletion"
    fi
  fi
  echo ""
  if [[ "$_UI_GUM" == true && "$_UI_TTY" == true ]]; then
    gum style --bold --foreground "$GUM_SUCCESS" \
      "  All done!  On ${default_branch}, PR merged, branches cleaned up."
  else
    _ui_print "${_GREEN}${_B}  All done! On ${default_branch}, PR merged, branches cleaned up.${_R}"
  fi
  echo ""
}

_ui_do_open_browser() {
  local pr="$1" repo="$2"
  gh pr view "$pr" --repo "$repo" --web 2>/dev/null \
    || _ui_print "${_YELLOW}Could not open browser — try: gh pr view ${pr} --repo ${repo} --web${_R}"
}

# ─── no-op stubs (removed broken CSR implementation) ─────────────────────────
# Kept so existing call sites compile without error; runners now call
# ui_reflect_log() directly for inline status instead.
ui_reflect_start() { :; }
ui_reflect_done()  { :; }
