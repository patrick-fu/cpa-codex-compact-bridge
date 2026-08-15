# CPA Plugin Store publication readiness

**Checked:** 2026-08-15

**Project:** [`patrick-fu/cpa-codex-compact-bridge`](https://github.com/patrick-fu/cpa-codex-compact-bridge)

**Store snapshot:** [`router-for-me/CLIProxyAPI-Plugins-Store@c98d051`](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/commit/c98d051f6a9e1761833dee4aedfc289ffeb3c08d)

**Method:** read-only verification against the public repositories, current GitHub metadata, CPA installer source, and the official plugin development guide.

## Current answer

The repository is public, the linux/amd64 [v0.1.2 Release](https://github.com/patrick-fu/cpa-codex-compact-bridge/releases/tag/v0.1.2) is published and independently verified, and the exact Release library has passed a real-provider test-cpa gate. It is ready for a CPA Plugin Store `registry.json` submission.

The remaining store action is a small pull request that adds one entry to `registry.json`.

## Open-source repository status

| Check | Status | Evidence / action |
| --- | --- | --- |
| Public repository | Ready | GitHub reports `PUBLIC`: [repository](https://github.com/patrick-fu/cpa-codex-compact-bridge). |
| License | Ready | MIT [`LICENSE`](../../LICENSE) and [`THIRD_PARTY_NOTICES`](../../THIRD_PARTY_NOTICES). |
| Contribution policy | Ready | [`CONTRIBUTING.md`](../../CONTRIBUTING.md), [`CODE_OF_CONDUCT.md`](../../CODE_OF_CONDUCT.md), issue forms. |
| Security reporting | Ready | Private advisory route documented in [`SECURITY.md`](../../SECURITY.md). |
| English / Chinese overview | Ready | [`README.md`](../../README.md) and [`README.zh-CN.md`](../../README.zh-CN.md) share the same problem-goal-solution structure. |
| Build and integration CI | Ready | [`Build Linux Plugin`](https://github.com/patrick-fu/cpa-codex-compact-bridge/actions/workflows/build-linux-plugin.yml) tests, vets, builds linux/amd64, and integrates with pinned CPA v7.2.125. |
| Required CI enforcement | Optional gap | CI runs on PRs and `main` pushes, but GitHub branch protection currently has no required status check. Require `Test and build linux/amd64 plugin` before accepting external PRs. |
| Stable release | Ready | [v0.1.2](https://github.com/patrick-fu/cpa-codex-compact-bridge/releases/tag/v0.1.2) contains the required linux/amd64 zip and `checksums.txt`. |
| Multi-platform release | Limited by choice | Current release workflow publishes only linux/amd64. The store permits this, but other platforms will not be installable. |

## What the official store requires

The [official store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store) is a lightweight registry. Its [README](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/README.md) requires:

1. A public GitHub repository URL in the exact form `https://github.com/{owner}/{repo}`.
2. A latest GitHub Release tagged `v<version>` where `<version>` is dotted numeric.
3. One zip for every supported platform, named:

   ```text
   <plugin-id>_<version>_<goos>_<goarch>.zip
   ```

4. A `checksums.txt` asset in sha256sum format.
5. Exactly one correctly named dynamic library at the zip root:
   - Linux / FreeBSD: `<plugin-id>.so`
   - macOS: `<plugin-id>.dylib`
   - Windows: `<plugin-id>.dll`
6. A PR that normally changes only `registry.json` and includes the repository URL, release tag, release-asset evidence, and a short capability description.

CPA's [plugin store installer](https://github.com/router-for-me/CLIProxyAPI/tree/main/internal/pluginstore) fetches the repository's **latest GitHub Release**, selects the exact platform asset, verifies its SHA-256 from `checksums.txt`, validates the archive layout, and installs a versioned library. A Git tag without a GitHub Release is therefore insufficient.

The registry itself requires `id`, `name`, `description`, `author`, and `repository`. `homepage`, `license`, `tags`, `logo`, and a display-fallback `version` are optional. The store currently has no `.github` workflow; review is manual. A recent comparable submission, [Codex Local Compact PR #71](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/71), was reviewed for source, dependencies, and release binaries before merge.

## Project-to-store alignment

| Requirement | Status | Notes |
| --- | --- | --- |
| Stable plugin ID | Ready | `cpa-codex-compact-bridge`; valid and unique in the current registry. |
| Runtime registration metadata | Ready | Name, version, author, repository, ABI schema, and declared capabilities are returned by `plugin.register`. |
| Store-compatible packaging workflow | Ready | [`.github/workflows/release.yml`](../../.github/workflows/release.yml) produces `cpa-codex-compact-bridge_<version>_linux_amd64.zip`, one root-level `.so`, and `checksums.txt`. |
| Test gate for release | Ready | Unit test, vet, and real CPA integration are part of the tag workflow; CPA is pinned to v7.2.125. |
| Version source | Ready for prep CI | `pluginVersion` is `0.1.2`, matching the tested `0.1.2-rc.<sha>` candidate line. The release workflow independently verifies source metadata against the tag. |
| GitHub Release assets | Ready | Zip SHA-256 `6052bfa15e10323a742ab78318b8035878a57a85d4aa3c831b5d391a39a2f8e5`; root library SHA-256 `a2821f035f9b2dbc3f747cecd9a47c454e836559111682909673f75b688d8321`. |
| Registry entry | Ready to submit | No existing ID conflict in the 2026-08-15 official registry snapshot. |
| Logo | Optional | No logo is required. Add one only if useful; do not block publication on it. |

## Positioning next to the existing Codex Local Compact plugin

The store already lists `codex-localcompact`, described there as emulating Codex remote compaction V2 for configured DeepSeek and OpenAI-compatible models. This is not an ID conflict, but the submission should state this project's distinct scope:

- ordered **per-model bridge and native passthrough** rules behind one CPA endpoint;
- both legacy V1 and current V2 remote transports;
- marked replay normalization for later continuations;
- fail-closed handling of foreign opaque compaction state.

Do not claim that native compact state is portable across providers or that this plugin implements Codex local compact.

## Proposed registry entry

Proposed submission:

```json
{
  "id": "cpa-codex-compact-bridge",
  "name": "Codex Compact Bridge",
  "description": "Bridges Codex remote compaction V1 and V2 for configured third-party models while preserving native compaction passthrough per model.",
  "author": "patrick-fu",
  "repository": "https://github.com/patrick-fu/cpa-codex-compact-bridge",
  "homepage": "https://github.com/patrick-fu/cpa-codex-compact-bridge",
  "license": "MIT",
  "tags": ["Executor", "Interceptor", "Codex", "Compaction"]
}
```

`version` can be omitted because CPA derives the install version from the latest release tag. Add it only as an intentional display fallback.

## Minimal publication sequence

1. Open a store PR changing only `registry.json`, using the proposed entry and linking the verified v0.1.2 Release.
2. In the PR body, explicitly distinguish per-model passthrough + V1/V2 + replay from the existing V2-focused store plugin.
3. After merge, install once through the official store source in test-cpa and confirm version `0.1.2`.

Release v0.1.2 was published and installed only in test-cpa. No production service or GitHub protection setting was changed.

## Primary sources

- [CLIProxyAPI Plugins Store README](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/README.md)
- [Current registry.json](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/registry.json)
- [CLIProxyAPI plugin store installer](https://github.com/router-for-me/CLIProxyAPI/tree/main/internal/pluginstore)
- [CLIProxyAPI official plugin development guide](https://github.com/router-for-me/CLIProxyAPIDocs/blob/main/docs/en/plugin/development.md)
- [Comparable merged store submission #71](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/71)
