#!/usr/bin/env bash
# pr-review-insights.sh — Post-cycle insights: metrics tracking and summary output
#
# Source this file from a RINSE runner to get structured insights after each run.
#
# Public API:
#   insights_init  <pr_number> <repo> <model>
#     Call once at runner startup.
#
#   insights_record_iteration <comment_count> <comments_json>
#     Call at the end of each iteration that addressed comments.
#     comments_json: the raw JSON array from get_review_comments().
#
#   insights_finalize <outcome>
#     Call at the end of the run (approval / clean / stall / skip / error).
#     outcome: "approved" | "clean" | "stalled" | "skipped" | "error"
#
#   insights_print [--json]
#     Render the summary to stdout.
#     --json: emit machine-readable JSON instead of the TUI view.
#
# Globals set by insights_init (read-only externally):
#   _INS_PR, _INS_REPO, _INS_MODEL, _INS_START_EPOCH
#
# This file intentionally has no set -e so callers are not affected when sourced.

# ─── Internal state ───────────────────────────────────────────────────────────

_INS_PR=""
_INS_REPO=""
_INS_MODEL=""
_INS_START_EPOCH=0
_INS_END_EPOCH=0
_INS_TOTAL_ITERS=0
_INS_TOTAL_COMMENTS=0
_INS_OUTCOME=""

# Insights require bash 4+ for associative arrays. On bash 3.x (e.g., macOS default),
# set _INS_SUPPORTED=false so all public functions become no-ops.
_INS_SUPPORTED=false
if [[ "${BASH_VERSINFO[0]:-0}" -ge 4 ]]; then
  _INS_SUPPORTED=true
fi

# Category counters (associative array — requires bash 4+)
if [[ "$_INS_SUPPORTED" == true ]]; then
  declare -A _INS_CATS
fi

# Per-iteration log (JSON array of objects) — built incrementally
_INS_ITER_LOG="[]"

# ─── Category classifier ──────────────────────────────────────────────────────
#
# _ins_classify_comment <comment_body>
# Prints one of the canonical category names to stdout.
#
# Categories (ordered by specificity):
#   security        — injection, sanitize, XSS, CSRF, auth, secret, credential, eval
#   error_handling  — error, exception, catch, throw, panic, fatal, nil check, null check
#   performance     — performance, complexity, O(n), loop, cache, lazy, eager, alloc
#   type_safety     — type, typing, any, cast, assertion, interface, schema
#   testing         — test, coverage, assert, spec, mock, fixture, stub
#   documentation   — doc, comment, docstring, jsdoc, readme, example
#   naming          — naming, name, variable, function, identifier, convention, camel, snake
#   style           — style, format, indent, lint, spacing, whitespace, semicolon
#   logic           — logic, bug, incorrect, wrong, off-by-one, condition, branch
#   general         — everything else

_ins_classify_comment() {
  local body="${1:-}"
  local lower
  lower=$(printf '%s' "$body" | tr '[:upper:]' '[:lower:]')

  # Security (highest priority)
  if grep -qE 'inject|sanitiz|xss|csrf|sql injection|(^|[^[:alnum:]_])(auth|authn|authz|authentication|authorization)([^[:alnum:]_]|$)|secret|credential|password|token|eval\(|unsafe' <<<"$lower"; then
    echo "security"; return
  fi

  # Error handling
  if grep -qE '(^|[^[:alnum:]_])(error|exception|catch|throw|panic|fatal)([^[:alnum:]_]|$)|nil check|null check|unhandled' <<<"$lower"; then
    echo "error_handling"; return
  fi

  # Performance
  if grep -qE 'performance|complexity|o\(n\)|o\(n\^2\)|cache|memoiz|lazy|eager|alloc|memory leak|overhead' <<<"$lower"; then
    echo "performance"; return
  fi

  # Type safety
  if grep -qE '(^|[^[:alnum:]_])(type|typing|any|cast|assertion|interface|schema)([^[:alnum:]_]|$)|type-safe|type safe' <<<"$lower"; then
    echo "type_safety"; return
  fi

  # Testing
  if grep -qE '(^|[^[:alnum:]_])(test|coverage|assert|spec|mock|fixture|stub)([^[:alnum:]_]|$)|unit test' <<<"$lower"; then
    echo "testing"; return
  fi

  # Documentation
  if grep -qE '(^|[^[:alnum:]_])(doc|comment|docstring|jsdoc|readme|example|document)([^[:alnum:]_]|$)' <<<"$lower"; then
    echo "documentation"; return
  fi

  # Naming
  if grep -qE 'naming|variable name|function name|(^|[^[:alnum:]_])identifier([^[:alnum:]_]|$)|convention|camelcase|snake_case|mislead' <<<"$lower"; then
    echo "naming"; return
  fi

  # Style
  if grep -qE '(^|[^[:alnum:]_])(style|format|indent|lint|spacing|whitespace)([^[:alnum:]_]|$)|trailing' <<<"$lower"; then
    echo "style"; return
  fi

  # Logic / correctness
  if grep -qE '(^|[^[:alnum:]_])(bug|logic)([^[:alnum:]_]|$)|incorrect|wrong|off.by.one|(^|[^[:alnum:]_])(condition|branch)([^[:alnum:]_]|$)|incorrect behavior|doesn.t work' <<<"$lower"; then
    echo "logic"; return
  fi

  echo "general"
}

