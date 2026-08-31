package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func mustBridgeConfig(t *testing.T, yamlBody string) Config {
	t.Helper()
	cfg, err := loadConfig([]byte(yamlBody))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

// allCompactTargets lists every target the Compaction State Policy knows, in
// strictness order from most permissive to most restrictive.
var allCompactTargets = []compactTarget{targetExplicitPassthrough, targetUnmatchedPassthrough, targetBridge}

func decodeReplayBody(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal rewritten body: %v", err)
	}
	return parsed
}

func decodeInputItems(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	parsed := decodeReplayBody(t, body)
	var items []map[string]any
	if err := json.Unmarshal(parsed["input"], &items); err != nil {
		t.Fatalf("unmarshal rewritten input: %v", err)
	}
	return items
}

func requireNoOp(t *testing.T, response pluginapi.RequestInterceptResponse) {
	t.Helper()
	if response.Terminate {
		t.Fatalf("expected no-op, got terminate: %+v", response)
	}
	if len(response.Body) != 0 {
		t.Fatalf("expected no-op with empty Body, got %s", response.Body)
	}
	if len(response.ResponseBody) != 0 {
		t.Fatalf("expected no-op with empty ResponseBody, got %s", response.ResponseBody)
	}
}

func requireForwardedUnchanged(t *testing.T, result replayResult, body []byte) {
	t.Helper()
	if result.Changed {
		t.Fatalf("expected the request to be forwarded as received, got rewritten body: %s", result.Body)
	}
	if string(result.Body) != string(body) {
		t.Fatalf("unchanged result modified the body: got %s want %s", result.Body, body)
	}
}

// requireFailClosed asserts the deterministic client-error contract shared by
// every replay and generation path.
func requireFailClosed(t *testing.T, err error, wantMessage string) {
	t.Helper()
	pluginErrValue := mustPluginErr(t, err, errCodeInvalidCompactionState)
	if pluginErrValue.HTTPStatus != 400 {
		t.Fatalf("invalid_compaction_state must be a client error, got status %d", pluginErrValue.HTTPStatus)
	}
	if pluginErrValue.Message != wantMessage {
		t.Fatalf("message = %q, want %q", pluginErrValue.Message, wantMessage)
	}
}

func TestNormalizeCompactionStateNoCompaction(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":[{"type":"message","role":"user","content":"hello"}]}`)
	for _, target := range allCompactTargets {
		result, err := normalizeCompactionState(body, target)
		if err != nil {
			t.Fatalf("target %s: unexpected error: %v", target, err)
		}
		requireForwardedUnchanged(t, result, body)
	}
}

// TestNormalizeCompactionStateLeavesReasoningUntouched pins requirement 1: the
// policy reads only the compaction item type and never a reasoning item, even
// though both carry an encrypted_content field.
func TestNormalizeCompactionStateLeavesReasoningUntouched(t *testing.T) {
	reasoning := `{"type":"reasoning","id":"rs_123","encrypted_content":"opaque reasoning state","summary":[]}`
	body := []byte(`{"model":"glm-4.7","input":[` +
		`{"type":"message","role":"user","content":"original"},` + reasoning + `]}`)
	for _, target := range allCompactTargets {
		result, err := normalizeCompactionState(body, target)
		if err != nil {
			t.Fatalf("target %s: reasoning-only history must not be touched: %v", target, err)
		}
		requireForwardedUnchanged(t, result, body)
	}
	// A reasoning item must also survive byte-for-byte next to a normalized
	// compaction item.
	mixed := []byte(`{"model":"glm-4.7","input":[` + reasoning +
		`,{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"summary"}]}`)
	result, err := normalizeCompactionState(mixed, targetBridge)
	if err != nil {
		t.Fatalf("normalize alongside reasoning: %v", err)
	}
	if !result.Changed || !strings.Contains(string(result.Body), "opaque reasoning state") {
		t.Fatalf("reasoning item changed during normalization: %s", result.Body)
	}
	items := decodeInputItems(t, result.Body)
	if len(items) != 2 || items[0]["type"] != "reasoning" || items[0]["encrypted_content"] != "opaque reasoning state" {
		t.Fatalf("reasoning item rewritten: %s", result.Body)
	}
}

