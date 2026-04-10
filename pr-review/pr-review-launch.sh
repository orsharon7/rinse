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

# ─── Preflight ────────────────────────────────────────────────────────────────

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

# ─── Palette (Catppuccin Macchiato) ───────────────────────────────────────────
C_MAUVE="183"
C_LAVENDER="147"
C_TEAL="116"
C_RED="210"
C_YELLOW="222"
C_SURFACE="238"
C_OVERLAY="245"
C_TEXT="255"

export GUM_CHOOSE_CURSOR_FOREGROUND="$C_MAUVE"
export GUM_CHOOSE_SELECTED_FOREGROUND="$C_MAUVE"
export GUM_CHOOSE_HEADER_FOREGROUND="$C_OVERLAY"
export GUM_CHOOSE_CURSOR="❯ "
export GUM_CHOOSE_UNSELECTED_PREFIX="  "
export GUM_INPUT_CURSOR_FOREGROUND="$C_MAUVE"
export GUM_INPUT_PROMPT_FOREGROUND="$C_MAUVE"
export GUM_CONFIRM_PROMPT_FOREGROUND="$C_MAUVE"

# ─── Terminal helpers ──────────────────────────────────────────────────────────

_w()    { tput cols  2>/dev/null || echo 80; }
_h()    { tput lines 2>/dev/null || echo 24; }

# Hide/show cursor
_cur_hide() { printf '\033[?25l'; }
_cur_show() { printf '\033[?25h'; }

# Move cursor to row R (1-based), col 1
_goto()  { printf '\033[%d;1H' "$1"; }

# Erase from current cursor position to end of screen
_erase_down() { printf '\033[J'; }

# Enter / leave alternate screen (no scrollback pollution)
_altscreen_on()  { tput smcup 2>/dev/null || true; }
_altscreen_off() { tput rmcup 2>/dev/null || true; }

# Cleanup on exit
_cleanup() {
  _cur_show
  _altscreen_off
}
trap _cleanup EXIT INT TERM

# ─── Drawing helpers ──────────────────────────────────────────────────────────

# A full-width horizontal rule using box-drawing chars
_rule() {
  local w; w=$(_w)
  printf '\033[38;5;%smm%s\033[0m\n' "$C_SURFACE" "$(printf '─%.0s' $(seq 1 "$w"))"
}

# The static banner — rendered once into variable BANNER_LINES
# We store it as a string and print it at row 1 each repaint.
_make_banner() {
  local w; w=$(_w)
  # inner = full width minus 4 (border 2 each side)
  local inner=$(( w - 4 ))
  [[ $inner -lt 30 ]] && inner=30

  # Build banner text
  local title
  title=$(gum style \
    --border double \
    --border-foreground "$C_MAUVE" \
    --align center \
    --width "$inner" \
    --padding "1 4" \
    --bold \
    --foreground "$C_MAUVE" \
    "pr-review" \
    "" \
    "$(gum style --foreground "$C_OVERLAY" --italic "Copilot PR Review Automation")")
  printf '%s\n' "$title"
}

