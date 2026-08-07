package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestNormalizeForReplayNoCompaction(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":[{"type":"message","role":"user","content":"hello"}]}`)
	result, err := normalizeForReplay(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Normalized {
		t.Fatal("expected normalized=true")
	}
}

func TestNormalizeForReplayScalarInput(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-flash","input":"Reply only PONG."}`)
	result, err := normalizeForReplay(body)
	if err != nil {
		t.Fatalf("scalar input must pass through unchanged: %v", err)
	}
	if !result.Normalized {
		t.Fatal("expected normalized=true")
	}
	if string(result.Body) != string(body) {
		t.Fatalf("scalar input changed: got %s", result.Body)
	}
}

func TestNormalizeInterceptedReplay(t *testing.T) {
	cfg, err := loadConfig([]byte("rules:\n  - match: bridge-*\n    action: bridge\n"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	req := pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-response",
		RequestedModel: "bridge-test",
		Body:           []byte(`{"input":[{"type":"compaction","id":"cpa_compact_test","encrypted_content":"summary"},{"type":"message","role":"user","content":"continue"}]}`),
	}
	response := normalizeInterceptedReplay(cfg, req)
	if response.Terminate || !strings.Contains(string(response.Body), `"role":"user"`) || strings.Contains(string(response.Body), `"type":"compaction"`) {
		t.Fatalf("unexpected interceptor response: %+v", response)
	}
}

func TestNormalizeInterceptedReplayRejectsOpaqueState(t *testing.T) {
	cfg, err := loadConfig([]byte("rules:\n  - match: bridge-*\n    action: bridge\n"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-response",
		RequestedModel: "bridge-test",
		Body:           []byte(`{"input":[{"type":"compaction","id":"opaque","encrypted_content":"blob"}]}`),
	})
	if !response.Terminate || response.StatusCode != 502 || !strings.Contains(string(response.ResponseBody), errCodeCompactBridgeFailed) {
		t.Fatalf("opaque compact state must fail closed: %+v", response)
	}
}

func TestNormalizeForReplayCPACompaction(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":[` +
		`{"type":"message","role":"user","content":"original question"},` +
		`{"type":"compaction","id":"cpa_compact_abc-123","encrypted_content":"prior summary text"},` +
		`{"type":"message","role":"user","content":"follow up"}` +
		`]}`)
	result, err := normalizeForReplay(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Normalized {
		t.Fatal("expected normalized=true")
	}
	var parsed struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(result.Body, &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(parsed.Input) != 3 {
		t.Fatalf("expected 3 items, got %d", len(parsed.Input))
	}
	// Item 1 should now be a user message
	if parsed.Input[1]["role"] != "user" {
		t.Fatalf("expected compaction replaced by user message, got role=%v", parsed.Input[1]["role"])
	}
	if parsed.Input[1]["content"] != "prior summary text" {
		t.Fatalf("expected content=prior summary text, got %v", parsed.Input[1]["content"])
	}
}

func TestNormalizeForReplayUnknownCompactionFailClosed(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":[` +
		`{"type":"compaction","id":"other_provider_compact_xyz","encrypted_content":"opaque"}` +
		`]}`)
	_, err := normalizeForReplay(body)
	if err == nil {
		t.Fatal("expected fail-closed error for unknown compaction")
	}
	if !strings.Contains(err.Error(), "unknown compaction item") {
		t.Fatalf("expected unknown compaction item error, got: %v", err)
	}
}

func TestNormalizeForReplayEmptyEncryptedContent(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":[` +
		`{"type":"compaction","id":"cpa_compact_abc","encrypted_content":""}` +
		`]}`)
	_, err := normalizeForReplay(body)
	if err == nil {
		t.Fatal("expected error for empty encrypted_content")
	}
}

func TestNormalizeForReplayNoInputArray(t *testing.T) {
	body := []byte(`{"model":"glm-4.7"}`)
	result, err := normalizeForReplay(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Normalized {
		t.Fatal("expected normalized=true")
	}
}

func mustBridgeConfig(t *testing.T, yamlBody string) Config {
	t.Helper()
	cfg, err := loadConfig([]byte(yamlBody))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

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

func TestNormalizeForReplayPreservesNonInputFields(t *testing.T) {
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
	result, err := normalizeForReplay(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Normalized {
		t.Fatal("expected normalized=true")
	}
	rewritten := decodeReplayBody(t, result.Body)
	for _, key := range []string{"model", "stream", "instructions", "metadata"} {
		if string(rewritten[key]) != string(decodeReplayBody(t, body)[key]) {
			t.Fatalf("non-input field %q changed: got %s want %s", key, rewritten[key], decodeReplayBody(t, body)[key])
		}
	}
	items := decodeInputItems(t, result.Body)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0]["role"] != "user" || items[0]["content"] != "q" || items[0]["type"] != "message" {
		t.Fatalf("non-compaction item changed: %v", items[0])
	}
	if items[1]["type"] != "message" || items[1]["role"] != "user" || items[1]["content"] != "prior summary" {
		t.Fatalf("expected compaction replaced by user message, got %v", items[1])
	}
	if items[2]["role"] != "user" || items[2]["content"] != "follow up" || items[2]["type"] != "message" {
		t.Fatalf("trailing non-compaction item changed: %v", items[2])
	}
}

func TestNormalizeForReplayObjectInputPassThrough(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":{"type":"message","role":"user","content":"hi"}}`)
	result, err := normalizeForReplay(body)
	if err != nil {
		t.Fatalf("object input must pass through unchanged: %v", err)
	}
	if !result.Normalized {
		t.Fatal("expected normalized=true")
	}
	if string(result.Body) != string(body) {
		t.Fatalf("object input changed: got %s", result.Body)
	}
}

func TestNormalizeForReplayNullInputPassThrough(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":null}`)
	result, err := normalizeForReplay(body)
	if err != nil {
		t.Fatalf("null input must pass through unchanged: %v", err)
	}
	if !result.Normalized {
		t.Fatal("expected normalized=true")
	}
	if string(result.Body) != string(body) {
		t.Fatalf("null input changed: got %s", result.Body)
	}
}

func TestNormalizeForReplayEmptyInputArrayPassThrough(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":[]}`)
	result, err := normalizeForReplay(body)
	if err != nil {
		t.Fatalf("empty input array must pass through unchanged: %v", err)
	}
	if !result.Normalized {
		t.Fatal("expected normalized=true")
	}
	if string(result.Body) != string(body) {
		t.Fatalf("empty input array changed: got %s", result.Body)
	}
}

func TestNormalizeForReplayInvalidJSONFails(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"model":`),
		[]byte(`not-json`),
		[]byte(``),
	} {
		if _, err := normalizeForReplay(body); err == nil {
			t.Fatalf("expected error for body %q", body)
		}
	}
}

func TestNormalizeForReplayNonObjectItemsPreserved(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":[` +
		`"raw string item",` +
		`{"type":"message","role":"user","content":"q"},` +
		`{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"summary"}` +
		`]}`)
	result, err := normalizeForReplay(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Normalized {
		t.Fatal("expected normalized=true")
	}
	parsed := decodeReplayBody(t, result.Body)
	var rawItems []json.RawMessage
	if err := json.Unmarshal(parsed["input"], &rawItems); err != nil {
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

func TestNormalizeInterceptedReplayNonBridgeModelNoOp(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-response",
		RequestedModel: "other-model",
		Body:           []byte(`{"input":[{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"summary"}]}`),
	})
	requireNoOp(t, response)
}

func TestNormalizeInterceptedReplayPassthroughRuleNoOp(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: pass-*\n    action: passthrough\n")
	response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-response",
		RequestedModel: "pass-x",
		Body:           []byte(`{"input":[{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"summary"}]}`),
	})
	requireNoOp(t, response)
}

func TestNormalizeInterceptedReplayWrongSourceFormatNoOp(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-chat",
		RequestedModel: "bridge-test",
		Body:           []byte(`{"input":[{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"summary"}]}`),
	})
	requireNoOp(t, response)
}

