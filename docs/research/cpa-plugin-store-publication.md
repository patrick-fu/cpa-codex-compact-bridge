# Research: Publishing cpa-codex-compact-bridge to the CPA Plugin Store

**Date:** 2026-08-08
**Subject:** How to publish this CLIProxyAPI (CPA) native plugin to the official/community CPA plugin store.
**Scope:** Read-only external research against first-party sources only. No code changes were made.

## TL;DR

A real, actively-maintained **official plugin store exists**: the [`CLIProxyAPI-Plugins-Store`](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store) repository. It is a lightweight registry that holds a single [`registry.json`](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/registry.json). CPA itself fetches that file by default ([`DefaultRegistryURL`](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/registry.go)) and can install plugins from it directly ([`POST /plugin-store/{pluginID}/install`](https://github.com/router-for-me/CLIProxyAPIDocs/blob/main/docs/en/plugin/development.md)). Publication is a two-part job: (1) publish versioned binary zip + checksum assets in your own GitHub Releases, and (2) open a PR adding one object to `registry.json`. There is **no manifest file** to submit, **no code signing**, and **no automated CI gate** on the store repo — review is human.

This project's current blockers are concrete: it ships **no GitHub Release assets** (tag `v0.1.0` exists but has no release/zip/checksums), its CI only **builds linux/amd64** and never publishes, and it is **not in `registry.json`**. The README also explicitly declares "Source-first Release. No prebuilt binaries," which is incompatible with the store's release-asset requirement.

## 1. Store entry point

The official store is the GitHub repository [router-for-me/CLIProxyAPI-Plugins-Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store). It contains only:

- [`LICENSE`](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/LICENSE)
- [`README.md`](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/README.md) — submission rules.
- [`registry.json`](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/registry.json) — the registry itself (39 plugins as of 2026-08-08).

There is **no `.github/` directory and no CI workflow** in the store repo (the `.github` path returns HTTP 404). Validation and review are manual.

CPA consumes it via a hardcoded default:

```go
DefaultRegistryURL = "https://raw.githubusercontent.com/router-for-me/CLIProxyAPI-Plugins-Store/main/registry.json"
```
Source: [`internal/pluginstore/registry.go`](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/registry.go).

The [official development docs](https://github.com/router-for-me/CLIProxyAPIDocs/blob/main/docs/en/plugin/development.md) document the same default and the install endpoint `POST /v0/management/plugin-store/{pluginID}/install`.

**Conclusion:** an official store and publication path exist. There is no separate "manifest upload" portal or marketplace website; a GitHub PR to `registry.json` is the entry point.

## 2. registry.json manifest requirements

From the [store README](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/README.md) and the [validation code](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/registry.go):

```json
{
  "schema_version": 1,
  "plugins": [
    {
      "id": "sample-provider",
      "name": "Sample Provider",
      "description": "Adds sample provider support.",
      "author": "author-name",
      "repository": "https://github.com/owner/cliproxy-sample-provider",
      "logo": "https://raw.githubusercontent.com/owner/cliproxy-sample-provider/main/logo.png",
      "homepage": "https://github.com/owner/cliproxy-sample-provider",
      "license": "MIT",
      "tags": ["provider"]
    }
  ]
}
```

| Field | Required? | Rule |
|---|---|---|
| `schema_version` | Yes | Must be `1`. |
| `id` | Yes | Regex `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`; unique across registry. |
| `name` | Yes | Non-empty. |
| `description` | Yes | Non-empty. |
| `author` | Yes | Non-empty. |
| `repository` | Yes (for github-release type) | Exactly `https://github.com/{owner}/{repo}` — no `.git`, no query/fragment. |
| `version` | Optional | Display fallback only; must **not** start with `v`. |
| `logo` / `homepage` / `license` / `tags` | Optional | Free-form. |

Sources: [README "Registry Format" + "Validation Rules"](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/README.md); [`ValidatePlugin`](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/registry.go) and [`pluginIDPattern`](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/registry.go).

There is **no per-plugin manifest file submitted to the store** — the `Manifest` type in CPA ([`internal/pluginstore/manifest.go`](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/manifest.go)) is a runtime/installer construct, not a publication artifact.

## 3. Binary / version / platform / checksum requirements

The store registry points at your repo; **the actual binaries live in your own GitHub Releases**. From the [README "Release Requirements"](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/README.md):

- The latest release tag defines the version. Tag must be `v<version>` with a dotted numeric version, e.g. `v0.1.0`. A new release = a new version; the registry does not need to change.
- Each release must include **one `checksums.txt` and one zip per supported platform**.
- Asset naming (version **without** the leading `v`):

  ```text
  <id>_<version>_<goos>_<goarch>.zip
  checksums.txt
  ```
  Examples: `sample-provider_0.1.0_linux_amd64.zip`.

- `checksums.txt` must be sha256sum format:

  ```text
  <sha256>  sample-provider_0.1.0_darwin_arm64.zip
  ```

- **Zip layout:** the target dynamic library must be at the zip root — `<id>.dylib` (Darwin), `<id>.so` (Linux/FreeBSD), `<id>.dll` (Windows). Nested libraries, absolute paths, zip-slip paths, mismatched filenames, and multiple libraries are [rejected by the installer](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/install.go).

These rules are enforced at install time by CPA. The installer:

1. Fetches the repo's **latest release** via `https://api.github.com/repos/{owner}/{repo}/releases/latest` ([`FetchLatestRelease`](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/github.go)).
2. Selects the asset named `<id>_<version>_<goos>_<goarch>.zip` plus `checksums.txt` ([`SelectReleaseAssets`](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/github.go)).
3. Downloads both, parses `checksums.txt` ([`ParseChecksums`](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/checksum.go)), and verifies the zip's SHA-256 ([`VerifyChecksum`](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/checksum.go)).
4. Extracts the single root-level library and writes it to `plugins/<goos>/<goarch>/<id>-v<version>.<ext>` ([`InstallArchive`](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/install.go)).

**No code signing, GPG, or signature scheme is required or checked.** The only integrity mechanism is the SHA-256 in `checksums.txt`. (The org also has a [`signature`](https://github.com/router-for-me/CLIProxyAPI/tree/main/internal/signature) package, but the plugin store installer does not use it.)

Supported platforms seen in reference plugin CI: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64, freebsd/amd64. You only strictly need the platform(s) you want installable; the store does not mandate a minimum set, but the installer errors if the user's platform has no matching zip ([`SelectReleaseAssets`](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/github.go)).

## 4. Submission flow and review

Per the [README "Adding A Plugin"](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/README.md):

> Open a pull request that updates only `registry.json` unless documentation also needs clarification. The pull request should include:
> - The plugin's GitHub repository URL.
> - The latest release tag in `v<version>` form.
> - Evidence that the required zip asset and `checksums.txt` exist in the release.
> - A short description of what capability the plugin adds.

Observed practice (confirmed against recent PRs):

- **No automated CI** runs on the store repo. A bot (`copilot-pull-request-reviewer`) leaves an informational review that explicitly ["doesn't count toward merge requirements"](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/68).
- A maintainer (observed: **`luispater`**) reviews and merges. Merges happen the same day for compliant submissions (e.g. [#69](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/69), [#67](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/67), [#66](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/66) all merged 2026-08-07).
- **Security audit is real:** PR [#68](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/68) was **not merged** because the maintainer found the plugin served dynamic/privileged content from an unauthenticated resource route (`/v0/resource/plugins/...`), violating the CPA resource-route boundary. This is the documented review bar: resource pages must be static; privileged logic must sit behind the management-key-protected `/v0/management/...` ([docs](https://github.com/router-for-me/CLIProxyAPIDocs/blob/main/docs/en/plugin/development.md)).

So review is fast but substantive on security/route boundaries.

## 5. Release CI reference (how plugins publish binaries)

There is no store-provided publisher. Each plugin ships its own release workflow. The canonical in-org example is [`cpa-plugin-gemini-cli`](https://github.com/router-for-me/cpa-plugin-gemini-cli)'s [`.github/workflows/build.yml`](https://github.com/router-for-me/cpa-plugin-gemini-cli/blob/main/.github/workflows/build.yml), which:

- Triggers on `push` to `v*` tags.
- Runs `go test` + `go vet`.
- Builds a matrix of `c-shared` libraries per platform (`CGO_ENABLED=1 go build -buildmode=c-shared -ldflags "-s -w"`), deleting the generated `.h`.
- Packages each library with a helper script [`package-release.go`](https://github.com/router-for-me/cpa-plugin-gemini-cli/blob/main/.github/scripts/package-release.go) that zips the single library at the zip root and emits a per-zip `.sha256`.
- In the `release` job: downloads all artifacts, `sort dist/*.sha256 > dist/checksums.txt`, and creates/uploads the GitHub Release with `*.zip` + `checksums.txt`.

This workflow produces exactly the asset names and `checksums.txt` layout the installer expects.

## 6. Gap analysis: cpa-codex-compact-bridge @ main (c47771b)

| Requirement | Status | Evidence |
|---|---|---|
| Public GitHub repo at `https://github.com/{owner}/{repo}` | ✅ Met | [patrick-fu/cpa-codex-compact-bridge](https://github.com/patrick-fu/cpa-codex-compact-bridge) (public). |
| Plugin ID valid & stable | ✅ Met | `cpa-codex-compact-bridge` ([`plugin/main.go:53`](../../plugin/main.go)). Matches id regex; `.so` basename already equals the ID. |
| Registration metadata | ✅ Met | `plugin.register` returns `schema_version`, `Name`, `Version` (`0.1.0`), `Author`, `GitHubRepository` ([`plugin/main.go:166-171`](../../plugin/main.go)). |
| Tag `v<version>` exists | ✅ Met | tag `v0.1.0` exists. |
| GitHub **Release** with zip + checksums | ❌ **Missing** | [`/releases`](https://github.com/patrick-fu/cpa-codex-compact-bridge/releases) returns empty; tag has no release, no `cpa-codex-compact-bridge_0.1.0_linux_amd64.zip`, no `checksums.txt`. |
| CI builds & publishes release assets | ❌ **Missing** | [`.github/workflows/build-linux-plugin.yml`](../../.github/workflows/build-linux-plugin.yml) only builds linux/amd64 into `dist/`, computes a `.sha256` of the raw `.so` (not the zip), and does **not** create a release. It also has `permissions: contents: read`. |
| Multi-platform binaries | ⚠️ Partial | Only linux/amd64 is built/tested. Store doesn't mandate platforms, but each platform you omit is un-installable for that OS/arch. |
| `registry.json` entry | ❌ **Missing** | Not present in [registry.json](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/registry.json). |
| Security/route boundary compliance | ✅ Likely OK | Plugin is an interceptor/executor with no management/resource routes; no dynamic resource page. Low review risk on this axis. |
| Project stance | ⚠️ Policy conflict | README declares "Source-first Release. No prebuilt binaries are distributed." This directly conflicts with the store's mandatory release-asset model. This is a product decision to make, not just a code change. |

The single biggest technical gap is the absence of a release pipeline that produces the store-compatible artifacts. The single biggest non-technical gap is the explicit "no prebuilt binaries" stance in the README.

## 7. Minimal publication steps

Assuming the decision is made to publish prebuilt binaries:

1. **Add a release CI workflow** (model on [gemini-cli `build.yml`](https://github.com/router-for-me/cpa-plugin-gemini-cli/blob/main/.github/workflows/build.yml)): trigger on `v*` tags; test + vet; matrix-build `c-shared` per platform with `-ldflags "-s -w -X github.com/patrick-fu/cpa-codex-compact-bridge/plugin.pluginVersion=<version>"`; package each as a root-level-library zip named `cpa-codex-compact-bridge_<version>_<goos>_<goarch>.zip`; generate `checksums.txt` (sha256sum format) by sorting per-zip checksums; create the GitHub Release with all zips + `checksums.txt`. Set `permissions: contents: write`. Before tagging a later version, explicitly update the default `pluginVersion` Go variable so source-build metadata matches the release tag; the release workflow injects the same version into its published binary.
2. **Tag and push** `v0.1.0` (or a new `v0.1.1`) to trigger the release. Verify the release contains, at minimum, `cpa-codex-compact-bridge_0.1.0_linux_amd64.zip` and `checksums.txt`, and that the zip root contains exactly `cpa-codex-compact-bridge.so`.
3. **Update the README** to remove the "no prebuilt binaries" declaration for published platforms (or scope it to unsupported platforms only), since the store requires downloadable binaries.
4. **Open a PR** against [`router-for-me/CLIProxyAPI-Plugins-Store`](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store) adding one object to `registry.json`:

   ```json
   {
     "id": "cpa-codex-compact-bridge",
     "name": "Codex Compact Bridge",
     "description": "Lets Codex keep remote compaction enabled while routing non-native-compact models through an ordinary summary call.",
     "author": "patrick-fu",
     "version": "0.1.0",
     "repository": "https://github.com/patrick-fu/cpa-codex-compact-bridge",
     "homepage": "https://github.com/patrick-fu/cpa-codex-compact-bridge",
     "license": "MIT",
     "tags": ["Interceptor", "Codex", "Compaction"]
   }
   ```

   In the PR body include: repo URL, release tag `v0.1.0`, explicit evidence (asset names + that `checksums.txt` exists), and a one-line capability summary — matching the [README's requested PR contents](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/README.md) and observed merged PRs like [#69](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/69).
5. **Respond to maintainer review** if any. Given this plugin exposes no management/resource routes, the security-route audit that blocked [#68](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/68) should not apply.

No code signing, no manifest file upload, and no store-side CI are involved.

## Sources (all first-party)

- Store repo: https://github.com/router-for-me/CLIProxyAPI-Plugins-Store
- Store README (rules): https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/README.md
- registry.json: https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/main/registry.json
- CPA installer code:
  - https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/registry.go
  - https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/install.go
  - https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/github.go
  - https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/manifest.go
  - https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/pluginstore/checksum.go
- CPA official docs (plugin development + store format + install endpoint): https://github.com/router-for-me/CLIProxyAPIDocs/blob/main/docs/en/plugin/development.md
- Reference release CI: https://github.com/router-for-me/cpa-plugin-gemini-cli/blob/main/.github/workflows/build.yml
- Reference packaging script: https://github.com/router-for-me/cpa-plugin-gemini-cli/blob/main/.github/scripts/package-release.go
- Review evidence (merged): https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/69
- Review evidence (blocked on security): https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/68