// TestNormalizeCompactionStateDecouplesBridgeStateFromTarget is requirement 3:
// valid plaintext state normalizes for a bridge target and a native passthrough
// target alike.
func TestNormalizeCompactionStateDecouplesBridgeStateFromTarget(t *testing.T) {
	body := []byte(`{"model":"any-target","input":[` +
		`{"type":"message","role":"user","content":"original question"},` +
		`{"type":"compaction","id":"cpa_compact_abc-123","encrypted_content":"prior summary text"},` +
		`{"type":"message","role":"user","content":"follow up"}` +
		`]}`)
	var normalized []byte
	for _, target := range allCompactTargets {
		result, err := normalizeCompactionState(body, target)
		if err != nil {
			t.Fatalf("target %s: %v", target, err)
		}
		if !result.Changed {
			t.Fatalf("target %s: valid bridge state must normalize", target)
		}
		items := decodeInputItems(t, result.Body)
		if len(items) != 3 {
			t.Fatalf("target %s: expected 3 items, got %d", target, len(items))
		}
		if items[1]["type"] != "message" || items[1]["role"] != "user" || items[1]["content"] != "prior summary text" {
			t.Fatalf("target %s: expected compaction replaced by user summary, got %v", target, items[1])
		}
		if strings.Contains(string(result.Body), "cpa_compact_") {
			t.Fatalf("target %s: bridge marker leaked downstream: %s", target, result.Body)
		}
		if normalized == nil {
			normalized = result.Body
		} else if string(normalized) != string(result.Body) {
			t.Fatalf("normalization depends on the target rule: bridge=%s passthrough=%s", normalized, result.Body)
		}
	}
}

// TestNormalizeCompactionStateOpaqueNeedsExplicitPassthroughRule is requirement
// 4: opaque native state continues unchanged only on a target whose Bridge Rule
// explicitly declares the route native-compatible, and fails closed on every
// other target. An unmatched model used to forward such state blindly, which
// let the plugin vouch for a route it never configured.
func TestNormalizeCompactionStateOpaqueNeedsExplicitPassthroughRule(t *testing.T) {
	bodies := map[string][]byte{
		"ordinary turn": []byte(`{"model":"glm-4.7","input":[{"type":"compaction","id":"cmp_9f8e","encrypted_content":"opaque"}]}`),
		"v2 trigger":    []byte(`{"model":"glm-4.7","stream":true,"input":[{"type":"compaction","id":"cmp_9f8e","encrypted_content":"opaque native state"},{"type":"compaction_trigger"}]}`),
		"no id":         []byte(`{"model":"glm-4.7","input":[{"type":"compaction","encrypted_content":"opaque"}]}`),
		"bare marker":   []byte(`{"model":"glm-4.7","input":[{"type":"compaction","id":"cpa_compact_","encrypted_content":"opaque"}]}`),
		"other marker":  []byte(`{"model":"glm-4.7","input":[{"type":"compaction","id":"other_provider_compact_xyz","encrypted_content":"opaque"}]}`),
	}
	rejections := map[compactTarget]string{
		targetBridge:               msgNativeCompactionOnBridge,
		targetUnmatchedPassthrough: msgUnruledNativeCompaction,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			result, err := normalizeCompactionState(body, targetExplicitPassthrough)
			if err != nil {
				t.Fatalf("declared native route must not error: %v", err)
			}
			requireForwardedUnchanged(t, result, body)
			for _, target := range allCompactTargets {
				wantMessage, rejected := rejections[target]
				if !rejected {
					continue
				}
				_, err := normalizeCompactionState(body, target)
				requireFailClosed(t, err, wantMessage)
			}
		})
	}
}

// TestNormalizeCompactionStateMixedFailsClosedOnEveryTarget is requirement 5.
func TestNormalizeCompactionStateMixedFailsClosedOnEveryTarget(t *testing.T) {
	body := []byte(`{"model":"any-target","input":[` +
		`{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"readable summary"},` +
		`{"type":"compaction","id":"cmp_9f8e","encrypted_content":"opaque"}]}`)
	for _, target := range allCompactTargets {
		_, err := normalizeCompactionState(body, target)
		requireFailClosed(t, err, msgMixedCompactionState)
	}
}

