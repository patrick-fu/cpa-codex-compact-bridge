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
        - match: "gpt-*-codex*"
          action: passthrough
      on_no_match: passthrough
```

Rules are evaluated in order against the original client-requested model using case-sensitive glob matching. `bridge` owns V1 and V2 compact turns for that model; ordinary streaming turns remain on CPA's built-in route and are rewritten only if their normalized history contains `cpa_compact_*` state. `passthrough` leaves every request on CPA's built-in route. The first match wins; `on_no_match` currently accepts only `passthrough`.

`summary_model` is optional. When absent, the plugin uses the request model for the normal summary request. `compact_prompt` is optional and defaults to Codex's built-in local-compaction prompt. If the Codex client uses a custom `compact_prompt`, repeat it here to keep local and bridged summarization aligned; remote V1/V2 requests do not transmit the client-side prompt setting.

A matching `bridge` rule always fails closed for opaque native compact state: only the plugin's own `cpa_compact_*` plaintext items are rewritten to regular user messages before a third-party upstream receives them.

CLIProxyAPI Home mode disables plugin executor routing, so it cannot be used with a `bridge` rule.
