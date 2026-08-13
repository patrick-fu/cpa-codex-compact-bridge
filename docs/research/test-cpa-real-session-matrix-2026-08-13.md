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
| Existing native GPT `cmp_*` → DeepSeek local compact | **Expected fail-closed** | Pass (compact-only) | ON returned 502 `compact_bridge_failed`. The native checkpoint is provider-owned opaque state; the Bridge must not silently strip or translate it when the session changes provider. |
| GPT local compact over real OAuth | Pass | Same behavior | Ordinary GPT Responses calls succeeded; Bridge rule is passthrough for `gpt-*`. |
| GPT native V1 over real OAuth | Fail | Same behavior | `/v1/responses/compact` reached Codex OAuth upstream and returned 404. |
| GPT native V2 over real OAuth | Pass (compact-only) | Same behavior | V2 compaction completed with the real temporary OAuth credential. |

Additional long-history V1 observation: the first Bridge compact succeeded, but a follow-up could automatically compact again using a persisted previous GPT model and fail. Removing stale `turn_context` model history or using a fresh branch made the V1 compact + continuation pass. This is Codex previous-model fallback behavior, not a V1 response-shape failure.

## Findings

1. The new V1 response shape is accepted by the real current Codex client. It persisted a `cpa_compact_*` compaction item, and controlled continuation passed. V1/V2 state parity is therefore proven beyond unit/integration mocks.
2. Bridge is necessary for DeepSeek remote V1/V2. Without it, the provider cannot consume Codex remote compaction transports.
3. Local compact is independent of Bridge for ordinary history and works with both DeepSeek and GPT.
4. A native `cmp_*` checkpoint is provider-owned opaque state. GPT sessions containing that state are not portable to DeepSeek. The Bridge's 502 fail-closed behavior is intentional and must remain; no fallback, stripping, translation, or retry should be added.
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

## Semantic continuity round (2026-08-14)

Transport success is not sufficient to establish that a compacted session remains useful. A second round copied one fact-dense historical Codex rollout into isolated profiles and evaluated the actual post-compaction continuation. The source contained:

- the original goal, conclusion, five agent roles, and three collaboration operations;
- five initial multiplication probes;
- unique `send_message` tokens and `followup_task` calculations;
- a GLM timing race whose first and second rounds had different outcomes;
- a new requirement that could only be answered correctly if the compressed history retained that timeline.

Each case asked three real continuation questions after compaction. Answers were anonymized before two independent GPT judges (`gpt-5.4` and `gpt-5.6-terra`) scored them against a fixed 100-point diagnostic rubric. The initial table used 90 as an absolute diagnostic threshold and penalized invented facts. That threshold is not the Bridge release gate: local compact is lossy too, so the correct product criterion is relative non-inferiority against repeated local-compact runs using the same source history, model, prompt, questions, and judges. This follows OpenAI's recommendation to use task-specific criteria and automated graders for repeatable evaluation rather than relying on subjective inspection; see [Evaluation best practices](https://developers.openai.com/api/docs/guides/evals). It also tests the behavior that matters for compaction: whether the resulting context can be used for correct subsequent Responses calls; see [Compaction](https://developers.openai.com/api/docs/guides/compaction).

| Case | Repeat | GPT-5.4 judge | GPT-5.6 Terra judge | Result |
|---|---:|---:|---:|---|
| Uncompressed DeepSeek baseline | 1 | 96 | 90 | Pass |
| DeepSeek local compact | 1 | 79 | 87 | Marginal |
| Bridge V1 | 1 | 49 | 46 | Fail |
| Bridge V1 | 2 | 83 | 94 | Mixed |
| Bridge V1 | 3 | 80 | 86 | Marginal |
| Bridge V2 | 1 | 69 | 71 | Fail |
| Bridge V2 | 2 | 62 | 84 | Fail/marginal |
| Bridge V2 | 3 | 69 | 75 | Fail/marginal |

All seven compacted runs completed transport and all post-compact model turns returned successfully. The quality failures were semantic:

- every Bridge repeat omitted the first GLM follow-up fact `67×71=4757`;
- V1 repeat 1 lost all five initial multiplication probes and assigned two tokens to the wrong roles;
- V2 repeats retained more detailed prose but sometimes incorrectly concluded that DeepSeek also needed the new same-round `send_message` retest;
- local compact retained the core result and most exact facts, but missed `67×71=4757` and proposed a weaker GLM retest sequence.

V1 and V2 share the same Bridge summarization function and both persisted a valid `cpa_compact_*` item. The repeats therefore do not indicate a V1/V2 response-shape regression. They exposed a confounder in the comparison: Bridge used its own shorter handoff instruction while local compact used Codex's built-in prompt. Summary output was also variable: V1 summaries were 1,400–1,487 characters; V2 summaries were 2,397–3,375 characters; the local summary was 1,782 characters. More text did not guarantee the correct decision.

Minimum next fix: first align the Bridge default prompt byte-for-byte with Codex local compact, support an explicit plugin-side prompt override for clients that customize `compact_prompt`, then rerun local/V1/V2 repeatedly. The release gate should compare score distributions (proposed: Bridge median no more than five points below local median), not require absolute perfect recall. Do not change `cmp_*` recognition or fail-closed behavior as part of that fix.

