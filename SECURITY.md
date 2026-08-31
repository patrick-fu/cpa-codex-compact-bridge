# Security policy

## Supported versions

Security fixes target the current stable **v0.1.3** release. CI builds
linux/amd64 against the CLIProxyAPI **v7.2.120** SDK and runs the real
integration suite against exact CLIProxyAPI **v7.2.125** source. Other
CLIProxyAPI versions, operating systems, and CPU architectures are not verified
and may receive no fixes.

## Reporting a vulnerability

Please report security issues privately rather than in a public issue.

- Open a private GitHub security advisory:
  https://github.com/patrick-fu/cpa-codex-compact-bridge/security/advisories/new

Include the CLIProxyAPI version, OS/architecture, your `rules` configuration
(with secrets redacted), and a description of the impact. You should receive an
initial response within a few days.

Do not open a public GitHub issue for security vulnerabilities.

## Important security notes

This plugin is a best-effort interoperability layer, **not** a security
boundary. Read the following before deploying it.

### `encrypted_content` is not encrypted

In V1 and V2, the bridged summary is placed in the `encrypted_content` field of the
compaction item. Despite the field name, this value is **plaintext**. It is a
compatibility marker (the item `id` begins with `cpa_compact_`) so the plugin
can recognize its own state on later turns. It provides **no** confidentiality,
integrity, or authenticity, and it is **not** provider-verifiable native
compaction state. Treat its contents as visible to anyone who can read the
persisted Codex session.

### Your summary provider receives the conversation window

When a model matches a `bridge` rule, the plugin sends the conversation window
needed for compaction to the configured summary model (or the bridged model
itself) through CLIProxyAPI. The resulting summary is returned to Codex, but
the summary provider necessarily sees the source material it summarizes. Make
sure that is acceptable under your provider's terms before enabling a `bridge`
rule.

### You are responsible for upstream compliance

The plugin does not authenticate or vet upstream providers. You are responsible
for ensuring your use of any provider, account, API key, rate limit, and quota
complies with the applicable terms of service. This project provides no
guarantee of legal, regulatory, or contractual compliance.

### Fail-closed behavior

Deterministic compaction-state errors fail with HTTP 400 and the stable,
non-retryable code `invalid_compaction_state` before any upstream call. This
includes opaque state on a `bridge` or unmatched target, mixed native/bridge
state, and blank plugin state. Runtime summary, network, and upstream failures
keep the retryable code `compact_bridge_failed`; a blank generated summary is
never persisted.

An explicit `passthrough` rule is an administrator declaration that its target
can continue opaque native compaction state. The plugin cannot inspect that
state or verify which provider created it. Keep every passthrough pattern within
a compatible provider/model lineage; a bad explicit rule can still forward
opaque state to an incompatible upstream.

### No affiliation

This project is independent community software. It is not affiliated with,
endorsed by, or certified by OpenAI or by CLIProxyAPI's maintainers.
