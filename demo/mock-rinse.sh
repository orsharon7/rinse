#!/usr/bin/env bash
# demo/mock-rinse.sh — mock RINSE binary for VHS demo recording.
#
# Usage (from vhs tape):
#   PATH="$(dirname "$0"):$PATH" rinse run --pr 42
#
# Outputs ANSI-styled text that mimics the real RINSE cycle-monitor TUI.
# No GitHub auth, no Go binary required.

set -euo pipefail

# ── ANSI helpers ──────────────────────────────────────────────────────────────
RESET=$'\e[0m'
BOLD=$'\e[1m'
DIM=$'\e[2m'

# Catppuccin Macchiato palette (closest 256-color approximations)
MAUVE=$'\e[38;2;139;92;246m'    # #8B5CF6
GREEN=$'\e[38;2;16;185;129m'    # #10B981
AMBER=$'\e[38;2;245;158;11m'    # #F59E0B
TEAL=$'\e[38;2;139;213;202m'    # #8BD5CA
TEXT=$'\e[38;2;202;211;245m'    # #CAD3F5
MUTED=$'\e[38;2;165;173;203m'   # #A5ADCB
DIM_TEXT=$'\e[38;2;110;115;141m' # #6E738D
LAVENDER=$'\e[38;2;183;189;248m' # #B7BDF8

# ── Screen helpers ────────────────────────────────────────────────────────────
clear_screen() { printf '\e[2J\e[H'; }
move_to()      { printf '\e[%d;%dH' "$1" "$2"; }

# ── Header (always visible in monitor) ───────────────────────────────────────
print_header() {
  local phase="$1" phase_color="$2"
  printf '%s%s%s RINSE%s  %scycle monitor%s' \
    "$RESET" "$MAUVE" "$BOLD" "$RESET" "$MUTED" "$RESET"
  printf '   %sPR #42 · add-oauth-flow%s\n' "$DIM_TEXT" "$RESET"
  printf '%s────────────────────────────────────────────────────────────%s\n' "$DIM_TEXT" "$RESET"
  printf '%sphase%s  %s%s%s%s\n' \
    "$MUTED" "$RESET" "$phase_color" "$BOLD" "$phase" "$RESET"
}

# ── Spinner frames ────────────────────────────────────────────────────────────
spinner_frames=('⠋' '⠙' '⠹' '⠸' '⠼' '⠴' '⠦' '⠧' '⠇' '⠏')
spin_for() {
  # spin_for <seconds> <message> [color]
  local secs="$1" msg="$2" color="${3:-$MAUVE}"
  local end=$(( $(date +%s) + secs ))
  local i=0
  while [[ $(date +%s) -lt $end ]]; do
    printf '\r  %s%s%s  %s' "$color" "${spinner_frames[$((i % 10))]}" "$RESET" "$msg"
    sleep 0.1
    (( i++ )) || true
  done
  printf '\r'
}

# ── Timeline renderer ─────────────────────────────────────────────────────────
print_timeline() {
  # args: each arg is a dot type: "running" "fixed:N" "approved"
  printf '%shistory%s  ' "$MUTED" "$RESET"
  local first=1
  for dot in "$@"; do
    [[ $first -eq 0 ]] && printf '%s ›%s ' "$MUTED" "$RESET"
    first=0
    case "$dot" in
      running)   printf '%s◌%s'      "$MAUVE" "$RESET" ;;
      fixed:*)   printf '%s●%s%s'    "$GREEN" "${dot#fixed:}" "$RESET" ;;
      approved)  printf '%s✓%s'      "$GREEN" "$RESET" ;;
    esac
  done
  printf '\n'
}

# =============================================================================
# MAIN SEQUENCE
# =============================================================================

# 1. Startup message ──────────────────────────────────────────────────────────
clear_screen
printf '%s%sRINSE%s  connecting to GitHub…\n' "$MAUVE" "$BOLD" "$RESET"
sleep 0.8
printf '%sPR #42 · add-oauth-flow · 3 Copilot comments pending%s\n' "$TEXT" "$RESET"
sleep 1.2

# 2. Cycle monitor — iteration 1, waiting for Copilot ─────────────────────────
clear_screen
print_header "waiting for Copilot review" "$TEAL"
printf '\n'
printf '%siter%s   1 / 3\n' "$MUTED" "$RESET"
printf '\n'
print_timeline "running"
printf '\n'
spin_for 2 "${TEAL}waiting for GitHub Copilot to review…${RESET}" "$TEAL"