func TestNormalizeInterceptedReplayNoCompactionNoOp(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-response",
		RequestedModel: "bridge-test",
		Body:           []byte(`{"input":[{"type":"message","role":"user","content":"hello"}]}`),
	})
	requireNoOp(t, response)
}

func TestNormalizeInterceptedReplayScalarInputNoOp(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-response",
		RequestedModel: "bridge-test",
		Body:           []byte(`{"input":"Reply only PONG."}`),
	})
	requireNoOp(t, response)
}

func TestNormalizeInterceptedReplayObjectInputNoOp(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-response",
		RequestedModel: "bridge-test",
		Body:           []byte(`{"input":{"type":"message","role":"user","content":"hi"}}`),
	})
	requireNoOp(t, response)
}

func TestNormalizeInterceptedReplayNullInputNoOp(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-response",
		RequestedModel: "bridge-test",
		Body:           []byte(`{"input":null}`),
	})
	requireNoOp(t, response)
}

func TestNormalizeInterceptedReplayInvalidBodyNoOp(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	for _, body := range [][]byte{[]byte(`{invalid`), []byte(``)} {
		response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
			SourceFormat:   "openai-response",
			RequestedModel: "bridge-test",
			Body:           body,
		})
		requireNoOp(t, response)
	}
}

func TestNormalizeInterceptedReplayRewritesAndPreservesFields(t *testing.T) {
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
	response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-response",
		RequestedModel: "bridge-test",
		Body:           body,
	})
	if response.Terminate {
		t.Fatalf("expected rewrite, got terminate: %+v", response)
	}
	if len(response.Body) == 0 {
		t.Fatal("expected rewritten Body")
	}
	rewritten := decodeReplayBody(t, response.Body)
	for _, key := range []string{"model", "stream", "instructions"} {
		if string(rewritten[key]) != string(decodeReplayBody(t, body)[key]) {
			t.Fatalf("non-input field %q changed: got %s want %s", key, rewritten[key], decodeReplayBody(t, body)[key])
		}
	}
	items := decodeInputItems(t, response.Body)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[1]["type"] != "message" || items[1]["role"] != "user" || items[1]["content"] != "summary" {
		t.Fatalf("expected compaction replaced by user message, got %v", items[1])
	}
}