# ─── insights_init ────────────────────────────────────────────────────────────

insights_init() {
  # Require bash 4+ for associative arrays; disable insights gracefully on older bash.
  if [[ "$_INS_SUPPORTED" != true ]]; then
    >&2 echo "[rinse-insights] bash 4+ required for insights; disabling."
    return 0
  fi

  _INS_PR="${1:?insights_init: pr_number required}"
  _INS_REPO="${2:?insights_init: repo required}"
  _INS_MODEL="${3:-unknown}"
  _INS_START_EPOCH=$(date -u +%s 2>/dev/null || echo 0)
  _INS_END_EPOCH=0
  _INS_TOTAL_ITERS=0
  _INS_TOTAL_COMMENTS=0
  _INS_OUTCOME=""
  _INS_ITER_LOG="[]"

  # Reset category counters (reset known keys directly; avoids unset+redeclare portability issues)
  local cats=(security error_handling performance type_safety testing documentation naming style logic general)
  local cat
  for cat in "${cats[@]}"; do
    _INS_CATS["$cat"]=0
  done
}

# ─── insights_record_iteration ────────────────────────────────────────────────

insights_record_iteration() {
  [[ "$_INS_SUPPORTED" != true ]] && return 0
  local comment_count="${1:-0}"
  local comments_json="${2:-[]}"

  _INS_TOTAL_ITERS=$(( _INS_TOTAL_ITERS + 1 ))
  _INS_TOTAL_COMMENTS=$(( _INS_TOTAL_COMMENTS + comment_count ))

  # Classify each comment
  local i n
  n=$(printf '%s' "$comments_json" | jq 'length' 2>/dev/null || echo 0)
  if (( n > 0 )); then
    for i in $(seq 0 $(( n - 1 ))); do
      local body
      body=$(printf '%s' "$comments_json" | jq -r ".[$i].body // \"\"" 2>/dev/null || echo "")
      local cat
      cat=$(_ins_classify_comment "$body")
      _INS_CATS["$cat"]=$(( ${_INS_CATS["$cat"]:-0} + 1 ))
    done
  fi

  # Snapshot category counts for this iteration
  local cats_snapshot
  cats_snapshot=$(jq -n \
    --argjson security    "${_INS_CATS[security]:-0}" \
    --argjson error_handling "${_INS_CATS[error_handling]:-0}" \
    --argjson performance "${_INS_CATS[performance]:-0}" \
    --argjson type_safety "${_INS_CATS[type_safety]:-0}" \
    --argjson testing     "${_INS_CATS[testing]:-0}" \
    --argjson documentation "${_INS_CATS[documentation]:-0}" \
    --argjson naming      "${_INS_CATS[naming]:-0}" \
    --argjson style       "${_INS_CATS[style]:-0}" \
    --argjson logic       "${_INS_CATS[logic]:-0}" \
    --argjson general     "${_INS_CATS[general]:-0}" \
    '{security:$security, error_handling:$error_handling, performance:$performance,
      type_safety:$type_safety, testing:$testing, documentation:$documentation,
      naming:$naming, style:$style, logic:$logic, general:$general}' 2>/dev/null || echo "{}")

  local iter_entry
  iter_entry=$(jq -n \
    --argjson iter  "$_INS_TOTAL_ITERS" \
    --argjson count "$comment_count" \
    --argjson cats  "$cats_snapshot" \
    '{iter:$iter, comments_addressed:$count, categories:$cats}' 2>/dev/null || echo "{}")

  _INS_ITER_LOG=$(printf '%s' "$_INS_ITER_LOG" | jq --argjson e "$iter_entry" '. + [$e]' 2>/dev/null || echo "$_INS_ITER_LOG")
}

# ─── insights_finalize ────────────────────────────────────────────────────────

insights_finalize() {
  [[ "$_INS_SUPPORTED" != true ]] && return 0
  _INS_OUTCOME="${1:-unknown}"
  _INS_END_EPOCH=$(date -u +%s 2>/dev/null || echo 0)
}

# ─── insights_as_json ─────────────────────────────────────────────────────────

