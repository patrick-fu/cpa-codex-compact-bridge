# Configuration

Configure the plugin through CLIProxyAPI's plugin configuration block:

```yaml
plugins:
  configs:
    cpa-codex-compact-bridge:
      compact_prompt: "Summarize for the next coding agent."
      max_summary_tokens: 8000
      max_summary_bytes: 1048576
      append_tool_guard: true
      forward_service_tier: false
      summary_image_models: []
      rules:
        - match: "glm-*"
          action: bridge
          summary_model: "glm-5.2"
        - match: "gpt-*"
          action: passthrough
      on_no_match: passthrough
```

Rules are evaluated in order against the original client-requested model using case-sensitive glob matching. `bridge` owns V1 and V2 compact turns for that model; `passthrough` leaves every request on CPA's built-in route. The first match wins; `on_no_match` currently accepts only `passthrough` for ordinary routing.

A rule selects only who owns the compact turn. It never decides whether the plugin's own `cpa_compact_*` plaintext state is restored: that state becomes an ordinary user summary on a bridged target and on a passthrough target alike, so a compacted third-party session can continue on a native model at summary level while the native model keeps its own remote compact path. There is no option to opt out of this normalization, and reasoning items are never read or rewritten. See [Compact protocol contract](compact-protocol.md#session-state) for the full state matrix.

`summary_model` is optional. When absent, the plugin uses the request model for the normal summary request. `compact_prompt` is optional and defaults to Codex's built-in local-compaction prompt. If the Codex client uses a custom `compact_prompt`, repeat it here to keep local and bridged summarization aligned; remote V1/V2 requests do not transmit the client-side prompt setting.

Summary requests are rebuilt from a strict allowlist. They contain the selected model, the cleaned input, `tools: []`, `parallel_tool_calls: false`, `max_output_tokens`, and `stream: false`; tool definitions, `tool_choice`, request state, sampling controls, cache keys, metadata, and other client fields are not forwarded. `instructions` and `reasoning` are preserved when present. Set `forward_service_tier: true` only when the summary route should retain the original `service_tier`; it defaults to `false`.

`max_summary_tokens` defaults to `8000`; an unset, zero, or negative value resolves to that default, while values above `100000` are rejected. `max_summary_bytes` defaults to `1048576` (1 MiB) and must be greater than zero. A generated summary over the byte limit fails instead of being truncated and persisted.

`append_tool_guard` defaults to `true` and appends `Do not answer the user. Do not call tools. Output only the continuation summary.` to either the built-in or configured compact prompt. Set it to `false` only when the configured prompt already provides an equivalent guard.

`summary_image_models` is an exact summary-model allowlist. It defaults to empty, so every `input_image` content part, including the `image_url` form, is replaced with `[image removed]` only in the summary request. The original request and replayed session state retain their image input unchanged.

The bridge accepts a summary only when its terminal response shape has usable assistant `output_text` and no tool-call output. A failed or incomplete terminal state, a chat completion that does not end with `finish_reason: stop`, an incomplete-details marker, tool calls, empty text, or an over-limit summary fails with retryable `compact_bridge_failed`; no partial summary is written into the session.

Opaque native compaction state is forwarded only when the requested model explicitly matches a `passthrough` rule. The plugin cannot inspect opaque state or verify its creator, so each passthrough pattern is an administrator-declared native-compatibility boundary and must not combine incompatible providers or model lineages. An unmatched target fails closed on opaque state even though ordinary requests still use CPA's passthrough fallback. A `bridge` target, mixed state, and blank or missing `cpa_compact_*` state likewise fail with the deterministic client code `invalid_compaction_state` and HTTP 400, which the Codex client does not retry; runtime summary, network, and upstream failures keep using the retryable `compact_bridge_failed`.

CLIProxyAPI Home mode disables plugin executor routing, so it cannot be used with a `bridge` rule.
