# Compact protocol contract

This contract applies only to a request matched by a `bridge` Bridge Rule. Native Compact Routes are unchanged.

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

## Replay normalization

On a later ordinary request matched by the same Bridge Rule, the request interceptor replaces every `compaction` item whose `id` begins `cpa_compact_` with one ordinary user summary message containing the stored plaintext. CPA then uses its ordinary provider route.

An unmarked `compaction` item is not presumed to be plaintext. The facade fails closed rather than sending an opaque native compact state to a third-party provider.

## Failure contract

If summary generation fails or produces no usable text, the facade sends no partial compact result.

- V1 returns HTTP 502 with a standard OpenAI error body and code `compact_bridge_failed`.
- V2 emits `response.failed` with code `compact_bridge_failed`, then closes without `response.completed`.

Failure messages are stable and sanitized; they do not expose upstream credentials or raw provider bodies.
