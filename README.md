# cpa-codex-compact-bridge

A [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) native plugin that lets Codex keep remote compaction enabled, while routing models that **do not** support the Codex compaction protocol through an ordinary summary call.

> ⚠️ **Not affiliated with OpenAI or CLIProxyAPI.** This is independent community software. It is not endorsed by or certified by OpenAI or CLIProxyAPI's maintainers. "Codex" and "OpenAI" are trademarks of their respective owners.

## How it works

Codex remote compaction has two protocol shapes, plus a replay step. The bridge handles each:

- **V1** — intercepts the non-streaming `POST /v1/responses/compact` endpoint, summarizes the context through an ordinary model request, and returns a single assistant summary message.
- **V2** — detects a streaming `/v1/responses` request whose final input is a `compaction_trigger`, summarizes through an ordinary model request, and returns the `compaction` SSE item plus `response.completed` that Codex expects.
- **Replay** — on later ordinary turns, converts the plugin's own `cpa_compact_*` plaintext state back into a normal user summary so context survives Responses→Chat conversion.
- **Passthrough** — models you configure as native-compact-capable are forwarded unchanged.

The `encrypted_content` field of the bridged V2 compaction item holds the **plaintext** summary. It is a compatibility marker (the `cpa_compact_*` ID), not encryption and not a trust, integrity, or confidentiality boundary. See [Security](#security).

## Status

- **Confirmed:** CLIProxyAPI **v7.2.120**, **linux/amd64** — build and integration CI.
- **Experimental / unverified:** other CLIProxyAPI versions, macOS/Windows builds, and other CPU architectures. Build the library yourself for your platform; behavior is not guaranteed there.
- **Source-first release.** No prebuilt binaries are distributed. Build from source.

## Disclaimers

- The plugin does not vet or authenticate upstream providers. **You are responsible** for ensuring your use of any provider, account, API key, rate limit, and quota complies with the applicable terms of service. This project provides no guarantee of legal, regulatory, or contractual compliance.
- When a model matches a `bridge` rule, the configured summary provider receives a **compressed/summarized version of your conversation context**.
- In V2 the plaintext summary is placed in `encrypted_content`; it is not encrypted (see above).

## Installation

Build a dynamic library matching your CPA runtime platform:

```bash
cd plugin
go build -buildmode=c-shared -o cpa-codex-compact-bridge.so .
```

Place the artifact in CPA's plugin discovery directory. The tested target is Linux
amd64:

```text
<plugin-dir>/linux/amd64/cpa-codex-compact-bridge.so
```

Use `.dylib` on macOS or `.dll` on Windows and replace the directory with
`<GOOS>/<GOARCH>`; those platforms are experimental. The dynamic library
basename (without extension) is the plugin ID. The plugin is compiled against
the CLIProxyAPI `v7.2.120` SDK.

## Configuration

```yaml
plugins:
  enabled: true
  dir: /absolute/path/to/plugins
  configs:
    cpa-codex-compact-bridge:
      enabled: true
      priority: 100
      rules:
        # Third-party models: bridge
        - match: "glm-*"
          action: bridge
          summary_model: "glm-5.2"
        # Native remote-compact models: keep CPA's original path
        - match: "gpt-*-codex*"
          action: passthrough
      on_no_match: passthrough
```

Rules are evaluated in order against the original client-requested model using **case-sensitive** glob matching; the first match wins. `bridge` makes the plugin own that model's compact turns and enables replay normalization; `passthrough` leaves every request on CPA's built-in route. Whether a provider has native compact capability is expressed by you through `bridge` / `passthrough` rules — the plugin does not guess upstream capability.

`summary_model` is optional; when absent, the request model is used for the ordinary summary request. `on_no_match` accepts only `passthrough`.

CLIProxyAPI **Home mode disables plugin executor routing**, so it cannot be used together with a `bridge` rule.

## Security

Read [SECURITY.md](SECURITY.md). In short:

- `encrypted_content` is plaintext, not a security boundary.
- The summary provider receives your compressed context.
- The plugin fails closed (`compact_bridge_failed`) instead of forwarding compaction protocol items to an unsupported upstream.
- Report vulnerabilities privately, not via public issues.

## Verification

```bash
(cd plugin && go test ./... && go vet ./...)
CPA_SOURCE_DIR=/path/to/CLIProxyAPI (cd integration && go test ./... -count=1)
```

The integration suite builds a real CPA binary and the c-shared plugin and covers V1, V2 HTTP/SSE, HTTP replay, streaming replay, and a WebSocket V2 compact turn with `previous_response_id` and its continuation.
It requires a local checkout of CLIProxyAPI at `CPA_SOURCE_DIR`; CI supplies the
pinned checkout automatically.

## Documentation

- [Protocol contract](docs/compact-protocol.md)
- [Configuration](docs/configuration.md)
- [Domain glossary](CONTEXT.md)
- [Architecture decision: plaintext V2 compaction items](docs/adr/0001-use-plaintext-v2-compaction-items.md)

中文文档见 [README.zh-CN.md](README.zh-CN.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

MIT — see [LICENSE](LICENSE). Third-party notices are in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).
