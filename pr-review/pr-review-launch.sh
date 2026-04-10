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
C_MAUVE="183"     # soft purple — primary accent
C_LAVENDER="147"  # light blue-purple — secondary
C_TEAL="116"      # teal — success / active
C_RED="210"       # soft red — error
C_YELLOW="222"    # yellow — warning
C_SURFACE="238"   # dark grey — borders / muted
C_OVERLAY="245"   # mid grey — subtitles / hints
C_TEXT="255"      # near-white — body text
C_SUBTEXT="249"   # light grey — secondary values

export GUM_CHOOSE_CURSOR_FOREGROUND="$C_MAUVE"
export GUM_CHOOSE_SELECTED_FOREGROUND="$C_MAUVE"
export GUM_CHOOSE_HEADER_FOREGROUND="$C_OVERLAY"
export GUM_CHOOSE_CURSOR="❯ "
export GUM_CHOOSE_UNSELECTED_PREFIX="  "
export GUM_INPUT_CURSOR_FOREGROUND="$C_MAUVE"
export GUM_INPUT_PROMPT_FOREGROUND="$C_SURFACE"
export GUM_CONFIRM_PROMPT_FOREGROUND="$C_MAUVE"

# ─── Helpers ──────────────────────────────────────────────────────────────────

_ac()     { gum style --foreground "$C_MAUVE"   "$@"; }
_lav()    { gum style --foreground "$C_LAVENDER" "$@"; }
_muted()  { gum style --foreground "$C_OVERLAY"  "$@"; }
_teal()   { gum style --foreground "$C_TEAL"     "$@"; }
_err()    { gum style --foreground "$C_RED"      "$@"; }
_bold()   { gum style --bold                     "$@"; }

_w() { tput cols 2>/dev/null || echo 80; }

# Print a horizontal rule the full terminal width
_rule() {
  local w; w=$(_w)
  printf '\033[2m%*s\033[0m\n' "$w" '' | tr ' ' '─'
}

# Render the banner — big title + subtitle
_banner() {
  local w; w=$(_w)
  local inner=$(( w - 4 ))
  [[ $inner -lt 20 ]] && inner=20

  gum style \
    --border double \
    --border-foreground "$C_MAUVE" \
    --align center \
    --width "$inner" \
    --padding "1 3" \
    --bold \
    --foreground "$C_MAUVE" \
    "pr-review" \
    "" \
    "$(gum style --foreground "$C_OVERLAY" --italic "Copilot PR Review Automation")"
}

