# Security policy

## Supported versions

Security fixes target the current `v0.1.0` source baseline built and tested against
CLIProxyAPI **v7.2.120** on **linux/amd64**. Other CLIProxyAPI versions,
operating systems, and CPU architectures are not verified and may receive no
fixes.

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

In V2, the bridged summary is placed in the `encrypted_content` field of the
compaction item. Despite the field name, this value is **plaintext**. It is a
compatibility marker (the item `id` begins with `cpa_compact_`) so the plugin
can recognize its own state on later turns. It provides **no** confidentiality,
integrity, or authenticity, and it is **not** provider-verifiable native
compaction state. Treat its contents as visible to anyone who can read the
persisted Codex session.

### Your provider receives compressed context

When a model matches a `bridge` rule, the plugin summarizes your conversation
and sends that summary to the configured summary model (or the bridged model
itself) through CLIProxyAPI. That third-party provider therefore receives a
compressed version of your conversation context. Make sure that is acceptable
under your provider's terms before enabling a `bridge` rule.

### You are responsible for upstream compliance

The plugin does not authenticate or vet upstream providers. You are responsible
for ensuring your use of any provider, account, API key, rate limit, and quota
complies with the applicable terms of service. This project provides no
guarantee of legal, regulatory, or contractual compliance.

### Fail-closed behavior

If summary generation fails or produces no usable text, the plugin fails
closed. It returns a stable error (`compact_bridge_failed`) instead of
forwarding Codex-specific compaction protocol items to an upstream that cannot
handle them, which avoids sending malformed compact requests to a provider.

### No affiliation

This project is independent community software. It is not affiliated with,
endorsed by, or certified by OpenAI or by CLIProxyAPI's maintainers.
