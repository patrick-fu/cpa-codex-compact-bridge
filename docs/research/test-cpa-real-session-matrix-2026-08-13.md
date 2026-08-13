# test-cpa real Codex session compaction matrix (2026-08-13)

## Scope and environment

- Target: isolated `test-cpa` only, CPA `v7.2.125` (`2e6b1d8`). Production was not accessed.
- Codex CLI: `0.147.0`; each run used a separate `CODEX_HOME` and app-server `thread/resume` + `thread/compact/start`.
- Third-party provider: real `DeepSeek V4 Flash` credential already configured in `test-cpa`.
- OAuth: a current local ChatGPT OAuth access token was temporarily installed without its refresh token, used for GPT probes, and removed after the run. No credential value is recorded here.
- Bridge artifact: `cpa-codex-compact-bridge-v0.1.1-rc.806ba3c.so`, SHA-256 `07320c22ae9a076580fab0508b22a1208a2e237db66b4ae76f535e60793dabc5`.

Official Codex configuration documents profile files as `$CODEX_HOME/<name>.config.toml`. Current Codex source selects local compaction for providers without remote compaction support, legacy V1 for an OpenAI-named provider with `remote_compaction_v2=false`, and V2 for the same provider with the feature enabled. See the official [configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference) and `codex-rs/model-provider/src/provider.rs`, `codex-rs/core/src/session/turn.rs` in the local latest OpenAI Codex checkout.

## Historical inventory and selected samples

The local inventory contained 2,134 rollout JSONL files: 1,133 `model_provider=custom`, 313 with a persisted compact checkpoint, 11 with local-summary lineage, 303 with remote-compaction lineage, and 85 containing `cpa_compact_` state. V1 and V2 cannot be distinguished from their final rollout alone because both persist the same `CompactedItem`; transport mode was therefore forced explicitly during replay.

| Sample | Historical shape | Size |
|---|---|---:|
| `019cedbc-9dc0-7760-8fb0-b77ecbb0786f` | OpenAI, Codex 0.114.0, local compact | 1,369,064 B |
| `019fc5d4-bc80-7de2-9d4e-48f38e1c40ed` | custom provider, uncompressed | 294,889 B |
| `019fc5db-40f6-7082-99b4-07bb605af4ca` | short custom-provider session | 90,492 B |
| `019ff0f5-5111-7c73-82f2-cf91335caeca` | GPT → DeepSeek, native `cmp_*` checkpoint | 401,284 B |
| `019fddff-72f3-78a0-a49b-22501ef99b27` | third-party session containing `cpa_compact_*` | 162,575 B |

Copies received new test thread IDs. Real user/assistant/tool history was retained. For controlled transport comparisons, persisted old `turn_context` records were removed where necessary so current-model fallback could not silently retry an old GPT model; this does not remove conversation items.

## Main matrix

`Pass` means manual compact completed and a real follow-up returned `CONTINUATION-OK`, unless noted as compact-only.

| Route | Bridge ON | Bridge OFF | Result and evidence |
|---|---|---|---|
| DeepSeek local compact, plain old session | Pass | Pass | Client summarizes through ordinary `/v1/responses`; Bridge is not required. |
| DeepSeek V1, short old session | Pass | Fail | ON returned/persisted `cpa_compact_*` and continuation passed. OFF reached `/v1/responses/compact` then third-party upstream returned 404. |
| DeepSeek V2, short old session | Pass | Fail | ON received exactly one compaction item and continuation passed. OFF returned `auth_unavailable` for the selected incompatible native path. |
| Existing Bridge `cpa_compact_*` → local compact | Pass | Not required | Marker replay normalized to a user summary; compact and continuation passed. |
| Existing Bridge `cpa_compact_*` → V2 | Pass | Not meaningful | Replay, re-compaction, and continuation passed. |
| Existing native GPT `cmp_*` → DeepSeek local compact | **Fail** | Pass (compact-only) | ON returned 502 `compact_bridge_failed`; OFF allowed Codex local summarization. This is the key new compatibility defect. |
| GPT local compact over real OAuth | Pass | Same behavior | Ordinary GPT Responses calls succeeded; Bridge rule is passthrough for `gpt-*`. |
| GPT native V1 over real OAuth | Fail | Same behavior | `/v1/responses/compact` reached Codex OAuth upstream and returned 404. |
| GPT native V2 over real OAuth | Pass (compact-only) | Same behavior | V2 compaction completed with the real temporary OAuth credential. |

Additional long-history V1 observation: the first Bridge compact succeeded, but a follow-up could automatically compact again using a persisted previous GPT model and fail. Removing stale `turn_context` model history or using a fresh branch made the V1 compact + continuation pass. This is Codex previous-model fallback behavior, not a V1 response-shape failure.

## Findings

1. The new V1 response shape is accepted by the real current Codex client. It persisted a `cpa_compact_*` compaction item, and controlled continuation passed. V1/V2 state parity is therefore proven beyond unit/integration mocks.
2. Bridge is necessary for DeepSeek remote V1/V2. Without it, the provider cannot consume Codex remote compaction transports.
3. Local compact is independent of Bridge for ordinary history and works with both DeepSeek and GPT.
4. A native `cmp_*` checkpoint is provider-owned opaque state. The Bridge currently rejects it before a DeepSeek local compact can summarize it. The minimal behavioral fix is to distinguish an ordinary local-summary request from Bridge V1/V2 replay: for local summarization, retain visible surrounding messages and omit the opaque native compaction item; keep fail-closed behavior when a native checkpoint would otherwise be replayed as third-party conversation state.
5. GPT V1 is not usable in this `test-cpa`/OAuth combination; GPT V2 is. This validates keeping V2 as the default and treating V1 as a compatibility transport, not a universal native path.

## Cost and request evidence

- 54 CPA Responses/compact log files were created during the full exploratory run, including controlled reruns and expected failures.
- Representative successful DeepSeek V1 continuation reported 92,390 cumulative tokens in the isolated copied thread (23,518 in the last turn, 17,152 cached); V2 reported 96,928 cumulative (28,056 last, 17,152 cached). These cumulative counters include retained historical rollout context and are not equivalent to incremental provider billing.
- No provider price was available from the CPA response, so monetary cost is not asserted.

## Final state and cleanup

- Bridge config was restored byte-for-byte to the pre-test JSON (`SHA-256 2023077d6eff84c6928ca5e2aa416e77cc8afba67cab5a7807b9673d80da26d4`) and remains enabled.
- The temporary OAuth auth file was deleted; the pre-existing test Codex auth remains.
- Real `~/.codex/auth.json` and `~/.codex/config.toml` size/mtime were unchanged.
- Repository source remained unchanged during the test run. The isolated evidence directory is under the system temporary directory and contains copied private rollout content, so it is intentionally not committed.