# Render already-filled fields as a summary table above the current prompt.
# Args: parallel arrays via nameref (bash 4.3+) — pass filled_keys and filled_vals as
# space-separated strings (simpler and portable).
#   _summary "KEY1" "VAL1" "KEY2" "VAL2" ...
_summary() {
  [[ $# -eq 0 ]] && return
  local w; w=$(_w)
  local inner=$(( w - 4 ))
  [[ $inner -lt 20 ]] && inner=20

  local rows=""
  while [[ $# -ge 2 ]]; do
    local k="$1" v="$2"; shift 2
    rows+="$(printf '  %s  %s\n' \
      "$(gum style --foreground "$C_OVERLAY"  "$(printf '%-18s' "$k")")" \
      "$(gum style --foreground "$C_LAVENDER" --bold "$v")")"$'\n'
  done

  gum style \
    --border normal \
    --border-foreground "$C_SURFACE" \
    --width "$inner" \
    --padding "0 1" \
    "${rows%$'\n'}"
}

# Prompt header — shown above each gum input/choose
_step_header() {
  local n="$1" total="$2" label="$3"
  printf '\n'
  gum style \
    --foreground "$C_MAUVE" --bold \
    "  step ${n}/${total}  $(gum style --foreground "$C_OVERLAY" "·")  ${label}"
  printf '\n'
}

_detect_repo()           { gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || echo ""; }
_detect_cwd()            { pwd; }
_detect_default_branch() { gh repo view "$1" --json defaultBranchRef -q '.defaultBranchRef.name' 2>/dev/null || echo "main"; }

# Returns lines like:  "#42  branch-name  — PR title"
_detect_open_prs() {
  gh pr list --repo "$1" --json number,title,headRefName --limit 15 \
    --jq '.[] | "#\(.number)  \(.headRefName | .[0:30])  — \(.title | .[0:50])"' 2>/dev/null || true
}

# ─── Wizard state ─────────────────────────────────────────────────────────────

main() {
  # Parse pre-filled CLI args
  local arg_pr="" arg_repo="" arg_cwd=""
  if [[ $# -ge 1 && "$1" =~ ^[0-9]+$ ]]; then arg_pr="$1"; shift; fi
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --repo) arg_repo="$2"; shift 2 ;;
      --cwd)  arg_cwd="$2";  shift 2 ;;
      *)      shift ;;
    esac
  done

  # Wizard fields
  local repo="" pr="" cwd="" runner_label="" runner_script="" default_model=""
  local model="" reflect="false" reflect_branch="" wait_max="300" dry_run="false"

  local detected_repo="${arg_repo:-$(_detect_repo)}"
  local detected_cwd="${arg_cwd:-$(_detect_cwd)}"
  local detected_default_branch="main"

  # Step loop — we walk forward/backward through steps 1..N
  local STEPS=8        # total logical steps (some are conditional)
  local step=1         # current step
  local direction=1    # +1 forward, -1 back

  while true; do
    printf '\033[H\033[J'
    echo ""
    _banner
    echo ""

    # ── Render summary of already-answered fields ──────────────────────────────
    local sum_args=()
    [[ -n "$repo"         ]] && sum_args+=("repository"  "$repo")
    [[ -n "$pr"           ]] && sum_args+=("PR"           "#${pr}")
    [[ -n "$cwd"          ]] && sum_args+=("local path"   "$cwd")
    [[ -n "$runner_label" ]] && sum_args+=("runner"       "$runner_label")
    [[ -n "$model"        ]] && sum_args+=("model"        "$model")
    [[ "$reflect" == "true" ]] && sum_args+=("reflection" "on → ${reflect_branch}")
    [[ "$reflect" == "false" && $step -gt 6 ]] && sum_args+=("reflection" "off")

    if [[ ${#sum_args[@]} -gt 0 ]]; then
      _summary "${sum_args[@]}"
      echo ""
    fi

    # ── Step dispatcher ────────────────────────────────────────────────────────
    case "$step" in

      # ── 1: Repository ─────────────────────────────────────────────────────
      1)
        _step_header 1 7 "repository"
        _muted "  Which GitHub repo? (owner/repo)"
        echo ""
        local new_repo
        new_repo=$(gum input \
          --placeholder "owner/repo" \
          --value "${repo:-$detected_repo}" \
          --prompt "  ❯ " \
          --prompt.foreground "$C_MAUVE" \
          --width 60) || { _err "  Cancelled."; exit 0; }

        if [[ -z "$new_repo" ]]; then
          _err "  Repository is required."; sleep 1; continue
        fi
        repo="$new_repo"
        # Kick off branch detection in background; we'll use it in reflect step
        detected_default_branch=$(_detect_default_branch "$repo")
        step=$(( step + 1 ))
        ;;

      # ── 2: PR picker ──────────────────────────────────────────────────────
      2)
        _step_header 2 7 "pull request"
        _muted "  Fetching open PRs for ${repo}…"
        echo ""

        local open_prs_raw
        open_prs_raw=$(_detect_open_prs "$repo")

        if [[ -z "$open_prs_raw" ]]; then
          # No open PRs — fall back to typed input
          _muted "  (no open PRs found — enter number manually)"
          echo ""
          local new_pr
          new_pr=$(gum input \
            --placeholder "42" \
            --value "${pr:-$arg_pr}" \
            --prompt "  ❯ " \
            --prompt.foreground "$C_MAUVE" \
            --width 20) || { step=$(( step - 1 )); continue; }
          if [[ -z "$new_pr" || ! "$new_pr" =~ ^[0-9]+$ ]]; then
            _err "  Must be a positive integer."; sleep 1; continue
          fi
          pr="$new_pr"
        else
          # Build a choose list — prepend "↩ back" and optionally current val
          local -a pr_options=()
          [[ -n "$pr" ]] && pr_options+=("keep  #${pr}  (current)")
          while IFS= read -r line; do
            pr_options+=("$line")
          done <<< "$open_prs_raw"
          pr_options+=("✏  type a number manually")

          local chosen
          chosen=$(printf '%s\n' "${pr_options[@]}" | gum choose \
            --height 12 \
            --header "  Select a PR  (↑/↓ to navigate, enter to select)" \
            --header.foreground "$C_OVERLAY" \
            --cursor.foreground "$C_MAUVE" \
            --selected.foreground "$C_MAUVE" \
            --cursor "❯ ") || { step=$(( step - 1 )); continue; }

          if [[ "$chosen" == "✏  type a number manually" ]]; then
            echo ""
            local new_pr
            new_pr=$(gum input \
              --placeholder "42" \
              --value "${pr:-$arg_pr}" \
              --prompt "  ❯ " \
              --prompt.foreground "$C_MAUVE" \
              --width 20) || { step=$(( step - 1 )); continue; }
            if [[ -z "$new_pr" || ! "$new_pr" =~ ^[0-9]+$ ]]; then
              _err "  Must be a positive integer."; sleep 1; continue
            fi
            pr="$new_pr"
          elif [[ "$chosen" == keep* ]]; then
            : # keep existing pr
          else
            # Extract the number from "#42  branch  — title"
            pr=$(echo "$chosen" | grep -oE '^#[0-9]+' | tr -d '#')
          fi
        fi
        step=$(( step + 1 ))
        ;;

      # ── 3: Local path ─────────────────────────────────────────────────────
      3)
        _step_header 3 7 "local clone path"
        _muted "  Absolute path to your local checkout of ${repo}"
        echo ""
        local new_cwd
        new_cwd=$(gum input \
          --placeholder "/path/to/repo" \
          --value "${cwd:-$detected_cwd}" \
          --prompt "  ❯ " \
          --prompt.foreground "$C_MAUVE" \
          --width 80) || { step=$(( step - 1 )); continue; }
        [[ -z "$new_cwd" ]] && new_cwd="${cwd:-$detected_cwd}"
        if [[ ! -d "$new_cwd" ]]; then
          gum style --foreground "$C_YELLOW" "  ⚠  Directory not found — continue anyway? (it may be created later)"
          if ! gum confirm "" --affirmative "Continue" --negative "Re-enter" --default=false; then
            continue
          fi
        fi
        cwd="$new_cwd"
        step=$(( step + 1 ))
        ;;

      # ── 4: Runner ─────────────────────────────────────────────────────────
      4)
        _step_header 4 7 "runner"
        _muted "  Which AI agent should drive the review loop?"
        echo ""
        local runner_options=(
          "opencode   — GitHub Copilot, no API key needed  ✦"
          "claude v2  — Claude Code, requires Anthropic key"
          "claude v1  — legacy claude runner"
        )
        local runner_chosen
        runner_chosen=$(printf '%s\n' "${runner_options[@]}" | gum choose \
          --height 6 \
          --cursor.foreground "$C_MAUVE" \
          --selected.foreground "$C_MAUVE" \
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

      # ── 5: Model ──────────────────────────────────────────────────────────
      5)
        _step_header 5 7 "model"
        _muted "  Leave blank to use the runner default: $(gum style --foreground "$C_LAVENDER" "${default_model:-n/a}")"
        echo ""
        local new_model
        new_model=$(gum input \
          --placeholder "${default_model}" \
          --value "${model}" \
          --prompt "  ❯ " \
          --prompt.foreground "$C_MAUVE" \
          --width 60) || { step=$(( step - 1 )); continue; }
        model="$new_model"   # empty = use default
        step=$(( step + 1 ))
        ;;

      # ── 6: Reflection ─────────────────────────────────────────────────────
      6)
        if [[ "$runner_label" == "claude v1" ]]; then
          # Skip reflection for legacy runner
          reflect="false"
          step=$(( step + 1 ))
          continue
        fi
        _step_header 6 7 "reflection agent"
        _muted "  Extracts coding rules from Copilot comments and pushes them to ${detected_default_branch}."
        echo ""
        if gum confirm "Enable reflection?" \
          --affirmative "Yes, enable" \
          --negative "No, skip" \
          --default=false \
          --prompt.foreground "$C_MAUVE"; then
          reflect="true"
          echo ""
          _muted "  Branch to push reflection rules to:"
          echo ""
          local new_rb
          new_rb=$(gum input \
            --placeholder "$detected_default_branch" \
            --value "${reflect_branch:-$detected_default_branch}" \
            --prompt "  ❯ " \
            --prompt.foreground "$C_MAUVE" \
            --width 40) || { reflect="false"; step=$(( step - 1 )); continue; }
          [[ -z "$new_rb" ]] && new_rb="$detected_default_branch"
          reflect_branch="$new_rb"
        else
          reflect="false"
          reflect_branch=""
        fi
        step=$(( step + 1 ))
        ;;

      # ── 7: Advanced / dry-run ─────────────────────────────────────────────
      7)
        _step_header 7 7 "advanced options"
        echo ""
        local new_wait
        new_wait=$(gum input \
          --placeholder "300" \
          --value "${wait_max}" \
          --prompt "  Max wait per Copilot review (seconds) ❯ " \
          --prompt.foreground "$C_OVERLAY" \
          --width 20) || { step=$(( step - 1 )); continue; }
        if [[ -z "$new_wait" || ! "$new_wait" =~ ^[0-9]+$ || "$new_wait" -lt 1 ]]; then
          new_wait=300
        fi
        wait_max="$new_wait"

        echo ""
        if gum confirm "Enable dry run?  (print command only, do not execute)" \
          --affirmative "Yes" \
          --negative "No" \
          --default=false \
          --prompt.foreground "$C_OVERLAY"; then
          dry_run="true"
        else
          dry_run="false"
        fi

        step=$(( step + 1 ))
        ;;

      # ── 8: Review & confirm ───────────────────────────────────────────────
      8)
        local w; w=$(_w)
        local inner=$(( w - 4 ))
        [[ $inner -lt 20 ]] && inner=20

        # Full summary
        local final_rows=()
        final_rows+=("PR"            "#${pr}")
        final_rows+=("repository"    "$repo")
        final_rows+=("local path"    "$cwd")
        final_rows+=("runner"        "$runner_label")
        final_rows+=("model"         "${model:-${default_model} (default)}")
        final_rows+=("reflection"    "$( [[ "$reflect" == "true" ]] && echo "on → ${reflect_branch}" || echo "off" )")
        final_rows+=("wait max"      "${wait_max}s")
        [[ "$dry_run" == "true" ]] && final_rows+=("dry run" "yes")

        local summary_body=""
        local i=0
        while [[ $i -lt ${#final_rows[@]} ]]; do
          local fk="${final_rows[$i]}"
          local fv="${final_rows[$((i+1))]}"
          summary_body+="$(printf '  %s  %s\n' \
            "$(gum style --foreground "$C_OVERLAY"  "$(printf '%-16s' "$fk")")" \
            "$(gum style --foreground "$C_LAVENDER" --bold "$fv")")"$'\n'
          i=$(( i + 2 ))
        done

        gum style \
          --border double \
          --border-foreground "$C_MAUVE" \
          --width "$inner" \
          --padding "1 2" \
          "$(gum style --bold --foreground "$C_TEXT" "  Review your settings")" \
          "" \
          "${summary_body%$'\n'}"

        echo ""

        local action
        action=$(printf '%s\n' \
          "🚀  Launch" \
          "✏   Edit settings  (go back)" \
          "✗   Abort" \
          | gum choose \
            --height 5 \
            --cursor.foreground "$C_MAUVE" \
            --selected.foreground "$C_MAUVE" \
            --cursor "❯ ") || { _muted "  Aborted."; exit 0; }

        case "$action" in
          *Launch*)
            break
            ;;
          *"Edit settings"*)
            step=$(( step - 1 ))
            continue
            ;;
          *Abort*)
            echo ""
            _muted "  Aborted."
            exit 0
            ;;
        esac
        ;;

      *)
        # Should never happen
        break
        ;;
    esac
  done

  # ── Build command ──────────────────────────────────────────────────────────
  local cmd=("$runner_script" "$pr" "--repo" "$repo" "--cwd" "$cwd" "--wait-max" "$wait_max")
  [[ -n "$model" ]] && cmd+=("--model" "$model")
  [[ "$reflect" == "true" ]] && cmd+=("--reflect" "--reflect-main-branch" "$reflect_branch")
  [[ "$dry_run" == "true" ]] && cmd+=("--dry-run")

  # ── Launch banner ──────────────────────────────────────────────────────────
  printf '\033[H\033[J'
  echo ""
  local w; w=$(_w)
  local inner=$(( w - 4 ))
  [[ $inner -lt 20 ]] && inner=20

  gum style \
    --border double \
    --border-foreground "$C_TEAL" \
    --align center \
    --width "$inner" \
    --padding "1 3" \
    "$(gum style --bold --foreground "$C_TEAL"    "launching pr-review")" \
    "" \
    "$(gum style --foreground "$C_LAVENDER" "PR #${pr}  ·  ${repo}  ·  ${runner_label}")"

  echo ""

  if [[ "$dry_run" == "true" ]]; then
    gum style --foreground "$C_YELLOW" "  dry run — command that would be executed:"
    echo ""
    gum style --foreground "$C_OVERLAY" "  ${cmd[*]}"
    exit 0
  fi

  exec "${cmd[@]}"
}

main "$@"