# Print the summary box of already-answered fields.
# Args: key val key val ...
_summary() {
  [[ $# -eq 0 ]] && return
  local w; w=$(_w)
  local inner=$(( w - 4 ))
  [[ $inner -lt 30 ]] && inner=30

  local rows=""
  while [[ $# -ge 2 ]]; do
    local k="$1" v="$2"; shift 2
    rows+="$(printf '%s  %s\n' \
      "$(gum style --foreground "$C_OVERLAY"  "$(printf '%-16s' "$k")")" \
      "$(gum style --foreground "$C_LAVENDER" --bold "$v")")"$'\n'
  done

  gum style \
    --border normal \
    --border-foreground "$C_SURFACE" \
    --width "$inner" \
    --padding "0 2" \
    "${rows%$'\n'}"
}

# Step label line
_step_label() {
  local n="$1" total="$2" label="$3"
  gum style \
    --foreground "$C_MAUVE" --bold \
    "  [$n/$total]  $(gum style --foreground "$C_OVERLAY" "$label")"
}

# ─── Full repaint ─────────────────────────────────────────────────────────────
# Repaints the screen: banner (static) + summary (dynamic) + step label.
# After this returns, cursor is positioned right below the step label,
# ready for the gum input/choose prompt.
# Args: step total label [summary key val key val ...]
_repaint() {
  local step="$1" total="$2" label="$3"; shift 3
  local sum_args=("$@")

  _cur_hide
  _goto 1
  _erase_down

  echo ""
  _make_banner
  echo ""

  if [[ ${#sum_args[@]} -gt 0 ]]; then
    _summary "${sum_args[@]}"
    echo ""
  fi

  _step_label "$step" "$total" "$label"
  echo ""

  _cur_show
}

# ─── Data helpers ─────────────────────────────────────────────────────────────

_detect_repo()           { gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || echo ""; }
_detect_cwd()            { pwd; }
_detect_default_branch() { gh repo view "$1" --json defaultBranchRef -q '.defaultBranchRef.name' 2>/dev/null || echo "main"; }

_detect_open_prs() {
  gh pr list --repo "$1" --json number,title,headRefName --limit 15 \
    --jq '.[] | "#\(.number)  \(.headRefName | .[0:28])  — \(.title | .[0:48])"' 2>/dev/null || true
}

# ─── Wizard ───────────────────────────────────────────────────────────────────

main() {
  local arg_pr="" arg_repo="" arg_cwd=""
  if [[ $# -ge 1 && "$1" =~ ^[0-9]+$ ]]; then arg_pr="$1"; shift; fi
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --repo) arg_repo="$2"; shift 2 ;;
      --cwd)  arg_cwd="$2";  shift 2 ;;
      *)      shift ;;
    esac
  done

  local repo="" pr="" cwd="" runner_label="" runner_script="" default_model=""
  local model="" reflect="false" reflect_branch="" wait_max="300" dry_run="false"

  local detected_repo="${arg_repo:-$(_detect_repo)}"
  local detected_cwd="${arg_cwd:-$(_detect_cwd)}"
  local detected_default_branch="main"

  local TOTAL=7
  local step=1

  _altscreen_on

  while true; do

    # Build summary args from fields filled so far
    local sum=()
    [[ -n "$repo"         ]] && sum+=("repository"  "$repo")
    [[ -n "$pr"           ]] && sum+=("PR"           "#${pr}")
    [[ -n "$cwd"          ]] && sum+=("path"         "$cwd")
    [[ -n "$runner_label" ]] && sum+=("runner"       "$runner_label")
    [[ -n "$model"        ]] && sum+=("model"        "$model")
    [[ "$reflect" == "true"  ]] && sum+=("reflection" "on → ${reflect_branch}")
    [[ "$reflect" == "false" && $step -gt 6 ]] && sum+=("reflection" "off")

    case "$step" in

      # ── 1: Repository ───────────────────────────────────────────────────────
      1)
        _repaint 1 $TOTAL "repository" "${sum[@]}"
        gum style --foreground "$C_OVERLAY" "  GitHub repo in owner/repo format"
        echo ""
        local new_repo
        new_repo=$(gum input \
          --placeholder "owner/repo" \
          --value "${repo:-$detected_repo}" \
          --prompt "  ❯ " \
          --width 60) || { _altscreen_off; _cur_show; exit 0; }

        if [[ -z "$new_repo" ]]; then
          gum style --foreground "$C_RED" "  repository is required"; sleep 1; continue
        fi
        repo="$new_repo"
        detected_default_branch=$(_detect_default_branch "$repo")
        step=$(( step + 1 ))
        ;;

      # ── 2: PR picker ────────────────────────────────────────────────────────
      2)
        _repaint 2 $TOTAL "pull request" "${sum[@]}"
        gum style --foreground "$C_OVERLAY" "  Fetching open PRs…"

        local open_prs_raw
        open_prs_raw=$(_detect_open_prs "$repo")

        # Erase the "fetching" line by going back up one line
        printf '\033[1A\033[2K'

        if [[ -z "$open_prs_raw" ]]; then
          gum style --foreground "$C_OVERLAY" "  No open PRs found — enter number manually"
          echo ""
          local new_pr
          new_pr=$(gum input \
            --placeholder "42" \
            --value "${pr:-$arg_pr}" \
            --prompt "  ❯ " \
            --width 20) || { step=$(( step - 1 )); continue; }
          if [[ -z "$new_pr" || ! "$new_pr" =~ ^[0-9]+$ ]]; then
            gum style --foreground "$C_RED" "  must be a positive integer"; sleep 1; continue
          fi
          pr="$new_pr"
        else
          local -a pr_options=()
          [[ -n "$pr" ]] && pr_options+=("keep  #${pr}  (current)")
          while IFS= read -r line; do
            pr_options+=("$line")
          done <<< "$open_prs_raw"
          pr_options+=("✏  type a number manually")

          local chosen
          chosen=$(printf '%s\n' "${pr_options[@]}" | gum choose \
            --height 12 \
            --header "  ↑/↓ navigate · enter select · esc go back" \
            --header.foreground "$C_OVERLAY" \
            --cursor "❯ ") || { step=$(( step - 1 )); continue; }

          if [[ "$chosen" == "✏  type a number manually" ]]; then
            echo ""
            local new_pr
            new_pr=$(gum input \
              --placeholder "42" \
              --value "${pr:-$arg_pr}" \
              --prompt "  ❯ " \
              --width 20) || { step=$(( step - 1 )); continue; }
            if [[ -z "$new_pr" || ! "$new_pr" =~ ^[0-9]+$ ]]; then
              gum style --foreground "$C_RED" "  must be a positive integer"; sleep 1; continue
            fi
            pr="$new_pr"
          elif [[ "$chosen" == keep* ]]; then
            : # keep existing
          else
            pr=$(printf '%s' "$chosen" | grep -oE '^#[0-9]+' | tr -d '#')
          fi
        fi
        step=$(( step + 1 ))
        ;;

      # ── 3: Local path ───────────────────────────────────────────────────────
      3)
        _repaint 3 $TOTAL "local clone path" "${sum[@]}"
        gum style --foreground "$C_OVERLAY" "  Absolute path to your local checkout"
        echo ""
        local new_cwd
        new_cwd=$(gum input \
          --placeholder "/path/to/repo" \
          --value "${cwd:-$detected_cwd}" \
          --prompt "  ❯ " \
          --width $(( $(_w) - 6 ))) || { step=$(( step - 1 )); continue; }
        [[ -z "$new_cwd" ]] && new_cwd="${cwd:-$detected_cwd}"
        if [[ ! -d "$new_cwd" ]]; then
          echo ""
          gum style --foreground "$C_YELLOW" "  ⚠  directory not found — continue anyway?"
          if ! gum confirm "" --affirmative "Continue" --negative "Re-enter" --default=false; then
            continue
          fi
        fi
        cwd="$new_cwd"
        step=$(( step + 1 ))
        ;;

      # ── 4: Runner ───────────────────────────────────────────────────────────
      4)
        _repaint 4 $TOTAL "runner" "${sum[@]}"
        gum style --foreground "$C_OVERLAY" "  Which AI agent drives the review loop?"
        echo ""
        local runner_chosen
        runner_chosen=$(printf '%s\n' \
          "opencode   — GitHub Copilot · no API key needed  ✦" \
          "claude v2  — Claude Code · requires Anthropic key" \
          "claude v1  — legacy runner" \
          | gum choose \
            --height 6 \
            --cursor "❯ ") || { step=$(( step - 1 )); continue; }

        case "$runner_chosen" in
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
        step=$(( step + 1 ))
        ;;

      # ── 5: Model ────────────────────────────────────────────────────────────
      5)
        _repaint 5 $TOTAL "model" "${sum[@]}"
        gum style --foreground "$C_OVERLAY" \
          "  Leave blank for default: $(gum style --foreground "$C_LAVENDER" "${default_model:-n/a}")"
        echo ""
        local new_model
        new_model=$(gum input \
          --placeholder "${default_model}" \
          --value "${model}" \
          --prompt "  ❯ " \
          --width 60) || { step=$(( step - 1 )); continue; }
        model="$new_model"
        step=$(( step + 1 ))
        ;;

      # ── 6: Reflection ───────────────────────────────────────────────────────
      6)
        if [[ "$runner_label" == "claude v1" ]]; then
          reflect="false"; step=$(( step + 1 )); continue
        fi
        _repaint 6 $TOTAL "reflection agent" "${sum[@]}"
        gum style --foreground "$C_OVERLAY" \
          "  Extracts coding rules from Copilot comments → pushes to ${detected_default_branch}"
        echo ""
        if gum confirm "Enable reflection?" \
          --affirmative "Yes, enable" \
          --negative "No, skip" \
          --default=false; then
          reflect="true"
          echo ""
          gum style --foreground "$C_OVERLAY" "  Branch to push rules to:"
          echo ""
          local new_rb
          new_rb=$(gum input \
            --placeholder "$detected_default_branch" \
            --value "${reflect_branch:-$detected_default_branch}" \
            --prompt "  ❯ " \
            --width 40) || { reflect="false"; step=$(( step - 1 )); continue; }
          [[ -z "$new_rb" ]] && new_rb="$detected_default_branch"
          reflect_branch="$new_rb"
        else
          reflect="false"; reflect_branch=""
        fi
        step=$(( step + 1 ))
        ;;

      # ── 7: Confirm & launch ─────────────────────────────────────────────────
      7)
        _repaint 7 $TOTAL "review & launch" "${sum[@]}"

        # Full settings table
        local w; w=$(_w)
        local inner=$(( w - 4 ))
        [[ $inner -lt 30 ]] && inner=30

        local final_body=""
        local -a final_rows=(
          "PR"         "#${pr}"
          "repository" "$repo"
          "path"       "$cwd"
          "runner"     "$runner_label"
          "model"      "${model:-${default_model} (default)}"
          "reflection" "$( [[ "$reflect" == "true" ]] && echo "on → ${reflect_branch}" || echo "off" )"
          "wait max"   "${wait_max}s"
        )
        local i=0
        while [[ $i -lt ${#final_rows[@]} ]]; do
          local fk="${final_rows[$i]}" fv="${final_rows[$((i+1))]}"
          final_body+="$(printf '%s  %s\n' \
            "$(gum style --foreground "$C_OVERLAY"  "$(printf '%-14s' "$fk")")" \
            "$(gum style --foreground "$C_LAVENDER" --bold "$fv")")"$'\n'
          i=$(( i + 2 ))
        done

        gum style \
          --border double \
          --border-foreground "$C_MAUVE" \
          --width "$inner" \
          --padding "1 2" \
          "$(gum style --bold --foreground "$C_TEXT" "settings")" \
          "" \
          "${final_body%$'\n'}"

        echo ""

        local action
        action=$(printf '%s\n' \
          "🚀  Launch" \
          "✏   Edit  (go back one step)" \
          "✗   Abort" \
          | gum choose \
            --height 5 \
            --cursor "❯ ") || { _altscreen_off; _cur_show; exit 0; }

        case "$action" in
          *Launch*)  break ;;
          *Edit*)    step=$(( step - 1 )); continue ;;
          *Abort*)   _altscreen_off; _cur_show; exit 0 ;;
        esac
        ;;

      *)
        break
        ;;
    esac
  done

  # ── Build & exec ──────────────────────────────────────────────────────────
  local cmd=("$runner_script" "$pr" "--repo" "$repo" "--cwd" "$cwd" "--wait-max" "$wait_max")
  [[ -n "$model" ]] && cmd+=("--model" "$model")
  [[ "$reflect" == "true" ]] && cmd+=("--reflect" "--reflect-main-branch" "$reflect_branch")
  [[ "$dry_run" == "true" ]] && cmd+=("--dry-run")

  # Leave alternate screen before handing off to runner (it scrolls normally)
  _altscreen_off
  _cur_show

  local w; w=$(_w)
  local inner=$(( w - 4 ))
  [[ $inner -lt 30 ]] && inner=30

  echo ""
  gum style \
    --border double \
    --border-foreground "$C_TEAL" \
    --align center \
    --width "$inner" \
    --padding "1 4" \
    "$(gum style --bold --foreground "$C_TEAL" "launching pr-review")" \
    "" \
    "$(gum style --foreground "$C_LAVENDER" "PR #${pr}  ·  ${repo}  ·  ${runner_label}")"
  echo ""

  if [[ "$dry_run" == "true" ]]; then
    gum style --foreground "$C_YELLOW" "  dry run — command:"
    echo ""
    gum style --foreground "$C_OVERLAY" "  ${cmd[*]}"
    exit 0
  fi

  exec "${cmd[@]}"
}

main "$@"
