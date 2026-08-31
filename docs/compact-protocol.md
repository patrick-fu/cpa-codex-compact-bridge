# Compact protocol contract

The compact transports below are owned only by a request matched by a `bridge` Bridge Rule. Native Compact Routes keep their own compact behavior, but the Compaction State Policy in [Session state](#session-state) applies to every Responses request regardless of the target rule.

## V1

`POST /v1/responses/compact` is non-streaming. The facade summarizes through its Summary Model and returns a canonical compacted window: the request's user/developer messages followed by one marked compaction item.

```json
{
  "output": [
    {
      "type": "message",
      "role": "user",
      "content": [{"type": "input_text", "text": "<retained user message>"}]
    },
    {
      "type": "compaction",
      "id": "cpa_compact_<uuid>",
      "encrypted_content": "<summary>"
    }
  ]
}
```

No SSE event and no `response.completed` are part of V1. Codex parses `output` directly as its replacement history. The bridge follows the current remote endpoint behavior: retain only `message` items with role `user` or `developer`, preserve those items as received, discard protocol artifacts, then append the newest compaction item.

## V2

The facade recognizes a streaming Responses request whose final input item is `{"type":"compaction_trigger"}`. It removes the trigger before making the ordinary Summary Model request and returns exactly two SSE frames:

```text
data: {"type":"response.output_item.done","item":{"type":"compaction","id":"cpa_compact_<uuid>","encrypted_content":"<summary>"}}

data: {"type":"response.completed","response":{"id":"resp_cpa_compact_<uuid>"}}

```

In both V1 and V2, `encrypted_content` contains the plaintext summary directly. `cpa_compact_<uuid>` is an item marker, not a native encrypted state or the completed response ID. The V2 facade emits no partial summary, extra output item, invented token usage, or synthetic `response.created` event.

## WebSocket transport

The first release supports a matched Responses WebSocket `response.create` V2 compaction trigger. The facade returns the same Responses event JSON used by its HTTP SSE path; CPA converts those events to WebSocket text messages.

V1 remains the non-streaming HTTP `/responses/compact` endpoint, including when ordinary turns in the same Codex session use WebSockets.

The release gate is an end-to-end WebSocket test with a prior response ID and a V2 trigger. It proves that the facade receives a final `compaction_trigger`, the client receives the terminal compaction event, and the following continuation retains the summary. CPA normalizes that continuation after ModelRouter has declined its ordinary turn; the plugin's request interceptor then replaces the merged `cpa_compact_*` item with a normal user summary before the third-party provider translation. If the gate fails, WebSocket support is excluded from that release and the supported transport is HTTP only; CPA core is not changed to force support.

## Session state

The request interceptor applies one policy to the `compaction` items of every `openai-response` request. Reasoning items and every other item type are never read or rewritten.

| Request state | Explicit `bridge` target | Explicit `passthrough` target | Unmatched target |
|---|---|---|---|
| No `compaction` item | unchanged | unchanged | unchanged |
| Only valid `cpa_compact_*` items with non-blank `encrypted_content` | each becomes one ordinary user summary message | same rewrite, then CPA's native route continues the turn | same rewrite, then CPA's fallback route continues the turn |
| Only unmarked (native) `compaction` items | `invalid_compaction_state` | forwarded exactly as received | `invalid_compaction_state` |
| Both kinds in one request | `invalid_compaction_state` | `invalid_compaction_state` | `invalid_compaction_state` |
| `cpa_compact_*` with absent, empty, or whitespace-only `encrypted_content` | `invalid_compaction_state` | `invalid_compaction_state` | `invalid_compaction_state` |

Restoring bridge state is deliberately independent of the target rule: switching a compacted third-party session to a native model continues at summary level, and that model's own later V1 or V2 compact turns stay native. Opaque native state is different: only an explicit `passthrough` rule permits it to continue. The rule is an administrator declaration, not proof of lineage; the plugin cannot determine which provider created an opaque item, so every explicit passthrough pattern must stay within a provider/model compatibility domain. CPA's `on_no_match: passthrough` fallback does not make an unmatched target trusted for opaque state.

Scalar, object, `null`, and empty `input`, plus a request without an `input` array, cannot carry compaction items and are forwarded untouched.

## Failure contract

Two stable codes with different retry semantics. The Codex client treats HTTP 400 as a terminal invalid request and retries 5xx, so a state error must leave the transport as a 400 rather than an in-band stream failure.

`invalid_compaction_state` is deterministic and non-retryable. Every path rejects with HTTP 400 and an OpenAI error body of type `invalid_request_error` before any upstream call, and V2 rejects out-of-band so no compact stream opens:

```json
{
  "error": {
    "message": "native compaction state cannot continue on a bridged model; switch back to the model that created it or start a new session",
    "type": "invalid_request_error",
    "code": "invalid_compaction_state"
  }
}
```

The four state messages are fixed: a native item on a bridged target uses the message above; an unmatched target uses `native compaction state has no matching passthrough rule; add a passthrough rule for a compact-capable model or start a new session`; a request that mixes both kinds uses `request mixes bridged and native compaction state; start a new session`; and corrupt bridge state uses `bridged compaction state has no summary text; start a new session`.

`compact_bridge_failed` stays the retryable runtime code for Summary Model generation, network, and upstream failures, and for a generated summary that is empty or whitespace-only:

- V1 returns HTTP 502 with a standard OpenAI error body and code `compact_bridge_failed`.
- V2 emits `response.failed` with code `compact_bridge_failed`, then closes without `response.completed`.

A blank generated summary never becomes a compaction item; the facade sends no partial compact result at all.

Failure messages are stable and sanitized; they do not expose upstream credentials or raw provider bodies.