// TestNormalizeCompactionStateCorruptBridgeStateFailsClosed is requirement 6:
// missing, empty, or whitespace-only summary text is corrupt state.
func TestNormalizeCompactionStateCorruptBridgeStateFailsClosed(t *testing.T) {
	for name, item := range map[string]string{
		"missing field": `{"type":"compaction","id":"cpa_compact_abc"}`,
		"empty string":  `{"type":"compaction","id":"cpa_compact_abc","encrypted_content":""}`,
		"spaces":        `{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"   "}`,
		"newlines":      `{"type":"compaction","id":"cpa_compact_abc","encrypted_content":" \t\n "}`,
		"null":          `{"type":"compaction","id":"cpa_compact_abc","encrypted_content":null}`,
	} {
		for _, target := range allCompactTargets {
			body := []byte(`{"model":"any-target","input":[` + item + `]}`)
			t.Run(name, func(t *testing.T) {
				_, err := normalizeCompactionState(body, target)
				requireFailClosed(t, err, msgCorruptBridgeCompaction)
			})
		}
	}
}

func TestNormalizeCompactionStateScalarInputPassThrough(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-flash","input":"Reply only PONG."}`)
	result, err := normalizeCompactionState(body, targetBridge)
	if err != nil {
		t.Fatalf("scalar input must pass through unchanged: %v", err)
	}
	requireForwardedUnchanged(t, result, body)
}

func TestNormalizeCompactionStateObjectInputPassThrough(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":{"type":"message","role":"user","content":"hi"}}`)
	result, err := normalizeCompactionState(body, targetBridge)
	if err != nil {
		t.Fatalf("object input must pass through unchanged: %v", err)
	}
	requireForwardedUnchanged(t, result, body)
}

func TestNormalizeCompactionStateNullAndEmptyInputPassThrough(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"model":"glm-4.7","input":null}`),
		[]byte(`{"model":"glm-4.7","input":[]}`),
		[]byte(`{"model":"glm-4.7"}`),
	} {
		result, err := normalizeCompactionState(body, targetBridge)
		if err != nil {
			t.Fatalf("body %s must pass through: %v", body, err)
		}
		requireForwardedUnchanged(t, result, body)
	}
}

func TestNormalizeCompactionStateInvalidJSONFails(t *testing.T) {
	for _, body := range [][]byte{[]byte(`{"model":`), []byte(`not-json`), []byte(``)} {
		if _, err := normalizeCompactionState(body, targetBridge); err == nil {
			t.Fatalf("expected error for body %q", body)
		} else if asInvalidCompactionState(err) != nil {
			t.Fatalf("a malformed body is not a compaction state error: %v", err)
		}
	}
}

func TestNormalizeCompactionStatePreservesNonInputFields(t *testing.T) {
	body := []byte(`{
		"model":"glm-4.7",
		"stream":true,
		"instructions":"keep me",
		"metadata":{"attempt":2,"tags":["a","b"]},
		"input":[
			{"type":"message","role":"user","content":"q"},
			{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"prior summary"},
			{"type":"message","role":"user","content":"follow up"}
		]
	}`)
	result, err := normalizeCompactionState(body, targetBridge)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected rewritten body")
	}
	rewritten := decodeReplayBody(t, result.Body)
	original := decodeReplayBody(t, body)
	for _, key := range []string{"model", "stream", "instructions", "metadata"} {
		if string(rewritten[key]) != string(original[key]) {
			t.Fatalf("non-input field %q changed: got %s want %s", key, rewritten[key], original[key])
		}
	}
	items := decodeInputItems(t, result.Body)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0]["type"] != "message" || items[0]["role"] != "user" || items[0]["content"] != "q" {
		t.Fatalf("leading non-compaction item changed: %v", items[0])
	}
	if items[1]["type"] != "message" || items[1]["role"] != "user" || items[1]["content"] != "prior summary" {
		t.Fatalf("expected compaction replaced by user message, got %v", items[1])
	}
	if items[2]["type"] != "message" || items[2]["content"] != "follow up" {
		t.Fatalf("trailing non-compaction item changed: %v", items[2])
	}
}

func TestNormalizeCompactionStateNonObjectItemsPreserved(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":[` +
		`"raw string item",` +
		`{"type":"message","role":"user","content":"q"},` +
		`{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"summary"}` +
		`]}`)
	result, err := normalizeCompactionState(body, targetBridge)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected rewritten body")
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(decodeReplayBody(t, result.Body)["input"], &rawItems); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	if len(rawItems) != 3 {
		t.Fatalf("expected 3 items, got %d", len(rawItems))
	}
	var first string
	if err := json.Unmarshal(rawItems[0], &first); err != nil || first != "raw string item" {
		t.Fatalf("non-object item changed: %s", rawItems[0])
	}
	var compact map[string]any
	if err := json.Unmarshal(rawItems[2], &compact); err != nil {
		t.Fatalf("unmarshal item 2: %v", err)
	}
	if compact["type"] != "message" || compact["role"] != "user" || compact["content"] != "summary" {
		t.Fatalf("expected compaction replaced, got %v", compact)
	}
}

