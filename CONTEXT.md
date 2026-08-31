# Domain Glossary

## Compact Bridge Facade

The plugin route and executor that owns V1 and V2 compact turns for a configured third-party model family. An accompanying request interceptor restores marked summary state in ordinary turns after CPA has normalized their history.

## Native Compact Route

A model route whose upstream provider natively supports Codex remote compaction. Its compact turns remain outside the Compact Bridge Facade, while the Compaction State Policy may still restore a Plaintext Compaction Item before an ordinary or compact request continues.

## Declared Native-Compatible Target

A requested model that explicitly matches a `passthrough` Bridge Rule. The rule is the administrator's declaration that opaque native compaction state may continue on that route; the plugin cannot identify or verify which provider or model created the opaque state. An unmatched model is not a Declared Native-Compatible Target even though CPA otherwise applies its passthrough fallback.

## Bridged Compact Route

A configured third-party model route whose upstream cannot process Codex compaction protocol items. The Compact Bridge Facade summarizes context through an ordinary model request and maintains the compatibility boundary.

## Bridge Rule

An ordered, explicit user-configured glob rule matched against the original `RequestedModel`. `bridge` makes the Compact Bridge Facade own matching compact turns; `passthrough` leaves the request to CPA. The first match wins and no match means passthrough. A Bridge Rule never decides whether existing Plaintext Compaction State may be restored, which is the job of the Compaction State Policy.

## Summary Model

The model used to produce a bridged context summary. It defaults to the active bridged model and can be overridden by its Bridge Rule.

## Fail-Closed Compact

The policy that rejects a failed bridged compaction instead of forwarding Codex-specific compact protocol items to an unsupported upstream provider.

## Plaintext Compaction Item

A bridged V1 or V2 `compaction` item whose `encrypted_content` is the generated plaintext summary and whose `id` begins `cpa_compact_`. The marker lets the Compact Bridge Facade safely recognize and normalize its own state on later turns. It is intentionally a best-effort interoperability form rather than an opaque, provider-verifiable native compaction state.

## Compaction State Policy

The single one-way rule that decides what may continue on which route: valid Plaintext Compaction State always becomes an ordinary user summary, opaque native compaction state continues only on a Declared Native-Compatible Target, and Mixed or Corrupt state fails closed for every target.

## Corrupt Bridge State

A Plaintext Compaction Item whose `encrypted_content` is absent, empty, or whitespace-only. It cannot be replayed or summarized, so the plugin rejects it instead of forwarding it or inventing an empty user message.

## Mixed Compaction State

One request that carries Plaintext Compaction Items together with opaque native compaction items. The retained history can no longer be ordered or interpreted safely, so it fails closed regardless of the target rule.

## Compaction State Error

The stable code `invalid_compaction_state` for deterministic state the bridge can never continue. It is a client error, returned as HTTP 400 before any upstream call or compact stream opens, so the Codex client does not spend a retry on it.

## Runtime Compact Failure

The stable code `compact_bridge_failed` for failures that may succeed on another attempt: Summary Model generation, a blank generated summary, network, and upstream errors. V1 answers HTTP 502 and V2 answers an in-band `response.failed` frame.

## Canonical Compacted Window

The replacement history Codex installs after remote compaction. V1 receives the whole window from `/responses/compact`; V2 retains eligible history locally and appends the streamed compaction item. The two transports must converge on equivalent retained history plus one Plaintext Compaction Item.

## WebSocket Bridged Turn

A Responses WebSocket `response.create` whose final input is a V2 trigger and matches a Bridge Rule. CPA adapts the facade's Responses events to WebSocket messages; later ordinary continuations are replay-normalized by the request interceptor.
