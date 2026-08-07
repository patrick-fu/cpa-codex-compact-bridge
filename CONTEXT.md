# Domain Glossary

## Compact Bridge Facade

The plugin route and executor that owns V1 and V2 compact turns for a configured third-party model family. An accompanying request interceptor restores marked summary state in ordinary turns after CPA has normalized their history.

## Native Compact Route

A model route whose upstream provider natively supports Codex remote compaction. The plugin must not transform its compact requests or opaque compact state.

## Bridged Compact Route

A configured third-party model route whose upstream cannot process Codex compaction protocol items. The Compact Bridge Facade summarizes context through an ordinary model request and maintains the compatibility boundary.

## Bridge Rule

An ordered, explicit user-configured glob rule matched against the original `RequestedModel`. `bridge` makes the Compact Bridge Facade own matching compact turns and enables replay normalization; `passthrough` leaves the request to CPA. The first match wins and no match means passthrough.

## Summary Model

The model used to produce a bridged context summary. It defaults to the active bridged model and can be overridden by its Bridge Rule.

## Fail-Closed Compact

The policy that rejects a failed bridged compaction instead of forwarding Codex-specific compact protocol items to an unsupported upstream provider.

## Plaintext Compaction Item

A bridged V2 `compaction` item whose `encrypted_content` is the generated plaintext summary and whose `id` begins `cpa_compact_`. The marker lets the Compact Bridge Facade safely recognize and normalize its own state on later turns. It is intentionally a best-effort interoperability form rather than an opaque, provider-verifiable native compaction state.

## V1 Summary Message

The single ordinary assistant message emitted as the compacted V1 replacement window. Unlike a Plaintext Compaction Item, it is normal Responses message content and needs no replay normalization.

## WebSocket Bridged Turn

A Responses WebSocket `response.create` whose final input is a V2 trigger and matches a Bridge Rule. CPA adapts the facade's Responses events to WebSocket messages; later ordinary continuations are replay-normalized by the request interceptor.
