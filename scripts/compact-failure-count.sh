#!/bin/sh
# Cron example (run hourly with a five-minute overlap for log mtime):
# 0 * * * * /CLIProxyAPI/scripts/compact-failure-count.sh --since 65
#
# Counts only failure markers; it never prints request or model bodies.
set -eu

OUT=${OUT:-/CLIProxyAPI/logs/compact-failures.tsv}
MAIN_LOG=${MAIN_LOG:-/CLIProxyAPI/logs/main.log}
REQUEST_GLOB=${REQUEST_GLOB:-/CLIProxyAPI/logs/v1-responses-*.log}
since_minutes=0

usage() {
  echo "Usage: $0 [--since MINUTES]" >&2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --since)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      since_minutes=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

case "$since_minutes" in
  ''|*[!0-9]*) echo "--since must be a non-negative minute count" >&2; exit 2 ;;
esac

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/compact-failure-count.XXXXXX")
trap 'rm -rf "$tmpdir"' EXIT HUP INT TERM
files=$tmpdir/files
selected=$tmpdir/selected
: >"$files"

add_file() {
  [ -f "$1" ] || return 0
  if [ "$since_minutes" -eq 0 ]; then
    printf '%s\n' "$1" >>"$files"
  elif find "$1" -mmin "-$since_minutes" -type f -print 2>/dev/null | grep -q .; then
    printf '%s\n' "$1" >>"$files"
  fi
}

add_file "$MAIN_LOG"
for log in $REQUEST_GLOB; do
  add_file "$log"
done

if [ -s "$files" ]; then
  sed '/^$/d' "$files" | sort -u >"$selected"
else
  : >"$selected"
fi

count_pattern() {
  pattern=$1
  if [ -s "$selected" ]; then
    (xargs grep -h -F "$pattern" <"$selected" 2>/dev/null || true) | wc -l | tr -d ' '
  else
    printf '0\n'
  fi
}

# Markers come from a8bafc8. Parameter values are deliberately not emitted.
count_bridge_all=$(count_pattern 'bridged compaction failed')
count_bridge_encode=$(count_pattern 'bridged compaction failed: encode error')
count_bridge_generic=$((count_bridge_all - count_bridge_encode))
count_ordinary_stream=$(count_pattern 'unexpected ordinary streaming bridge request')
count_multiple_terminal_shapes=$(count_pattern 'summary response has multiple terminal shapes')
count_unknown_terminal_shape=$(count_pattern 'summary response has unknown terminal shape')
count_upstream_failed=$(count_pattern 'summary upstream failed (status=')
count_upstream_incomplete_all=$(count_pattern 'summary upstream incomplete')
count_incomplete_max_output_tokens=$(count_pattern 'summary upstream incomplete (reason=max_output_tokens)')
count_incomplete_content_filter=$(count_pattern 'summary upstream incomplete (reason=content_filter)')
count_incomplete_finish_content_filter=$(count_pattern 'summary upstream incomplete (finish_reason=content_filter)')
count_upstream_incomplete=$((count_upstream_incomplete_all - count_incomplete_max_output_tokens - count_incomplete_content_filter - count_incomplete_finish_content_filter))
count_truncated_length=$(count_pattern 'summary upstream truncated (finish_reason=length)')
count_truncated_max_tokens=$(count_pattern 'summary upstream truncated (finish_reason=max_tokens)')
count_tool_call_root_cause=$(count_pattern 'summary upstream returned tool call')
count_unsupported_output_guard=$(count_pattern 'summary upstream returned unsupported output item')
count_invalid_terminal_status_guard=$(count_pattern 'summary upstream invalid terminal status')
count_missing_terminal_status_guard=$(count_pattern 'summary text missing terminal status')
count_no_usable_text=$(count_pattern 'summary model produced no usable text')
count_oversized=$(count_pattern 'summary exceeds ')
count_total=$(count_pattern 'compact_bridge_failed')

timestamp=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
out_dir=$(dirname "$OUT")
[ -d "$out_dir" ] || { echo "output directory does not exist: $out_dir" >&2; exit 1; }

append_count() {
  printf '%s\t%s\t%s\n' "$timestamp" "$1" "$2" >>"$OUT"
}

append_count bridge_generic "$count_bridge_generic"
append_count bridge_encode "$count_bridge_encode"
append_count ordinary_stream "$count_ordinary_stream"
append_count multiple_terminal_shapes "$count_multiple_terminal_shapes"
append_count unknown_terminal_shape "$count_unknown_terminal_shape"
append_count upstream_failed "$count_upstream_failed"
append_count upstream_incomplete "$count_upstream_incomplete"
append_count incomplete_max_output_tokens "$count_incomplete_max_output_tokens"
append_count incomplete_content_filter "$count_incomplete_content_filter"
append_count incomplete_finish_content_filter "$count_incomplete_finish_content_filter"
append_count truncated_length "$count_truncated_length"
append_count truncated_max_tokens "$count_truncated_max_tokens"
append_count tool_call_root_cause "$count_tool_call_root_cause"
append_count unsupported_output_guard "$count_unsupported_output_guard"
append_count invalid_terminal_status_guard "$count_invalid_terminal_status_guard"
append_count missing_terminal_status_guard "$count_missing_terminal_status_guard"
append_count no_usable_text "$count_no_usable_text"
append_count oversized "$count_oversized"
append_count compact_bridge_failed "$count_total"
