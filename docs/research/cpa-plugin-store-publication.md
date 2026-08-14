# CPA Plugin Store publication readiness

**Checked:** 2026-08-14

**Project:** [`patrick-fu/cpa-codex-compact-bridge`](https://github.com/patrick-fu/cpa-codex-compact-bridge)

**Store snapshot:** [`router-for-me/CLIProxyAPI-Plugins-Store@c98d051`](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/commit/c98d051f6a9e1761833dee4aedfc289ffeb3c08d)

**Method:** read-only verification against the public repositories, current GitHub metadata, CPA installer source, and the official plugin development guide.

## Current answer

The repository is public and already has the normal open-source project files and a working build/integration CI. It is **not ready to submit to the CPA Plugin Store yet** because it has no stable GitHub Release containing the installable zip and `checksums.txt`.

The release workflow is present and uses the correct store asset layout for linux/amd64. After publishing and verifying a new release, the remaining store action is a small pull request that adds one entry to `registry.json`.

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
| Stable release | **Blocked** | GitHub has a `v0.1.0` tag but no GitHub Release or assets. Do not retarget the old tag. |
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
| Version source | **Needs release preparation** | `pluginVersion` is still `0.1.0`; the tested CI candidates use `0.1.2-rc.<sha>`. Choose the next stable version and update `pluginVersion` before tagging. |
| GitHub Release assets | **Missing** | No stable Release exists. This is the store submission blocker. |
| Registry entry | **Missing by design** | Submit only after the release can be installed and checksum-verified. |
| Logo | Optional | No logo is required. Add one only if useful; do not block publication on it. |

## Positioning next to the existing Codex Local Compact plugin

The store already lists `codex-localcompact`, described there as emulating Codex remote compaction V2 for configured DeepSeek and OpenAI-compatible models. This is not an ID conflict, but the submission should state this project's distinct scope:

- ordered **per-model bridge and native passthrough** rules behind one CPA endpoint;
- both legacy V1 and current V2 remote transports;
- marked replay normalization for later continuations;
- fail-closed handling of foreign opaque compaction state.

Do not claim that native compact state is portable across providers or that this plugin implements Codex local compact.

## Proposed registry entry

Add this only after the stable release exists:

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

1. Choose the first stable release from current `main`. Given the existing `v0.1.0` tag and tested `0.1.2-rc.*` candidates, `v0.1.2` is the least surprising option; this remains a maintainer version decision.
2. Update `pluginVersion` to the chosen version and merge through the required build/integration gate.
3. Tag that exact `main` commit once. Do not move or reuse `v0.1.0`.
4. Let the Release workflow publish the linux/amd64 zip and `checksums.txt`.
5. Independently verify the archive name, checksum, root layout, embedded version, and installation through a non-production CPA instance.
6. Open a store PR changing only `registry.json`, using the proposed entry and linking the verified Release.
7. In the PR body, explicitly distinguish per-model passthrough + V1/V2 + replay from the existing V2-focused store plugin.

No Release, store PR, store registry edit, deployment, or GitHub protection change was performed during this audit.

## Primary sources

- [CLIProxyAPI Plugins Store README](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/README.md)
- [Current registry.json](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/registry.json)
- [CLIProxyAPI plugin store installer](https://github.com/router-for-me/CLIProxyAPI/tree/main/internal/pluginstore)
- [CLIProxyAPI official plugin development guide](https://github.com/router-for-me/CLIProxyAPIDocs/blob/main/docs/en/plugin/development.md)
- [Comparable merged store submission #71](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/71)