The four GPT judge requests used the existing test Codex OAuth and reported 30,577 total tokens. DeepSeek monetary cost is not asserted because CPA/app-server counters include copied historical context and do not expose provider billing. No authentication, plugin, or routing configuration changed in this round.

### Native `cmp_*` safety recheck

The native GPT checkpoint case was rerun once with Codex `request_max_retries=0` and `stream_max_retries=0`:

- one `POST /v1/responses`, request ID `00000000-0000-4000-8000-000000000301`;
- one CPA request log, HTTP 502 in 391 ms, stable code `compact_bridge_failed`;
- no DeepSeek credential selection or upstream model call for that request;
- zero new `cpa_compact_*` markers and no additional compacted checkpoint;
- the source rollout SHA-256 remained `73f52c8deda4503987e0dd9e1f593025f8d8710672e512a3f34ead174cd12ca5`.

This case passes the intended safety contract: detect native `cmp_*`, stop once, do not retry, do not bill the third-party provider, and do not manufacture portable state.

After the round, test-cpa still had the same candidate artifact SHA-256 `07320c22ae9a076580fab0508b22a1208a2e237db66b4ae76f535e60793dabc5`, the same config SHA-256 `6812ab25010dd65a24115718c0551e156e038330f41529eb6dd74a06c834f2fa`, and the same rules: `gpt-*` passthrough, `DeepSeek V4 Flash` bridge, catch-all passthrough. Production was not accessed.

## Prompt-aligned relative evaluation (2026-08-14)

Commit `f20e36d` changed the Bridge default summary instruction to the exact Codex local-compaction prompt from OpenAI Codex `main` at `a70211249ab5d003836a2bb339f69265df84512c`. It also added optional plugin config `compact_prompt`; this must be set explicitly when the client customizes its own Codex prompt because remote V1/V2 requests do not carry that client-side setting.

The prompt-aligned candidate was built by Linux CI against CPA v7.2.125 as version `0.1.2-rc.d12112f`. Source artifact, downloaded artifact, staged container file, and final plugin file all matched SHA-256 `2541541e162dead650d14ddf1170775db634d143fff56d4be9e8645873b118f2`. test-cpa loaded and registered that exact version after a CPA-only service restart; PostgreSQL and production were not touched. The test configuration was restored to SHA-256 `6812ab25010dd65a24115718c0551e156e038330f41529eb6dd74a06c834f2fa`.

Using the same historical rollout, DeepSeek model, prompt, three continuation questions, and isolated profile construction, local, V1, and V2 were each compacted three times. All nine compactions and all 27 real continuation turns completed. Two blind GPT judges then scored both continuation answers and the compact summary bodies.

| Route | Summary-body scores (6) | Median | Difference vs local |
|---|---|---:|---:|
| Local | 76, 80, 83, 84, 87, 88 | 83.5 | — |
| Bridge V1 | 84, 84, 86, 86, 87, 90 | 86.0 | +2.5 |
| Bridge V2 | 81, 82, 85, 85, 85, 87 | 85.0 | +1.5 |

Both Bridge transports pass the relative non-inferiority gate (`Bridge median >= local median - 5`). The summary-body comparison is the correct Bridge-specific metric. A secondary score based on the model answering questions after compaction produced medians local 79, V1 83, V2 72.5; inspection showed the V2 summaries retained the relevant facts, while the subsequent model sometimes made a more conservative inference about the DeepSeek retest. That downstream answer variance is not evidence that V2 returned a worse compact state.

The native `cmp_*` safety case was rerun against `0.1.2-rc.d12112f`: exactly one CPA request log was created, it returned 502 `compact_bridge_failed`, no new Bridge marker or compact checkpoint was produced, and no continuation request ran. The intended fail-closed behavior remains intact.

## Native V2 comparison (2026-08-14)

V1 was excluded. Three product paths used the same source rollout, three post-compact questions, three independent compactions per path, and two blind GPT judges:

- native V2: `gpt-5.4` through real test-cpa Codex OAuth;
- Bridge V2: `DeepSeek V4 Flash` through `0.1.2-rc.d12112f`;
- local compact: Codex local compact with `DeepSeek V4 Flash`.

Native V2 state is opaque and cannot be directly scored, so the cross-route comparison uses only the three real continuation answers after each compaction.

| Route | Continuation scores (6) | Median | Mean |
|---|---|---:|---:|
| Native V2 | 87, 87, 88, 97, 98, 99 | 92.5 | 92.7 |
| Local compact | 71, 77, 78, 82, 85, 86 | 80.0 | 79.8 |
| Bridge V2 | 60, 65, 67, 68, 83, 86 | 67.5 | 71.5 |

All nine compactions and all 27 continuation turns completed successfully. Native V2 was materially stronger on this sample: two runs retained the first GLM follow-up `67×71=4757` and produced the correct new acceptance decision. Both plaintext-summary paths consistently omitted that first-round calculation. Bridge V2 additionally produced two continuations that incorrectly required DeepSeek to be retested, despite the summary retaining its success token.

This comparison measures the real product paths, not a pure compaction-algorithm effect: native V2 used `gpt-5.4`, while Bridge/local used DeepSeek. The earlier direct summary-body evaluation remains the Bridge-specific result—Bridge V2 median 85 versus local 83.5—because it removes downstream answer variance. The new continuation result shows that native opaque state plus GPT gives substantially better practical recall and inference than either plaintext-summary path for this history; it does not isolate how much of the gain comes from native compaction versus the model.
