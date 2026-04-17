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
#     Call at the end of the run (approval / clean / stall / error).
#     outcome: "approved" | "clean" | "stalled" | "error"
#
#   insights_print [--json]
#     Render the summary to stdout.
#     --json: emit machine-readable JSON instead of the TUI view.
#
# Globals set by insights_init (read-only externally):
#   _INS_PR, _INS_REPO, _INS_MODEL, _INS_START_EPOCH
#
# This file intentionally has no set -e so callers are not affected when sourced.

# ─── Bash version guard ───────────────────────────────────────────────────────

if [[ -z "${BASH_VERSINFO:-}" || "${BASH_VERSINFO[0]}" -lt 4 ]]; then
  echo "pr-review-insights.sh requires Bash 4+ (associative arrays are used for category counters)." >&2
  return 1 2>/dev/null || exit 1
fi

# ─── Internal state ───────────────────────────────────────────────────────────

_INS_PR=""
_INS_REPO=""
_INS_MODEL=""
_INS_START_EPOCH=0
_INS_END_EPOCH=0
_INS_TOTAL_ITERS=0
_INS_TOTAL_COMMENTS=0
_INS_OUTCOME=""

# Category counters (associative array — requires bash 4+)
declare -A _INS_CATS 2>/dev/null || true

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
  if echo "$lower" | grep -qE 'inject|sanitiz|xss|csrf|sql injection|auth|secret|credential|password|token|eval\(|unsafe'; then
    echo "security"; return
  fi

  # Error handling
  if echo "$lower" | grep -qE '\berror\b|\bexception\b|\bcatch\b|\bthrow\b|\bpanic\b|\bfatal\b|nil check|null check|unhandled'; then
    echo "error_handling"; return
  fi

  # Performance
  if echo "$lower" | grep -qE 'performance|complexity|o\(n\)|o\(n\^2\)|cache|memoiz|lazy|eager|alloc|memory leak|overhead'; then
    echo "performance"; return
  fi

  # Type safety
  if echo "$lower" | grep -qE '\btype\b|\btyping\b|\bany\b|\bcast\b|\bassertion\b|\binterface\b|\bschema\b|type-safe|type safe'; then
    echo "type_safety"; return
  fi

  # Testing
  if echo "$lower" | grep -qE '\btest\b|\bcoverage\b|\bassert\b|\bspec\b|\bmock\b|\bfixture\b|\bstub\b|\bunit test\b'; then
    echo "testing"; return
  fi

  # Documentation
  if echo "$lower" | grep -qE '\bdoc\b|\bcomment\b|\bdocstring\b|\bjsdoc\b|\breadme\b|\bexample\b|\bdocument\b'; then
    echo "documentation"; return
  fi

  # Naming
  if echo "$lower" | grep -qE 'naming|variable name|function name|\bidentifier\b|convention|camelcase|snake_case|mislead'; then
    echo "naming"; return
  fi

  # Style
  if echo "$lower" | grep -qE '\bstyle\b|\bformat\b|\bindent\b|\blint\b|\bspacing\b|\bwhitespace\b|trailing'; then
    echo "style"; return
  fi

  # Logic / correctness
  if echo "$lower" | grep -qE '\bbug\b|\blogic\b|incorrect|wrong|off.by.one|\bcondition\b|\bbranch\b|incorrect behavior|doesn.t work'; then
    echo "logic"; return
  fi

  echo "general"
}

# ─── insights_init ────────────────────────────────────────────────────────────

insights_init() {
  _INS_PR="${1:?insights_init: pr_number required}"
  _INS_REPO="${2:?insights_init: repo required}"
  _INS_MODEL="${3:-unknown}"
  _INS_START_EPOCH=$(date -u +%s 2>/dev/null || echo 0)
  _INS_END_EPOCH=0
  _INS_TOTAL_ITERS=0
  _INS_TOTAL_COMMENTS=0
  _INS_OUTCOME=""
  _INS_ITER_LOG="[]"

  # Reset category counters
  declare -A _INS_CATS 2>/dev/null || true
  local cats=(security error_handling performance type_safety testing documentation naming style logic general)
  for cat in "${cats[@]}"; do
    _INS_CATS["$cat"]=0
  done
}

# ─── insights_record_iteration ────────────────────────────────────────────────

insights_record_iteration() {
  local comment_count="${1:-0}"
  local comments_json="${2:-[]}"

  _INS_TOTAL_ITERS=$(( _INS_TOTAL_ITERS + 1 ))
  _INS_TOTAL_COMMENTS=$(( _INS_TOTAL_COMMENTS + comment_count ))

  # Classify each comment
  local iter_cats="{}"
  local i n
  n=$(printf '%s' "$comments_json" | jq 'length' 2>/dev/null || echo 0)
  for i in $(seq 0 $(( n - 1 ))); do
    local body
    body=$(printf '%s' "$comments_json" | jq -r ".[$i].body // \"\"" 2>/dev/null || echo "")
    local cat
    cat=$(_ins_classify_comment "$body")
    _INS_CATS["$cat"]=$(( ${_INS_CATS["$cat"]:-0} + 1 ))
  done

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
      pr:                ($pr | tonumber),
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