// --- Request interceptor policy ---

func interceptRequest(model, body string) pluginapi.RequestInterceptRequest {
	return pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-response",
		RequestedModel: model,
		Body:           []byte(body),
	}
}

func TestNormalizeInterceptedReplayBridgedRewritesAndPreservesFields(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	body := []byte(`{
		"model":"glm-4.7",
		"stream":false,
		"instructions":"keep",
		"input":[
			{"type":"message","role":"user","content":"q"},
			{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"summary"}
		]
	}`)
	response := normalizeInterceptedReplay(cfg, interceptRequest("bridge-test", string(body)))
	if response.Terminate {
		t.Fatalf("expected rewrite, got terminate: %+v", response)
	}
	if len(response.Body) == 0 {
		t.Fatal("expected rewritten Body")
	}
	rewritten := decodeReplayBody(t, response.Body)
	original := decodeReplayBody(t, body)
	for _, key := range []string{"model", "stream", "instructions"} {
		if string(rewritten[key]) != string(original[key]) {
			t.Fatalf("non-input field %q changed: got %s want %s", key, rewritten[key], original[key])
		}
	}
	items := decodeInputItems(t, response.Body)
	if len(items) != 2 || items[1]["type"] != "message" || items[1]["role"] != "user" || items[1]["content"] != "summary" {
		t.Fatalf("expected compaction replaced by user message, got %s", response.Body)
	}
}

// TestNormalizeInterceptedReplayNormalizesOnEveryTarget covers the two rule
// shapes that carry plugin state: an explicit passthrough rule and no rule at
// all (on_no_match).
func TestNormalizeInterceptedReplayNormalizesOnEveryTarget(t *testing.T) {
	cases := map[string]struct {
		cfg   string
		model string
	}{
		"explicit passthrough rule": {"rules:\n  - match: pass-*\n    action: passthrough\n", "pass-x"},
		"no matching rule":          {"rules:\n  - match: bridge-*\n    action: bridge\n", "gpt-5.4"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			response := normalizeInterceptedReplay(mustBridgeConfig(t, tc.cfg),
				interceptRequest(tc.model, `{"input":[{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"summary"}]}`))
			if response.Terminate || !strings.Contains(string(response.Body), `"role":"user"`) ||
				strings.Contains(string(response.Body), `"type":"compaction"`) {
				t.Fatalf("bridge state must normalize to a user summary: %+v", response)
			}
		})
	}
}