func TestNormalizeInterceptedReplayUnknownCompactionFailClosed(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
		SourceFormat:   "openai-response",
		RequestedModel: "bridge-test",
		Body:           []byte(`{"input":[{"type":"compaction","id":"other_compact","encrypted_content":"opaque"}]}`),
	})
	if !response.Terminate {
		t.Fatalf("expected terminate for unknown compaction: %+v", response)
	}
	if response.StatusCode != 502 {
		t.Fatalf("expected 502, got %d", response.StatusCode)
	}
	if !strings.Contains(string(response.ResponseBody), errCodeCompactBridgeFailed) {
		t.Fatalf("expected %q in response body, got %s", errCodeCompactBridgeFailed, response.ResponseBody)
	}
	if response.ResponseHeaders.Get("Content-Type") != "application/json" {
		t.Fatalf("expected json content type, got %v", response.ResponseHeaders)
	}
}

func TestNormalizeInterceptedReplayEmptyCPACompactionFailClosed(t *testing.T) {
	cfg := mustBridgeConfig(t, "rules:\n  - match: bridge-*\n    action: bridge\n")
	for _, content := range []string{"", "   "} {
		body := []byte(`{"input":[{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"` + content + `"}]}`)
		response := normalizeInterceptedReplay(cfg, pluginapi.RequestInterceptRequest{
			SourceFormat:   "openai-response",
			RequestedModel: "bridge-test",
			Body:           body,
		})
		if !response.Terminate || response.StatusCode != 502 || !strings.Contains(string(response.ResponseBody), errCodeCompactBridgeFailed) {
			t.Fatalf("expected fail-closed 502 for content %q, got %+v", content, response)
		}
	}
}