# 3. Cycle monitor — iteration 1, Copilot left 3 comments ────────────────────
clear_screen
print_header "fixing comments" "$AMBER"
printf '\n'
printf '%siter%s   1 / 3\n' "$MUTED" "$RESET"
printf '\n'
printf '  %s✓%s  Copilot left 3 comments\n' "$AMBER" "$RESET"
printf '\n'
print_timeline "running"
printf '\n'
spin_for 1 "${AMBER}agent fixing 3 Copilot comments…${RESET}" "$AMBER"

# 4. Fix lines ─────────────────────────────────────────────────────────────────
clear_screen
print_header "fixing comments" "$AMBER"
printf '\n'
printf '%siter%s   1 / 3\n' "$MUTED" "$RESET"
printf '\n'
printf '  %s→%s  reading PR diff\n'                                             "$DIM_TEXT" "$RESET"
sleep 0.4
printf '  %s→%s  applying fix: add error handling in auth.go:47\n'             "$DIM_TEXT" "$RESET"
sleep 0.5
printf '  %s→%s  applying fix: extract magic constant to named const\n'        "$DIM_TEXT" "$RESET"
sleep 0.5
printf '  %s→%s  applying fix: add context param to db.Query\n'                "$DIM_TEXT" "$RESET"
sleep 0.5
printf '  %s→%s  pushing commit…\n'                                             "$DIM_TEXT" "$RESET"
sleep 0.7
printf '  %s✓%s  pushed %sabc1d2e%s  ·  re-requesting review\n'               "$GREEN" "$RESET" "$MUTED" "$RESET"
sleep 1.2

# 5. Cycle monitor — iteration 2, waiting ──────────────────────────────────────
clear_screen
print_header "waiting for Copilot review" "$TEAL"
printf '\n'
printf '%siter%s   2 / 3\n' "$MUTED" "$RESET"
printf '\n'
printf '  %s✓%s  3 comments fixed in iter 1\n' "$GREEN" "$RESET"
printf '\n'
print_timeline "fixed:3" "running"
printf '\n'
spin_for 2 "${TEAL}waiting for GitHub Copilot to re-review…${RESET}" "$TEAL"

# 6. Approved ──────────────────────────────────────────────────────────────────
clear_screen
printf '\n'
printf '%s%s  ✓  Approved%s\n' "$GREEN" "$BOLD" "$RESET"
printf '%sGitHub Copilot approved after 2 iterations%s\n\n' "$MUTED" "$RESET"
printf '%shistory%s  %s●3%s %s›%s %s✓%s\n' \
  "$MUTED" "$RESET" "$GREEN" "$RESET" "$MUTED" "$RESET" "$GREEN" "$RESET"
printf '\n'

# Post-cycle menu box
printf '%s╭─ PR READY TO MERGE ──────────────────╮%s\n' "$MAUVE" "$RESET"
printf '%s│%s  %s→%s %sMerge PR%s                            %s│%s\n' \
  "$MAUVE" "$RESET" "$LAVENDER" "$RESET" "$TEXT" "$RESET" "$MAUVE" "$RESET"
printf '%s│%s    View PR on GitHub                 %s│%s\n' "$MAUVE" "$RESET" "$MAUVE" "$RESET"
printf '%s│%s    Exit                              %s│%s\n' "$MAUVE" "$RESET" "$MAUVE" "$RESET"
printf '%s╰──────────────────────────────────────╯%s\n' "$MAUVE" "$RESET"

sleep 3

# 7. Post-cycle summary ────────────────────────────────────────────────────────
clear_screen
printf '\n'
printf '%s%sRINSE approved ✓ — PR #42%s\n\n' "$GREEN" "$BOLD" "$RESET"
printf '  %s%-18s%s %s~4 min%s\n'            "$MUTED" "Time saved:"      "$RESET" "$TEXT" "$RESET"
printf '  %s%-18s%s %s4 across 2 rounds (3, 1)%s\n' \
                                              "$MUTED" "Comments fixed:"  "$RESET" "$TEXT" "$RESET"
printf '  %s%-18s%s %s2%s\n'                 "$MUTED" "Iterations:"       "$RESET" "$TEXT" "$RESET"
printf '  %s%-18s%s %serror handling, named constants, context%s\n' \
                                              "$MUTED" "Top patterns:"    "$RESET" "$TEXT" "$RESET"
printf '\n'
printf '  %sRun `rinse stats` to see your history.%s\n\n' "$DIM_TEXT" "$RESET"

sleep 2
