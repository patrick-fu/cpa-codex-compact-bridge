# Session portability across compaction and reasoning protocols

**Date:** 2026-08-13
**Scope:** Codex/OpenAI Responses, Claude Code/Anthropic Messages, CLIProxyAPI v7.2.125 code paths, and `cpa-codex-compact-bridge`. CPA claims below were rechecked against the exact v7.2.125 tag snapshot (`2e6b1d83f6c304a102aa33c1faf0a4f94d0d331e`), not inferred from the older local checkout. This was a read-only protocol and implementation audit; no model request or environment change was made.

## Executive conclusion

The portability boundary is the state representation, not the model name:

- A full visible transcript or a readable summary can usually continue on another provider at conversation level. Tool schemas and hidden reasoning may still be lost.
- An OpenAI-native encrypted compaction item is provider-owned opaque state. There is no documented cross-model or cross-provider compatibility contract. Once an old session only has that state, a proxy cannot reconstruct it after the switch.
- Native Codex remote compact V1 and V2 use different transports but converge on the same durable checkpoint: `RolloutItem::Compacted(CompactedItem)`. Their normalized `replacement_history` is expected to be equal.
- Bridge V1 and V2 now store the same plaintext `cpa_compact_*` compaction item. V1 returns retained user/developer history plus the item, while V2 returns the item over SSE and lets Codex retain history locally.
- Claude Code's locally observed `/compact` and auto-compact path stores a plaintext summary in the transcript. It is therefore much easier to migrate than OpenAI's encrypted compaction state.
- Compaction state and reasoning state are separate. A readable compact summary does not make an old GPT `reasoning.encrypted_content` or Claude `thinking.signature` portable.

An already-native-compacted GPT session cannot be made semantically whole on DeepSeek by the current bridge. It should remain an explicit unsupported migration case, with fail-closed detection and a user-visible handoff path. It should not drive the bridge implementation toward guessing or silently dropping state.

## Terminology correction

The project's “V1” and “V2” names are implementation shorthand, not official OpenAI protocol versions.

| Name used below | Actual shape | Persisted state |
|---|---|---|
| Uncompressed | Ordinary Responses or Messages history | Visible messages, tools, plus possible provider-specific reasoning blocks |
| Codex Local Compact | Codex client replaces old history with a readable summary | Ordinary plaintext history; no native compact item |
| Standalone compact (“V1” in this project) | `POST /responses/compact` | OpenAI-native response is a canonical compacted window containing an opaque encrypted compaction item and possibly retained items |
| Streaming trigger (“V2” in this project) | Codex-observed `/responses` request ending in `compaction_trigger` | Native path returns an opaque compaction item; the bridge returns a `cpa_compact_*` item |
| OpenAI server-side compaction | `POST /responses` with `context_management.compact_threshold` | Opaque encrypted compaction item emitted and applied in the same stream |
| Bridge V1 | Plugin intercepts standalone compact and calls an ordinary summary model | V1 envelope with retained user/developer history plus one `cpa_compact_*` compaction item |
| Bridge V2 | Plugin intercepts the streaming trigger and calls an ordinary summary model | `cpa_compact_*`; `encrypted_content` contains plaintext, despite its field name |
| Claude Code client compact | `/compact` or auto-compact in the currently observed CLI path | Plaintext summary stored as a user compact-summary entry |
| Anthropic server compaction | Messages beta `compact_20260112` | Anthropic-specific readable `compaction` content block that must be passed back |

