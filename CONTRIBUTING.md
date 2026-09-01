# Contributing

Thanks for your interest in contributing to cpa-codex-compact-bridge. This is a
small, focused plugin, so these guidelines are intentionally short.

## Prerequisites

- Go 1.26.5 (match `plugin/go.mod`).
- CGO and a C compiler (`gcc` on Linux, `clang` on macOS) to build the c-shared
  library.
- CLIProxyAPI v7.2.125 available for the integration suite. The plugin module
  imports the v7.2.120 SDK, while CI verifies runtime integration against the
  exact v7.2.125 source.

## Build

Build the dynamic library for your platform:

```bash
cd plugin
go build -buildmode=c-shared -o cpa-codex-compact-bridge.<ext> .
```

Use `.dylib` on macOS, `.so` on Linux, `.dll` on Windows. The dynamic library
basename (without extension) is the plugin ID, so keep it exactly
`cpa-codex-compact-bridge`.

## Test

Plugin unit tests and vet:

```bash
(cd plugin && go test ./... && go vet ./...)
```

Integration tests build a real CLIProxyAPI instance and the c-shared plugin and
exercise V1, V2 HTTP/SSE, replay normalization, and a WebSocket V2 release
gate:

```bash
CPA_SOURCE_DIR=/path/to/CLIProxyAPI (cd integration && go test ./... -count=1)
```

The full CPA integration suite is run on linux/amd64. Release CI also builds
and ABI-smoke-tests linux/amd64 plus macOS arm64 and amd64 libraries; macOS does
not run the full CPA integration suite. Windows is not covered by CI.

## Project layout

- `plugin/` — the c-shared plugin source: config, routing, V1/V2 compact, replay
  normalization, and host callbacks.
- `integration/` — end-to-end tests against a real CPA instance and a fake
  upstream.
- `docs/` — protocol contract, configuration, and architecture decision records.
- `testdata/` — request and SSE fixtures.

Before changing bridge behavior, read `CONTEXT.md` (domain glossary) and
`docs/compact-protocol.md` (protocol contract).

`AGENTS.md` and `docs/agents/` document maintainer automation workflows. They
do not change the user-facing protocol contract or configuration.

## Coding conventions

- Match the existing style and package layout.
- Preserve the fail-closed contract: never forward Codex compaction protocol
  items to an upstream that did not produce them.
- Add or update fixtures under `testdata/` for any new protocol path.
- Keep error codes stable (for example `compact_bridge_failed`); they are part
  of the public contract in `docs/compact-protocol.md`.

## Commits and pull requests

- Commit subject: a single capitalized English sentence, no `fix:` / `feat:`
  style prefixes. Use a bullet-point body for non-trivial changes.
- Open a pull request against `main`. Describe what changed and why.
- If a change alters the public protocol contract or supported CPA version,
  update `docs/compact-protocol.md`, `docs/configuration.md`, and `CONTEXT.md`,
  and call it out in the PR description.

## Maintainer releases

Prebuilt binaries will be published on [GitHub Releases](https://github.com/patrick-fu/cpa-codex-compact-bridge/releases). The release workflow:

1. **Prepare and tag** the commit as `vX.Y.Z`: first update the default
   `pluginVersion` in `plugin/main.go` to `X.Y.Z` so source builds stay
   accurate, then push the new tag. The workflow validates the tag and injects
   the same version into release metadata. Do not retarget or reuse a tag.
2. **CI** builds `c-shared` archives for linux/amd64, macOS/arm64, and
   macOS/amd64. Every archive has a single root-level shared library; the final
   publish job writes all three archive hashes to one sha256sum-format
   `checksums.txt`. Full CPA integration remains linux/amd64-only; macOS runs
   build and ABI smoke checks. Windows is not covered.
3. **GitHub Release**: create or refresh the release from the tag with all
   three zips and `checksums.txt`.
4. **CPA Plugin Store**: the plugin is listed through Store PR #80. Store
   releases read the latest GitHub Release automatically, so normal releases do
   not require a per-version `registry.json` PR.

## Scope

This plugin intentionally does one thing: bridge Codex remote compaction for
models that lack native compact support, while leaving native compact models on
a passthrough path. Proposals that expand scope (new protocols, additional
transports) are welcome as issues, but please discuss them before large
implementation work.