insights_as_json() {
  local elapsed=$(( _INS_END_EPOCH - _INS_START_EPOCH ))
  [[ $elapsed -lt 0 ]] && elapsed=0

  jq -n \
    --arg pr              "$_INS_PR" \
    --arg repo            "$_INS_REPO" \
    --arg model           "$_INS_MODEL" \
    --arg outcome         "$_INS_OUTCOME" \
    --argjson elapsed     "$elapsed" \
    --argjson iters       "$_INS_TOTAL_ITERS" \
    --argjson total_comments "$_INS_TOTAL_COMMENTS" \
    --argjson security    "${_INS_CATS[security]:-0}" \
    --argjson error_handling "${_INS_CATS[error_handling]:-0}" \
    --argjson performance "${_INS_CATS[performance]:-0}" \
    --argjson type_safety "${_INS_CATS[type_safety]:-0}" \
    --argjson testing     "${_INS_CATS[testing]:-0}" \
    --argjson documentation "${_INS_CATS[documentation]:-0}" \
    --argjson naming      "${_INS_CATS[naming]:-0}" \
    --argjson style       "${_INS_CATS[style]:-0}" \
    --argjson logic       "${_INS_CATS[logic]:-0}" \
    --argjson general     "${_INS_CATS[general]:-0}" \
    --argjson iterations  "$_INS_ITER_LOG" \
    '{
      schema:            "rinse-insights-v1",
      pr:                ($pr | tonumber? // null),
      repo:              $repo,
      model:             $model,
      outcome:           $outcome,
      elapsed_seconds:   $elapsed,
      iterations:        $iters,
      comments_addressed: $total_comments,
      categories: {
        security:        $security,
        error_handling:  $error_handling,
        performance:     $performance,
        type_safety:     $type_safety,
        testing:         $testing,
        documentation:   $documentation,
        naming:          $naming,
        style:           $style,
        logic:           $logic,
        general:         $general
      },
      iteration_log:     $iterations
    }' 2>/dev/null
}

# ─── insights_print ───────────────────────────────────────────────────────────
#
# insights_print [--json]
# Renders insights. Call after insights_finalize.

insights_print() {
  [[ "$_INS_SUPPORTED" != true ]] && return 0
  local json_mode=false
  [[ "${1:-}" == "--json" ]] && json_mode=true

  if [[ "$json_mode" == true ]]; then
    insights_as_json
    return
  fi

  # ── Human-readable TUI output ──────────────────────────────────────────────
  # Delegates rendering to ui_insights_summary() from pr-review-ui.sh.
  # If that function isn't loaded, fall back to a plain-text summary.
  if declare -f ui_insights_summary >/dev/null 2>&1; then
    ui_insights_summary \
      "$_INS_PR" "$_INS_REPO" "$_INS_MODEL" "$_INS_OUTCOME" \
      "$(( _INS_END_EPOCH - _INS_START_EPOCH ))" \
      "$_INS_TOTAL_ITERS" "$_INS_TOTAL_COMMENTS" \
      "${_INS_CATS[security]:-0}" \
      "${_INS_CATS[error_handling]:-0}" \
      "${_INS_CATS[performance]:-0}" \
      "${_INS_CATS[type_safety]:-0}" \
      "${_INS_CATS[testing]:-0}" \
      "${_INS_CATS[documentation]:-0}" \
      "${_INS_CATS[naming]:-0}" \
      "${_INS_CATS[style]:-0}" \
      "${_INS_CATS[logic]:-0}" \
      "${_INS_CATS[general]:-0}"
  else
    _ins_print_plain
  fi
}

# ─── _ins_print_plain ─────────────────────────────────────────────────────────
# Fallback plain-text summary (no gum, no ANSI required).

_ins_print_plain() {
  local elapsed=$(( _INS_END_EPOCH - _INS_START_EPOCH ))
  [[ $elapsed -lt 0 ]] && elapsed=0
  local mins=$(( elapsed / 60 ))
  local secs=$(( elapsed % 60 ))

  echo ""
  echo "════════════════════════════════════════════════════════"
  echo "  RINSE Cycle Summary — PR #${_INS_PR}"
  echo "════════════════════════════════════════════════════════"
  echo "  Outcome   : ${_INS_OUTCOME}"
  echo "  Model     : ${_INS_MODEL}"
  echo "  Elapsed   : ${mins}m ${secs}s"
  echo "  Iterations: ${_INS_TOTAL_ITERS}"
  echo "  Comments  : ${_INS_TOTAL_COMMENTS} addressed"
  echo ""
  echo "  Issues by category:"
  local cats=(security error_handling performance type_safety testing documentation naming style logic general)
  for cat in "${cats[@]}"; do
    local count="${_INS_CATS[$cat]:-0}"
    if [[ "$count" -gt 0 ]]; then
      printf "    %-18s %d\n" "${cat}:" "$count"
    fi
  done
  echo "════════════════════════════════════════════════════════"
  echo ""
}
