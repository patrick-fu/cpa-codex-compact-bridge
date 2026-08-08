# Contributing

Thanks for your interest in contributing to cpa-codex-compact-bridge. This is a
small, focused plugin, so these guidelines are intentionally short.

## Prerequisites

- Go 1.26.5 (match `plugin/go.mod`).
- CGO and a C compiler (`gcc` on Linux, `clang` on macOS) to build the c-shared
  library.
- CLIProxyAPI v7.2.120 available for the integration suite (the plugin is built
  against its v7.2.120 SDK).

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

The integration suite is run on linux/amd64. Other platforms are not covered by
CI and are considered experimental.

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
2. **CI** builds `c-shared` for linux/amd64, packages it as
   `cpa-codex-compact-bridge_X.Y.Z_linux_amd64.zip` (single root-level `.so`),
   and generates a `checksums.txt` in sha256sum format.
3. **GitHub Release**: create a release from the tag with the zip and
   `checksums.txt`. Other platforms are experimental and not included in the
   release matrix.
4. **CPA Plugin Store submission** (separate step, not part of the release
   workflow): open a PR against
   [CLIProxyAPI-Plugins-Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store)
   adding one entry to `registry.json`. Include the release tag, evidence that
   the zip and `checksums.txt` exist, and a short capability summary. See the
   [research report](docs/research/cpa-plugin-store-publication.md) for
   submission details and review expectations.

## Scope

This plugin intentionally does one thing: bridge Codex remote compaction for
models that lack native compact support, while leaving native compact models on
a passthrough path. Proposals that expand scope (new protocols, additional
transports) are welcome as issues, but please discuss them before large
implementation work.