func TestNormalizeInterceptedReplayNativeStateOnBridgeTargetFailsClosed(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	for name, body := range map[string]string{
		"opaque state":     `{"input":[{"type":"compaction","id":"other_compact","encrypted_content":"opaque"}]}`,
		"corrupt state":    `{"input":[{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"  "}]}`,
		"mixed state":      `{"input":[{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"summary"},{"type":"compaction","id":"cmp_1","encrypted_content":"opaque"}]}`,
		"v2 trigger state": `{"stream":true,"input":[{"type":"compaction","id":"cmp_1","encrypted_content":"opaque"},{"type":"compaction_trigger"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := normalizeInterceptedReplay(cfg, interceptRequest("bridge-test", body))
			if !response.Terminate {
				t.Fatalf("expected fail closed: %+v", response)
			}
			if response.StatusCode != 400 {
				t.Fatalf("status = %d, want 400", response.StatusCode)
			}
			if response.ResponseHeaders.Get("Content-Type") != "application/json" {
				t.Fatalf("content-type = %v", response.ResponseHeaders)
			}
			var decoded struct {
				Error struct {
					Type string `json:"type"`
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.ResponseBody, &decoded); err != nil {
				t.Fatalf("decode body %s: %v", response.ResponseBody, err)
			}
			if decoded.Error.Code != errCodeInvalidCompactionState || decoded.Error.Type != "invalid_request_error" {
				t.Fatalf("error shape = %s", response.ResponseBody)
			}
			if !strings.Contains(string(response.ResponseBody), "start a new session") &&
				!strings.Contains(string(response.ResponseBody), "switch back to the model") {
				t.Fatalf("fail-closed body must name the remedy: %s", response.ResponseBody)
			}
		})
	}
}

// TestNormalizeInterceptedReplayOpaqueStateNeedsExplicitPassthroughRule is the
// interceptor-level fail-closed regression: the target is read from the rules,
// so only a model matched by an explicit `passthrough` rule may carry opaque
// state onward, while both a `bridge` rule and no rule at all reject it.
func TestNormalizeInterceptedReplayOpaqueStateNeedsExplicitPassthroughRule(t *testing.T) {
	const body = `{"input":[{"type":"compaction","id":"cmp_1","encrypted_content":"opaque"},{"type":"reasoning","id":"rs_1","encrypted_content":"opaque reasoning"}]}`
	cases := []struct {
		name        string
		rules       string
		model       string
		wantMessage string
	}{
		{
			name:        "explicit passthrough rule",
			rules:       "rules:\n  - match: bridge-*\n    action: bridge\n  - match: gpt-*\n    action: passthrough\n",
			model:       "gpt-5.4",
			wantMessage: "",
		},
		{
			name:        "bridge rule",
			rules:       "rules:\n  - match: bridge-*\n    action: bridge\n  - match: gpt-*\n    action: passthrough\n",
			model:       "bridge-test",
			wantMessage: msgNativeCompactionOnBridge,
		},
		{
			name:        "no matching rule",
			rules:       "rules:\n  - match: bridge-*\n    action: bridge\n  - match: gpt-*\n    action: passthrough\n",
			model:       "unruled-model",
			wantMessage: msgUnruledNativeCompaction,
		},
		{
			name:        "no rules at all",
			rules:       "",
			model:       "gpt-5.4",
			wantMessage: msgUnruledNativeCompaction,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := normalizeInterceptedReplay(mustBridgeConfig(t, tc.rules), interceptRequest(tc.model, body))
			if tc.wantMessage == "" {
				requireNoOp(t, response)
				return
			}
			if !response.Terminate || response.StatusCode != 400 {
				t.Fatalf("opaque state on %s must return 400, got %+v", tc.name, response)
			}
			if len(response.Body) != 0 {
				t.Fatalf("a rejected request must not forward a rewritten body: %s", response.Body)
			}
			if !strings.Contains(string(response.ResponseBody), tc.wantMessage) {
				t.Fatalf("message for %s = %s", tc.name, response.ResponseBody)
			}
		})
	}
}

func TestNormalizeInterceptedReplayNoOpCases(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	t.Run("wrong source format", func(t *testing.T) {
		req := interceptRequest("bridge-test", `{"input":[{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"summary"}]}`)
		req.SourceFormat = "openai-chat"
		requireNoOp(t, normalizeInterceptedReplay(cfg, req))
	})
	for name, body := range map[string]string{
		"no compaction": `{"input":[{"type":"message","role":"user","content":"hello"}]}`,
		"scalar input":  `{"input":"Reply only PONG."}`,
		"object input":  `{"input":{"type":"message","role":"user","content":"hi"}}`,
		"null input":    `{"input":null}`,
		"invalid body":  `{invalid`,
		"empty body":    ``,
	} {
		t.Run(name, func(t *testing.T) {
			requireNoOp(t, normalizeInterceptedReplay(cfg, interceptRequest("bridge-test", body)))
		})
	}
}