OpenAI documents two current API modes: server-side compaction in `/responses`, and the standalone `/responses/compact` endpoint. The standalone endpoint returns a complete canonical next window; it must be passed onward as-is rather than reduced to its compaction item. Both native modes use opaque state. Source: [OpenAI Compaction](https://developers.openai.com/api/docs/guides/compaction).

Latest official Codex `main` at `e0de12a126f2beb10599a18fd4ca6b08f6e0a1a0` confirms the client-level contract:

- V1 deserializes `/responses/compact` as `CompactHistoryResponse { output: Vec<ResponseItem> }` (`codex-rs/codex-api/src/endpoint/compact.rs:86-90`). The native test responder retains user/developer messages and appends one `ResponseItem::Compaction` (`codex-rs/core/tests/common/responses.rs:1119-1187`).
- V2 requires exactly one streamed `ResponseItem::Compaction`, otherwise it raises a fatal error (`codex-rs/core/src/compact_remote_v2.rs:396-455`).
- Both call `Session::replace_compacted_history`, which writes `RolloutItem::Compacted(CompactedItem)` (`codex-rs/core/src/session/mod.rs:3306-3340`).
- `compact_remote_parity.rs:217-247` asserts that V1 and V2 produce equal post-compact requests and equal rollout `replacement_history`.

Therefore transport envelopes differ—unary JSON versus SSE—but their installed compacted history is deliberately normalized to parity.

CPA v7.2.125 currently removes `context_management` before sending a request to the Codex upstream because that upstream rejects the field. See `CLIProxyAPI/internal/translator/codex/openai/responses/codex_openai-responses_request.go:86-100`. Therefore the current CPA Codex path does not implement the newer OpenAI server-side mode even though the public Responses API does.

## Outcome legend

- **Conversation-level:** visible text and completed tool results can continue, subject to protocol translation losses. This label does not imply hidden-reasoning continuity.
- **Summary-level:** only the readable summary continues; details and hidden reasoning are intentionally lost.
- **Unsafe partial:** the request may return 200, but opaque or signed state was stripped, ignored, or not understood. A 200 response is not proof of session continuity.
- **Fail closed:** the bridge rejects state it cannot safely interpret.

## Codex / Responses direction matrix

The bridge decision is per requested target model. In the intended configuration, GPT models are `passthrough` and third-party models are `bridge`.

| Existing session state | GPT → third party, bridge off | GPT → third party, bridge on | Third party → GPT, GPT passthrough | Third party → GPT, GPT deliberately bridged |
|---|---|---|---|---|
| Uncompressed visible history | Conversation-level; GPT opaque reasoning state is not portable and translator loss is possible | Same visible-history result; ordinary requests contain no compact item to normalize | Conversation-level; third-party opaque reasoning state is translated only as visible reasoning text where supported | Same, but GPT unnecessarily loses its native compact route |
| Codex Local Compact plaintext | Summary-level | Summary-level | Summary-level | Summary-level |
| Native standalone compact window (“V1”) | Unsupported. Retained visible items may survive, but opaque compaction state may be ignored or stripped | **Fail closed 502** when an unknown native compaction item reaches bridge replay/summary logic | Unsupported unless the source is actually the same provider lineage with an explicit compatibility contract | **Fail closed 502**; the bridge does not decrypt native state |
| Bridge V1 `cpa_compact_*` plaintext item | Unsupported/unverified because no replay normalizer runs | Summary-level: bridge rewrites it to an ordinary user summary | Unsupported in the current recommended GPT passthrough rule | Summary-level; technically works but needlessly replaces GPT's future native compact path |
| Native streaming compaction item (“V2”), including old `cmp_*` state | Unsupported; transport-specific code may pass, merge, strip, or ignore it | **Fail closed 502** because it is not a `cpa_compact_*` item | Unsupported | **Fail closed 502** |
| Bridge V2 `cpa_compact_*` plaintext item | Unsupported/unverified because no replay normalizer runs | Summary-level: bridge rewrites it to an ordinary user summary | Unsupported in the current recommended GPT passthrough rule | Summary-level: technically works, but it also replaces GPT's future native compact behavior and is not the recommended steady state |
| OpenAI `context_management` compaction item | Treat like native opaque state; not portable | Fail closed if presented as unknown compact state | Not portable without an explicit same-lineage contract | Fail closed |

Important asymmetry:

1. **Native GPT → third party after native compaction is inherently unrecoverable at the proxy layer.** The bridge has no plaintext to recover and must not guess.
2. **Bridge third party → GPT after Bridge V1 or V2 is recoverable at summary level once replay routing is decoupled.** Both transports install the same `cpa_compact_*` plaintext compaction state. The remaining problem is that replay normalization is coupled to the target model's `bridge` rule. A small design change can normalize plugin-owned state while leaving future GPT compact requests on native passthrough.

The previous black-box behavior is consistent with this matrix: an old native GPT compact item sent to DeepSeek with bridge enabled failed closed; disabling the bridge could produce a normal response, but that did not prove the compacted context survived.

### Bridge on/off semantics

The plugin registers only `openai-response` input and output formats (`plugin/main.go:165-184`). It has two distinct jobs that are currently tied to one rule:

1. choose whether a compact request is native passthrough or converted into an ordinary summary call;
2. normalize a later `cpa_compact_*` replay into an ordinary user summary.

It recognizes only its own marker. `plugin/replay.go:72-95` rewrites `cpa_compact_*`; every other compaction item fails closed. Bridge V1 returns a canonical compacted window ending in a plaintext-marked compaction item; Bridge V2 returns the same item in SSE. This matches the normalized-history parity required by official Codex tests.

This means “plugin installed” and “bridge used” are not the same condition. A model with `action: passthrough` does not receive bridge replay normalization.

## Claude Code / Messages direction matrix

Codex local/V1/V2 are not Claude protocol variants. For Claude Code, the comparable states are its client-side compact summary and Anthropic's separate server-side compaction block. The Compact Bridge has no effect on any row below because it does not register the Messages protocol.

| Existing session state | Claude model → third party in Claude Code wrapper | Third party → Claude model in Claude Code wrapper | Required handling |
|---|---|---|---|
| Uncompressed Messages history | Conversation-level after translating visible messages/tools. CPA v7.2.125 default mode drops non-GPT-compatible Claude signatures but can keep visible thinking text only for a configured `is-compat` model | Conversation-level; third-party visible reasoning can be wrapped for compatible models, but foreign opaque state is not Claude-native | Switch only between completed turns; use `is-compat` only when the configured upstream expects CPA's compatibility representation |
| Claude Code client `/compact` or auto-compact summary | Summary-level and generally portable because the stored state is plaintext | Summary-level and generally portable | Continue with the summary as an ordinary message |
| Anthropic `compact_20260112` block | Raw block is Anthropic-specific and should not be sent to a generic provider | Only meaningful if it was produced by a compatible Anthropic path | Its `content` is readable, so extract it into an ordinary handoff summary when leaving Anthropic |
| Imported OpenAI native compact state | Not a Messages state and not portable | Not portable | Stay on the originating Responses lineage or create a new plaintext handoff |
| Imported Bridge V2 `cpa_compact_*` | No automatic handling; the Messages plugin path never sees it | No automatic handling | Explicitly convert its plaintext to a normal message before entering Claude Code |

Claude Code's documented compaction replaces history with a summary. To create it, Claude Code sends a separate summarization request containing the same system prompt, tools, and history plus a summarization instruction. The next request uses the shorter summary history. Sources: [Claude Code prompt caching](https://code.claude.com/docs/en/prompt-caching) and [session management](https://code.claude.com/docs/en/sessions).

The locally observed Claude Code 2.1.221 transcript shape is:

- a `system` entry with subtype `compact_boundary` and compact metadata;
- a following `user` entry with `isCompactSummary: true` and a plaintext Markdown summary.

This was observed for both manual and automatic compact samples. The CLI binary also contains support for the Anthropic server-side beta, but the inspected local transcripts did not demonstrate that path as the active default. The two mechanisms must remain separate in tests and documentation.

Anthropic's API-side compaction returns a readable `compaction` block, requires the complete block to be appended to subsequent Messages calls, and ignores content before the most recent block. It is protocol-specific even though its summary is readable. Source: [Anthropic Compaction](https://platform.claude.com/docs/en/build-with-claude/compaction).

## Encrypted reasoning / “CoT” handling

The public fields below should not be described as a portable raw chain of thought. They are either readable summaries or opaque state controlled by the provider.

### OpenAI Responses

A reasoning item can carry:

- readable `summary` text;
- opaque `encrypted_content`, used to preserve reasoning state in stateless/ZDR-style continuation.

This is different from a `compaction` item's `encrypted_content`, even though both use the same field name. The item type and lifecycle define the meaning.
OpenAI describes persisted reasoning as opaque, says only available and compatible items are rendered into later turns, and requires complete output replay in stateless mode. Source: [OpenAI Reasoning](https://developers.openai.com/api/docs/guides/reasoning).

CPA's audited conversions are conservative by default, with an explicit per-model `is-compat` exception:

| Source → target | CPA behavior |
|---|---|
| Responses → OpenAI Chat / typical OpenAI-compatible chat provider | Flattens readable reasoning summary into `reasoning_content`; does not preserve the Responses `encrypted_content` (`responses/openai_openai-responses_request.go:191-211`) |
| Responses → Claude Messages | Creates a Claude `thinking` block only if the signature is already Claude-compatible; a GPT signature is not converted (`claude_openai-responses_request.go:487-497`) |
| Responses → Gemini | Preserves visible summary; a compatible Gemini signature is retained, while missing or foreign state is replaced by Gemini's documented-in-code bypass sentinel rather than treated as original Gemini reasoning |
| Responses → GPT/Codex upstream | Validates GPT reasoning signatures; invalid/null/wrong-provider `encrypted_content` is stripped, and orphan IDs are also removed when storage is disabled (`openai_responses_signature.go:14-141`) |
| Responses → xAI | Uses xAI/Grok-specific signature validation and replay cache; GPT/Claude/Gemini opaque formats are not treated as Grok state |

Therefore a GPT session can still produce a successful answer after switching provider while silently losing hidden reasoning continuity. That is an **unsafe partial** result, not full compatibility.

### Anthropic Messages

An extended-thinking response uses:

```json
{"type":"thinking","thinking":"<readable summary or empty>","signature":"<opaque encrypted state>"}
```

Safety-redacted state uses:

```json
{"type":"redacted_thinking","data":"<opaque encrypted state>"}
```

Anthropic documents these rules:

- during one tool-use assistant turn, every `thinking` and `redacted_thinking` block must be returned complete, unchanged, and in order;
- modifying, partially dropping, or reordering the latest consecutive blocks produces a 400 error;
- outside a tool-use turn, older thinking blocks may be omitted;
- when switching models, prior thinking and redacted-thinking blocks should be stripped; they are tied to the producing model;
- signatures are portable across the Claude API, Amazon Bedrock, and Google Cloud, but that does not make them portable to non-Anthropic model families.

Source: [Anthropic Thinking](https://platform.claude.com/docs/en/build-with-claude/thinking).

CPA's default Messages → OpenAI Chat translator maps a `thinking` block to `reasoning_content` only when its signature is already GPT-compatible; ordinary Claude signatures therefore do not become GPT-native reasoning. A v7.2.125 configured API-key model with `is-compat: true` deliberately preserves the **visible thinking text** as `reasoning_content` even when the signature is empty or foreign. It still does not convert the Claude signature into a GPT signature. `redacted_thinking` remains explicitly ignored (`internal/translator/openai/claude/openai_claude_request.go:178-193,376-387` in v7.2.125).

The reverse Responses → Claude path behaves similarly: default mode creates a Claude thinking block only from a Claude-compatible signature; `is-compat` may carry the original opaque value in the Messages `signature` field for a compatibility endpoint. That is an endpoint-specific transport mode, not proof that Anthropic itself accepts a GPT/DeepSeek signature. Before a native Claude upstream, the executor's sanitizer preserves compatible Claude blocks, drops incompatible ones, and records its decisions (`internal/runtime/executor/claude_executor.go:66-78,109-132`; `internal/signature/claude_messages_sanitize.go:42-64` in v7.2.125).

### Compaction and reasoning are orthogonal

| State | Human-readable? | Cross-provider policy |
|---|---|---|
| Ordinary message/tool result | Yes | Translate best effort |
| Local/client compact summary | Yes | Continue at summary level |
| Bridge V1 `cpa_compact_*` payload | Yes, despite field name | Normalize to ordinary text before changing route |
| Bridge V2 `cpa_compact_*` payload | Yes, despite field name | Normalize to ordinary text before changing route |
| OpenAI native compaction item | No | Same documented lineage only; otherwise fail/handoff |
| OpenAI reasoning `encrypted_content` | No | Same compatible provider state only; otherwise strip |
| Claude `thinking.signature` / `redacted_thinking.data` | No | Exact replay only where Anthropic compatibility rules allow; otherwise strip |
| Anthropic server `compaction.content` | Yes | Raw block stays Anthropic-specific; text can be extracted for handoff |

Bridge compaction deliberately trades native hidden-state continuity for provider portability. It cannot preserve encrypted reasoning that it cannot read.

## Product decisions and minimal fix

1. **Keep Bridge V1/V2 parity covered.** V1 returns a canonical replacement window: retained user/developer items plus exactly one `cpa_compact_*` compaction item. V2 keeps its SSE envelope but produces the same normalized replacement history.
2. **Declare old native-compact GPT → third-party migration unsupported.** Keep fail-closed detection. Offer a new session or a plaintext handoff summary generated before switching when the original readable history is still available.
3. **Separate compact generation from replay compatibility.** Normalize valid `cpa_compact_*` items for every target model, including GPT passthrough, without making GPT compact requests use Bridge V1/V2. This fixes Bridge-third-party → GPT at summary level.
4. **Do not accept 200 as continuity evidence.** Black-box assertions must verify the exact rollout `RolloutItem::Compacted.replacement_history`, the subsequent request body, and a canary fact from before compact.
5. **Do not extend the current plugin to Messages implicitly.** Claude Code needs a separate Messages-aware migration layer if automatic Anthropic compaction-block conversion is desired.
6. **Switch at turn boundaries.** Never change Claude providers in the middle of a tool-use turn; exact thinking/tool block continuity can otherwise be invalid.

## Minimum verification matrix

The next isolated test should cover:

- Codex: uncompressed, Local Compact, native standalone compact, native streaming compact, Bridge V1, and Bridge V2; for V1/V2, assert equal rollout `replacement_history` and equal follow-up request after normalizing transport-only fields.
- Bridge: target rule `bridge` and `passthrough`; verify pre-compact canary facts, not merely HTTP status.
- Claude Code: uncompressed, manual `/compact`, auto-compact, and (only if explicitly enabled) Anthropic server compaction; each tested Claude → third party and third party → Claude.
- Reasoning: no tools, completed tool turn, and attempted mid-tool switch; assert sanitizer decisions and absence of invalid foreign signatures upstream.

## Primary sources and local implementation evidence

- [OpenAI Compaction](https://developers.openai.com/api/docs/guides/compaction)
- [OpenAI Reasoning](https://developers.openai.com/api/docs/guides/reasoning)
- [OpenAI model guidance](https://developers.openai.com/api/docs/guides/latest-model)
- [Claude Code prompt caching](https://code.claude.com/docs/en/prompt-caching)
- [Claude Code session management](https://code.claude.com/docs/en/sessions)
- [Anthropic Thinking](https://platform.claude.com/docs/en/build-with-claude/thinking)
- [Anthropic Compaction](https://platform.claude.com/docs/en/build-with-claude/compaction)
- Official Codex `main` source at `e0de12a126f2beb10599a18fd4ca6b08f6e0a1a0`, cloned to `/Users/patrickfu/dev/codex`
- `plugin/main.go`, `plugin/replay.go`, `plugin/responses.go`, and `docs/compact-protocol.md`
- `CLIProxyAPI/docs/responses-compact-local-bridge.md`
- `CLIProxyAPI/internal/translator/**` and `CLIProxyAPI/internal/runtime/executor/**` at the audited v7.2.125-compatible source baseline
