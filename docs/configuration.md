# Configuration

Configure the plugin through CLIProxyAPI's plugin configuration block:

```yaml
plugins:
  configs:
    cpa-codex-compact-bridge:
      compact_prompt: "Summarize for the next coding agent."
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

Opaque native compaction state is forwarded only when the requested model explicitly matches a `passthrough` rule. The plugin cannot inspect opaque state or verify its creator, so each passthrough pattern is an administrator-declared native-compatibility boundary and must not combine incompatible providers or model lineages. An unmatched target fails closed on opaque state even though ordinary requests still use CPA's passthrough fallback. A `bridge` target, mixed state, and blank or missing `cpa_compact_*` state likewise fail with the deterministic client code `invalid_compaction_state` and HTTP 400, which the Codex client does not retry; runtime summary, network, and upstream failures keep using the retryable `compact_bridge_failed`.

CLIProxyAPI Home mode disables plugin executor routing, so it cannot be used with a `bridge` rule.
