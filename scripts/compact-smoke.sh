#!/bin/sh
# Usage: ./compact-smoke.sh --base-url URL --api-key KEY --model MODEL
# Zeabur: cd /tmp && npx --yes zeabur -i=false service exec --id 69d913e9e8ec40d5bceac923 -- sh -c '<只读脚本>'
set -eu
base_url=${CPA_BASE_URL:-}; api_key=${CPA_API_KEY:-}; model=${CPA_MODEL:-}; timeout=120; curl_bin=${CURL_BIN:-curl}; expect_failure=0
usage() { echo "Usage: $0 --base-url URL --api-key KEY --model NAME [--timeout SEC] [--curl-bin PATH] [--expect-failure]"; echo "Exit 0=all checks pass, 3=bridge pass but sentinel absent, other non-zero=bridge failure."; }
while [ "$#" -gt 0 ]; do
 case "$1" in
  --base-url) base_url=$2; shift 2;; --api-key) api_key=$2; shift 2;; --model) model=$2; shift 2;;
  --timeout) timeout=$2; shift 2;; --curl-bin) curl_bin=$2; shift 2;; --expect-failure) expect_failure=1; shift;;
  -h|--help) usage; exit 0;; *) echo "unknown option: $1" >&2; usage >&2; exit 2;;
 esac
done
[ -n "$base_url" ] || { echo "missing --base-url (or CPA_BASE_URL)" >&2; exit 2; }; [ -n "$api_key" ] || { echo "missing --api-key (or CPA_API_KEY)" >&2; exit 2; }; [ -n "$model" ] || { echo "missing --model (or CPA_MODEL)" >&2; exit 2; }
tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/compact-smoke.XXXXXX"); trap 'rm -rf "$tmpdir"' EXIT HUP INT TERM
payload=$tmpdir/payload.json; headers=$tmpdir/headers; body=$tmpdir/body; sentinel=CPA_SMOKE_SENTINEL_7F3A9C
if [ "$expect_failure" -eq 1 ]; then
# A foreign opaque compaction item is the deterministic client-side rejection: the plugin
# never owns native state it cannot read, so it must answer 400 without opening a stream.
 cat >"$payload" <<EOF
{"model":"$model","stream":false,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"$sentinel"}]},{"type":"compaction","id":"cmp_smoke_foreign","encrypted_content":"bm90LW91ci1mb3JtYXQ="}]}
EOF
 status=$($curl_bin -sS --max-time "$timeout" -D "$headers" -o "$body" -w '%{http_code}' -H "Authorization: Bearer $api_key" -H 'Content-Type: application/json' "$base_url/v1/responses" --data-binary "@$payload" 2>/dev/null || true)
 if [ "$status" = 400 ] && grep -q 'invalid_compaction_state' "$body"; then echo "PASS expect-failure: HTTP 400 invalid_compaction_state"; exit 0; fi
 echo "compact_bridge_failed: expect-failure assertion" >&2; echo "http_status=$status" >&2; echo "failure=expected HTTP 400 with invalid_compaction_state" >&2; exit 1
fi
cat >"$payload" <<EOF
{"model":"$model","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Conversation marker $sentinel: summarize this deployment smoke test."}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Acknowledged marker $sentinel and the compaction request."}]},{"type":"compaction_trigger"}]}
EOF
status=$($curl_bin -sS -N --max-time "$timeout" -D "$headers" -o "$body" -w '%{http_code}' -H "Authorization: Bearer $api_key" -H 'Content-Type: application/json' "$base_url/v1/responses" --data-binary "@$payload" 2>/dev/null || true)
events=$(awk '/^event:/{print $2} /"type"[[:space:]]*:/ { line=$0; while (match(line,/"type"[[:space:]]*:[[:space:]]*"[^"]+"/)) { x=substr(line,RSTART,RLENGTH); sub(/^.*"type"[[:space:]]*:[[:space:]]*"/,"",x); sub(/".*$/, "", x); print x; line=substr(line,RSTART+RLENGTH) }}' "$body" | awk 'NF&&!seen[$0]++' | tr '\n' ',' | sed 's/,$//')
item_done=$(grep -c 'response\.output_item\.done' "$body"); ids=$(grep -Eo '"id"[[:space:]]*:[[:space:]]*"cpa_compact_[^"]*"' "$body" | sed 's/.*"cpa_compact_/cpa_compact_/; s/"$//' | sort -u); id_count=$(printf '%s\n' "$ids" | grep -c .); compact_id=$(printf '%s\n' "$ids" | head -n 1); failed=0; grep -q 'response.failed' "$body" && failed=1 || true; completed=0; grep -q 'response.completed' "$body" && completed=1 || true
encrypted=$(grep -Eo '"encrypted_content"[[:space:]]*:[[:space:]]*"([^"\\]|\\.)*"' "$body" | head -n 1 | sed 's/^[^:]*:[[:space:]]*"//; s/"$//'); enc_len=$(printf '%s' "$encrypted" | wc -c | tr -d ' ')
bad=""; [ "$status" = 200 ] || bad="http_status"; [ "$id_count" = 1 ] || bad="${bad:+$bad,}compaction_item_count"; case "$compact_id" in cpa_compact_*) :;; *) bad="${bad:+$bad,}compaction_id";; esac; [ "$item_done" = 1 ] || bad="${bad:+$bad,}output_item_done_frames"; [ "$completed" -eq 1 ] || bad="${bad:+$bad,}response_completed"; [ "$failed" -eq 0 ] || bad="${bad:+$bad,}response_failed"; [ "$enc_len" -ge 1 ] && [ "$enc_len" -le 1048576 ] || bad="${bad:+$bad,}encrypted_content"
sentinel_missing=0; printf '%s' "$encrypted" | grep -q "$sentinel" || sentinel_missing=1
if [ -z "$bad" ] && [ "$sentinel_missing" -eq 0 ]; then echo "PASS compact smoke: HTTP $status; compaction item=$compact_id; encrypted_content_bytes=$enc_len"; exit 0; fi
if [ -z "$bad" ] && [ "$sentinel_missing" -eq 1 ]; then echo "PASS(without sentinel): bridge checks passed but summary omitted $sentinel"; exit 3; fi
if grep -q 'invalid_compaction_state' "$body"; then echo "invalid_compaction_state: 状态被拒，不是抖动，别重试部署" >&2; else echo "compact_bridge_failed: bridge output did not satisfy smoke criteria" >&2; fi
echo "http_status=$status" >&2; echo "events=${events:-none}" >&2; echo "failed_criteria=$bad" >&2; exit 1
