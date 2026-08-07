# Configuration

Configure the plugin through CLIProxyAPI's plugin configuration block:

```yaml
plugins:
  configs:
    cpa-codex-compact-bridge:
      rules:
        - match: "glm-*"
          action: bridge
          summary_model: "glm-5.2"
        - match: "gpt-*-codex*"
          action: passthrough
      on_no_match: passthrough
```

Rules are evaluated in order against the original client-requested model using case-sensitive glob matching. `bridge` owns V1 and V2 compact turns for that model; ordinary streaming turns remain on CPA's built-in route and are rewritten only if their normalized history contains `cpa_compact_*` state. `passthrough` leaves every request on CPA's built-in route. The first match wins; `on_no_match` currently accepts only `passthrough`.

`summary_model` is optional. When absent, the plugin uses the request model for the normal summary request. A matching `bridge` rule always fails closed for opaque native compact state: only the plugin's own `cpa_compact_*` plaintext items are rewritten to regular user messages before a third-party upstream receives them.

CLIProxyAPI Home mode disables plugin executor routing, so it cannot be used with a `bridge` rule.
