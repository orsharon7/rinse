#!/usr/bin/env bash
# _symlink-helper.sh — Shared helper: ensure CLAUDE.md → AGENTS.md symlink
#
# Source this file and call: ensure_claude_symlink <worktree_dir>
#
# Ensures CLAUDE.md in the given directory is a valid symlink pointing to
# AGENTS.md. If it exists as a regular file or points elsewhere, it is replaced.
#
# Uses a `log` function from the sourcing script when available.
# If no `log` function is defined, messages fall back to `echo`.

# Internal logging: use caller's log() if available, else echo.
_symlink_log() {
  if declare -F log >/dev/null 2>&1; then
    log "$@"
  else
    echo "$@"
  fi
}

ensure_claude_symlink() {
  local worktree_dir="$1"
  local _claude_md
  local _target

  if [[ -z $worktree_dir || ! -d $worktree_dir ]]; then
    _symlink_log "ensure_claude_symlink: invalid worktree_dir '${worktree_dir}'"
    return 1
  fi

  _claude_md="${worktree_dir}/CLAUDE.md"

  if [[ -L "$_claude_md" ]]; then
    _target=$(readlink "$_claude_md")
    if [[ "$_target" != "AGENTS.md" ]]; then
      _symlink_log "CLAUDE.md symlink points to '${_target}' instead of 'AGENTS.md' — recreating"
      rm -f -- "$_claude_md"
      ln -sf AGENTS.md "$_claude_md"
    fi
  elif [[ -e "$_claude_md" ]]; then
    _symlink_log "CLAUDE.md exists as a regular file — replacing with symlink"
    rm -f -- "$_claude_md"
    ln -sf AGENTS.md "$_claude_md"
  else
    ln -sf AGENTS.md "$_claude_md"
    _symlink_log "Created CLAUDE.md → AGENTS.md symlink"
  fi
}
