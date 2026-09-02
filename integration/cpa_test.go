package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	bridgeModel  = "bridge-test"
	summaryModel = "summary-test"
	nativeModel  = "native-test"
	accessKey    = "integration-key"
)

type upstreamRequest struct {
	Path string
	Body []byte
}

type fakeUpstream struct {
	server          *httptest.Server
	mu              sync.Mutex
	calls           []upstreamRequest
	failRequests    bool
	blankSummary    bool
	toolCallSummary bool
}

func newFakeUpstream() *fakeUpstream {
	f := &fakeUpstream{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		f.mu.Lock()
		f.calls = append(f.calls, upstreamRequest{Path: r.URL.Path, Body: append([]byte(nil), body...)})
		shouldFail := f.failRequests
		blankSummary := f.blankSummary
		toolCallSummary := f.toolCallSummary
		f.mu.Unlock()

		if shouldFail {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"simulated upstream failure","type":"server_error"}}`)
			return
		}

		var request struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.Unmarshal(body, &request)
		text := "delegated ordinary response"
		if strings.Contains(string(body), "You are performing a CONTEXT CHECKPOINT COMPACTION") {
			text = "fixture compact summary"
			if blankSummary {
				text = "   \n "
			}
			if toolCallSummary {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"chatcmpl_fake","object":"chat.completion","model":"`+request.Model+`","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_compact","type":"function","function":{"name":"search","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
				return
			}
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl_fake","object":"chat.completion.chunk","model":"`+request.Model+`","choices":[{"index":0,"delta":{"role":"assistant","content":"`+text+`"},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl_fake","object":"chat.completion.chunk","model":"`+request.Model+`","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl_fake","object":"chat.completion","model":"`+request.Model+`","choices":[{"index":0,"message":{"role":"assistant","content":"`+text+`"},"finish_reason":"stop"}]}`)
	}))
	return f
}

func (f *fakeUpstream) close() { f.server.Close() }

func (f *fakeUpstream) snapshot() []upstreamRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]upstreamRequest(nil), f.calls...)
}

func TestSummaryRequestUsesCodexLocalCompactPrompt(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses/compact", fixture(t, "v1-compact-with-tools.json"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("V1 compact status = %d, body=%s", response.StatusCode, response.Body)
	}
	calls := h.upstream.snapshot()
	if len(calls) != 1 {
		t.Fatalf("summary upstream calls = %d, want 1", len(calls))
	}
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(calls[0].Body, &request); err != nil {
		t.Fatalf("decode summary request: %v", err)
	}
	if len(request.Messages) == 0 || request.Messages[len(request.Messages)-1].Role != "user" || request.Messages[len(request.Messages)-1].Content != "You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM that will resume the task.\n\nInclude:\n- Current progress and key decisions made\n- Important context, constraints, or user preferences\n- What remains to be done (clear next steps)\n- Any critical data, examples, or references needed to continue\n\nBe concise, structured, and focused on helping the next LLM seamlessly continue the work.\nDo not answer the user. Do not call tools. Output only the continuation summary." {
		t.Fatalf("summary request did not use the Codex local compact prompt: messages=%+v body=%s", request.Messages, calls[0].Body)
	}
	var translated map[string]json.RawMessage
	if err := json.Unmarshal(calls[0].Body, &translated); err != nil {
		t.Fatalf("decode translated summary request: %v", err)
	}
	if string(translated["stream"]) != "false" || len(translated["max_tokens"]) == 0 {
		t.Fatalf("summary request did not preserve non-stream token cap: %s", calls[0].Body)
	}
	for _, field := range []string{"tools", "tool_choice", "parallel_tool_calls"} {
		if _, ok := translated[field]; ok {
			t.Fatalf("CPA forwarded tool configuration %q from summary request: %s", field, calls[0].Body)
		}
	}
}

func (f *fakeUpstream) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *fakeUpstream) setFailure() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failRequests = true
}

func (f *fakeUpstream) clearFailure() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failRequests = false
}

// setBlankSummary makes the summary model answer with whitespace only, which is
// a generated compaction state the bridge must refuse to persist.
func (f *fakeUpstream) setBlankSummary() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blankSummary = true
}

func (f *fakeUpstream) setToolCallSummary() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolCallSummary = true
}

type harness struct {
	baseURL  string
	upstream *fakeUpstream
	command  *exec.Cmd
	logs     bytes.Buffer
}

// bridgeRuleYAML is the rule block used by most integration tests: one `bridge`
// rule, so every other model, including native-test, is an unruled route.
func bridgeRuleYAML() string {
	return fmt.Sprintf(`      rules:
        - match: %q
          action: bridge
          summary_model: %q
`, bridgeModel, summaryModel)
}

// declaredNativeRuleYAML adds an explicit `passthrough` rule for native-test.
// That rule is the administrator's declaration that the route can interpret its
// own opaque compaction state; the plugin cannot verify which route produced it.
func declaredNativeRuleYAML() string {
	return bridgeRuleYAML() + fmt.Sprintf(`        - match: %q
          action: passthrough
`, nativeModel)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWithRules(t, bridgeRuleYAML())
}

func newHarnessWithRules(t *testing.T, ruleYAML string) *harness {
	t.Helper()
	bridgeRoot := bridgeRoot(t)
	cpaSource := cpaSource(t)
	temp := t.TempDir()
	upstream := newFakeUpstream()
	t.Cleanup(upstream.close)

	pluginDir := filepath.Join(temp, "plugins", runtime.GOOS, runtime.GOARCH)
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("create plugin discovery directory: %v", err)
	}
	ext := sharedLibraryExtension()
	pluginBinary := filepath.Join(pluginDir, "cpa-codex-compact-bridge"+ext)
	runGo(t, filepath.Join(bridgeRoot, "plugin"), "build", "-buildmode=c-shared", "-o", pluginBinary, ".")
	if err := os.Remove(strings.TrimSuffix(pluginBinary, ext) + ".h"); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove generated c-shared header: %v", err)
	}

	cpaBinary := filepath.Join(temp, "cli-proxy-api")
	runGo(t, cpaSource, "build", "-o", cpaBinary, "./cmd/server")
	port := freePort(t)
	configPath := filepath.Join(temp, "config.yaml")
	config := fmt.Sprintf(`host: "127.0.0.1"
port: %d
auth-dir: %q
api-keys:
  - %q
plugins:
  enabled: true
  dir: %q
  configs:
    cpa-codex-compact-bridge:
      enabled: true
      priority: 100
%sopenai-compatibility:
  - name: integration-fake
    base-url: %q
    api-key-entries:
      - api-key: test-upstream-credential
    models:
      - name: %q
        alias: %q
      - name: %q
        alias: %q
      - name: %q
        alias: %q
`, port, filepath.Join(temp, "auth"), accessKey, filepath.Join(temp, "plugins"), ruleYAML, upstream.server.URL, bridgeModel, bridgeModel, summaryModel, summaryModel, nativeModel, nativeModel)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write CPA config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, cpaBinary, "--config", configPath, "--local-model", "--no-browser")
	cmd.Dir = temp
	// CI runners may report an impractically large CPU count. CPA starts
	// background workers during service boot, so bound the subprocess to keep
	// the harness deterministic and avoid exhausting pthread limits.
	cmd.Env = append(os.Environ(), "GOMAXPROCS=4")
	h := &harness{baseURL: fmt.Sprintf("http://127.0.0.1:%d", port), upstream: upstream, command: cmd}
	cmd.Stdout = &h.logs
	cmd.Stderr = &h.logs
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start CPA: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	waitForCPA(t, h)
	return h
}

func TestNormalHostDelegation(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses", fixture(t, "normal.json"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("normal status = %d, body=%s\nCPA logs:\n%s", response.StatusCode, response.Body, h.logs.String())
	}
	if !strings.Contains(string(response.Body), "delegated ordinary response") {
		t.Fatalf("normal response did not preserve fake upstream response: %s", response.Body)
	}
	calls := h.upstream.snapshot()
	if len(calls) != 1 || !strings.Contains(string(calls[0].Body), `"model":"summary-test"`) {
		t.Fatalf("ordinary request was not delegated exactly once: %#v", calls)
	}
}

func TestV1Compact(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses/compact", fixture(t, "v1-compact.json"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("V1 compact status = %d, body=%s\nCPA logs:\n%s", response.StatusCode, response.Body, h.logs.String())
	}
	if response.Header.Get("Content-Type") == "" || strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("V1 compact content-type = %q, want non-streaming JSON", response.Header.Get("Content-Type"))
	}
	var compacted struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response.Body, &compacted); err != nil {
		t.Fatalf("decode V1 compact response: %v; body=%s", err, response.Body)
	}
	if len(compacted.Output) != 2 {
		t.Fatalf("V1 compact output has %d items, want retained history plus compaction: %s", len(compacted.Output), response.Body)
	}
	var retained struct {
		Type string `json:"type"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(compacted.Output[0], &retained); err != nil {
		t.Fatalf("decode retained V1 item: %v", err)
	}
	if retained.Type != "message" || retained.Role != "user" || !strings.Contains(string(compacted.Output[0]), "history to compact") {
		t.Fatalf("V1 compact did not retain the canonical user history: %s", compacted.Output[0])
	}
	var item struct {
		Type             string `json:"type"`
		ID               string `json:"id"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(compacted.Output[1], &item); err != nil {
		t.Fatalf("decode V1 compaction item: %v", err)
	}
	if item.Type != "compaction" || !strings.HasPrefix(item.ID, "cpa_compact_") || item.EncryptedContent != "fixture compact summary" {
		t.Fatalf("V1 compact item does not match V2's persisted state shape: %+v", item)
	}
	calls := h.upstream.snapshot()
	if len(calls) != 1 || !strings.Contains(string(calls[0].Body), `"model":"summary-test"`) {
		t.Fatalf("V1 compact must call only summary model: %#v", calls)
	}

	// Codex installs the V1 output array as replacement_history. Replaying that
	// exact window must take the same cpa_compact_ normalization path as V2.
	h.upstream.reset()
	continuationBody, err := json.Marshal(map[string]any{
		"model": bridgeModel,
		"input": compacted.Output,
	})
	if err != nil {
		t.Fatalf("encode V1 continuation: %v", err)
	}
	continuation := h.post(t, "/v1/responses", continuationBody)
	if continuation.StatusCode != http.StatusOK {
		t.Fatalf("V1 continuation status = %d, body=%s\nCPA logs:\n%s", continuation.StatusCode, continuation.Body, h.logs.String())
	}
	calls = h.upstream.snapshot()
	if len(calls) != 1 {
		t.Fatalf("V1 continuation calls = %#v", calls)
	}
	upstreamBody := string(calls[0].Body)
	if strings.Contains(upstreamBody, `"type":"compaction"`) || strings.Contains(upstreamBody, item.ID) || !strings.Contains(upstreamBody, "fixture compact summary") {
		t.Fatalf("V1 continuation did not normalize the compacted rollout window: %s", upstreamBody)
	}
}

func TestV2CompactSSE(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses", fixture(t, "v2-compact.json"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("V2 compact status = %d, body=%s\nCPA logs:\n%s", response.StatusCode, response.Body, h.logs.String())
	}
	if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("V2 compact content-type = %q, want text/event-stream", response.Header.Get("Content-Type"))
	}
	events := parseSSE(t, response.Body)
	if len(events) != 2 || eventType(events[0]) != "response.output_item.done" || eventType(events[1]) != "response.completed" {
		t.Fatalf("V2 compact events = %q", response.Body)
	}
	if !strings.HasPrefix(stringField(events[0], "item.id"), "cpa_compact_") || stringField(events[0], "item.encrypted_content") != "fixture compact summary" {
		t.Fatalf("V2 compaction item = %s", events[0])
	}
	if !strings.HasPrefix(stringField(events[1], "response.id"), "resp_cpa_compact_") {
		t.Fatalf("V2 completed event = %s", events[1])
	}
}

func TestCodexV1V2CompactedHistoryAndFollowUpParity(t *testing.T) {
	h := newHarness(t)

	v1 := h.post(t, "/v1/responses/compact", fixture(t, "v1-compact.json"))
	if v1.StatusCode != http.StatusOK {
		t.Fatalf("V1 compact status = %d, body=%s", v1.StatusCode, v1.Body)
	}
	var v1Body struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(v1.Body, &v1Body); err != nil {
		t.Fatalf("decode V1 compact body: %v", err)
	}

	v2 := h.post(t, "/v1/responses", fixture(t, "v2-compact.json"))
	if v2.StatusCode != http.StatusOK {
		t.Fatalf("V2 compact status = %d, body=%s", v2.StatusCode, v2.Body)
	}
	events := parseSSE(t, v2.Body)
	if len(events) != 2 {
		t.Fatalf("V2 compact events = %q", v2.Body)
	}
	var done struct {
		Item json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal([]byte(events[0]), &done); err != nil {
		t.Fatalf("decode V2 output item event: %v", err)
	}

	// Latest Codex installs V1's output directly. For V2 it retains the same
	// user history locally, then appends the single streamed compaction item.
	var v2Request struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(fixture(t, "v2-compact.json"), &v2Request); err != nil {
		t.Fatalf("decode V2 fixture: %v", err)
	}
	v2History := append([]json.RawMessage(nil), v2Request.Input[:len(v2Request.Input)-1]...)
	v2History = append(v2History, done.Item)
	if len(v1Body.Output) != len(v2History) {
		t.Fatalf("replacement history lengths differ: V1=%d V2=%d", len(v1Body.Output), len(v2History))
	}
	for i := range v1Body.Output {
		var left, right map[string]any
		if err := json.Unmarshal(v1Body.Output[i], &left); err != nil {
			t.Fatalf("decode V1 history item %d: %v", i, err)
		}
		if err := json.Unmarshal(v2History[i], &right); err != nil {
			t.Fatalf("decode V2 history item %d: %v", i, err)
		}
		delete(left, "id")
		delete(right, "id")
		leftJSON, _ := json.Marshal(left)
		rightJSON, _ := json.Marshal(right)
		if !bytes.Equal(leftJSON, rightJSON) {
			t.Fatalf("replacement history item %d differs after transport-only ID removal: V1=%s V2=%s", i, leftJSON, rightJSON)
		}
	}

	h.upstream.reset()
	postContinuation := func(history []json.RawMessage) []byte {
		body, err := json.Marshal(map[string]any{"model": bridgeModel, "input": history})
		if err != nil {
			t.Fatalf("encode continuation: %v", err)
		}
		response := h.post(t, "/v1/responses", body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("continuation status = %d, body=%s", response.StatusCode, response.Body)
		}
		calls := h.upstream.snapshot()
		return calls[len(calls)-1].Body
	}
	v1FollowUp := postContinuation(v1Body.Output)
	v2FollowUp := postContinuation(v2History)
	var v1Upstream, v2Upstream any
	if err := json.Unmarshal(v1FollowUp, &v1Upstream); err != nil {
		t.Fatalf("decode V1 follow-up upstream body: %v", err)
	}
	if err := json.Unmarshal(v2FollowUp, &v2Upstream); err != nil {
		t.Fatalf("decode V2 follow-up upstream body: %v", err)
	}
	v1JSON, _ := json.Marshal(v1Upstream)
	v2JSON, _ := json.Marshal(v2Upstream)
	if !bytes.Equal(v1JSON, v2JSON) {
		t.Fatalf("follow-up upstream bodies differ: V1=%s V2=%s", v1JSON, v2JSON)
	}
}

func TestCompactionReplayNormalization(t *testing.T) {
	h := newHarness(t)
	first := fixture(t, "replay.json")
	for i := 0; i < 2; i++ {
		h.upstream.reset()
		response := h.post(t, "/v1/responses", first)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("replay %d status = %d, body=%s\nCPA logs:\n%s", i, response.StatusCode, response.Body, h.logs.String())
		}
		calls := h.upstream.snapshot()
		if len(calls) != 1 {
			t.Fatalf("replay %d calls = %#v", i, calls)
		}
		body := string(calls[0].Body)
		if strings.Contains(body, "cpa_compact_fixture") || strings.Contains(body, `"type":"compaction"`) {
			t.Fatalf("replay %d leaked transient compaction state upstream: %s", i, body)
		}
		if !strings.Contains(body, "previous compacted summary") || !strings.Contains(body, `"role":"user"`) {
			t.Fatalf("replay %d did not normalize summary to user input: %s", i, body)
		}
	}
}

func TestStreamingCompactionReplayNormalization(t *testing.T) {
	h := newHarness(t)
	body := withStream(t, fixture(t, "replay.json"))
	response := h.post(t, "/v1/responses", body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("streaming replay status = %d, body=%s\nCPA logs:\n%s", response.StatusCode, response.Body, h.logs.String())
	}
	if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("streaming replay content-type = %q, want text/event-stream", response.Header.Get("Content-Type"))
	}
	if len(parseSSE(t, response.Body)) == 0 {
		t.Fatalf("streaming replay returned no SSE events: %q", response.Body)
	}
	calls := h.upstream.snapshot()
	if len(calls) != 1 || strings.Contains(string(calls[0].Body), "cpa_compact_fixture") || strings.Contains(string(calls[0].Body), `"type":"compaction"`) {
		t.Fatalf("streaming replay leaked transient compaction state upstream: %#v", calls)
	}
}

func TestWebSocketV2ReleaseGate(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(func() { t.Logf("CPA logs:\n%s", h.logs.String()) })
	wsURL := "ws" + strings.TrimPrefix(h.baseURL, "http") + "/v1/responses"
	header := http.Header{"Authorization": []string{"Bearer " + accessKey}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial CPA Responses websocket: %v\nCPA logs:\n%s", err, h.logs.String())
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, fixture(t, "ws-first.json")); err != nil {
		t.Fatalf("write first websocket turn: %v", err)
	}
	firstEvents := readWSEvents(t, conn)
	previousID := completedResponseID(firstEvents)
	if previousID == "" {
		t.Fatalf("first websocket turn has no completed response ID: %q", firstEvents)
	}
	second := strings.ReplaceAll(string(fixture(t, "ws-compact.json")), "${PREVIOUS_RESPONSE_ID}", previousID)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(second)); err != nil {
		t.Fatalf("write compaction websocket turn: %v", err)
	}
	events := readWSEvents(t, conn)
	if len(events) != 2 || eventType(events[0]) != "response.output_item.done" || eventType(events[1]) != "response.completed" {
		t.Fatalf("websocket release gate events = %q\nCPA logs:\n%s", events, h.logs.String())
	}
	if !strings.HasPrefix(stringField(events[0], "item.id"), "cpa_compact_") || stringField(events[0], "item.encrypted_content") != "fixture compact summary" {
		t.Fatalf("websocket compaction item = %s", events[0])
	}
	compactResponseID := completedResponseID(events)
	if compactResponseID == "" {
		t.Fatalf("websocket compaction response has no completed response ID: %q", events)
	}
	if err := conn.WriteMessage(websocket.TextMessage, websocketContinuation(t, compactResponseID)); err != nil {
		t.Fatalf("write websocket continuation after compaction: %v", err)
	}
	continuationEvents := readWSEvents(t, conn)
	if completedResponseID(continuationEvents) == "" {
		t.Fatalf("websocket continuation has no completed response: %q", continuationEvents)
	}
	calls := h.upstream.snapshot()
	if len(calls) != 3 || !strings.Contains(string(calls[1].Body), `"model":"summary-test"`) || strings.Contains(string(calls[1].Body), "compaction_trigger") {
		t.Fatalf("websocket compact request did not arrive normalized at summary model: %#v", calls)
	}
	// CPA builds the WebSocket continuation transcript after ModelRouter has
	// declined the ordinary turn. The bridge's request interceptor then sees the
	// merged compaction item and restores its plaintext as a user message before
	// the Responses-to-chat translator can discard that item type.
	if strings.Contains(string(calls[2].Body), `"type":"compaction"`) || !strings.Contains(string(calls[2].Body), "fixture compact summary") {
		t.Fatalf("websocket continuation did not normalize plaintext compaction state: %s", calls[2].Body)
	}
}

// --- Failure-path and edge-case integration tests ---

const (
	bridgeErrCode = "compact_bridge_failed"
	// stateErrCode is the stable, non-retryable code for compaction state that
	// can never be continued.
	stateErrCode = "invalid_compaction_state"
)

// TestScalarInputBridged verifies that a bridged model request with scalar
// (string) input does not 502. This is a regression test for the bug where the
// replay normalizer tried to decode scalar input as an array.
func TestScalarInputBridged(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses", fixture(t, "scalar-input.json"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("scalar input status = %d, body=%s\nCPA logs:\n%s", response.StatusCode, response.Body, h.logs.String())
	}
	calls := h.upstream.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 upstream call, got %d: %#v", len(calls), calls)
	}
}

// TestV1CompactUpstreamFailure verifies that V1 compact returns a stable 502
// with compact_bridge_failed when the summary model upstream fails.
func TestV1CompactUpstreamFailure(t *testing.T) {
	h := newHarness(t)
	h.upstream.setFailure()
	response := h.post(t, "/v1/responses/compact", fixture(t, "v1-compact.json"))
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("V1 compact upstream failure status = %d, want 502, body=%s", response.StatusCode, response.Body)
	}
	// CPA wraps plugin executor errors and may override the code, so assert on
	// the stable message rather than the code.
	if !strings.Contains(string(response.Body), "bridged compaction failed") {
		t.Fatalf("V1 compact failure body missing stable message: %s", response.Body)
	}
	// The summary call must have reached the upstream (and failed there).
	calls := h.upstream.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 upstream call (summary), got %d", len(calls))
	}
}

// TestV1CompactSummaryToolCallFails covers the CPA v7.2.125 translation path:
// tool_calls becomes a Responses function_call while status remains completed.
func TestV1CompactSummaryToolCallFails(t *testing.T) {
	h := newHarness(t)
	h.upstream.setToolCallSummary()
	response := h.post(t, "/v1/responses/compact", fixture(t, "v1-compact.json"))
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("V1 compact tool-call status = %d, want 502, body=%s", response.StatusCode, response.Body)
	}
	if !strings.Contains(string(response.Body), "summary upstream returned tool call") || strings.Contains(string(response.Body), "cpa_compact_") {
		t.Fatalf("V1 compact tool call was not rejected safely: %s", response.Body)
	}
}

// TestV2CompactUpstreamFailure verifies that V2 compact emits response.failed
// (and NOT response.completed) when the summary model upstream fails.
func TestV2CompactUpstreamFailure(t *testing.T) {
	h := newHarness(t)
	h.upstream.setFailure()
	response := h.post(t, "/v1/responses", fixture(t, "v2-compact.json"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("V2 compact failure HTTP status = %d, body=%s", response.StatusCode, response.Body)
	}
	if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("V2 compact failure content-type = %q", response.Header.Get("Content-Type"))
	}
	events := parseSSE(t, response.Body)
	hasFailed := false
	hasCompleted := false
	for _, event := range events {
		switch eventType(event) {
		case "response.failed":
			hasFailed = true
		case "response.completed":
			hasCompleted = true
		}
	}
	if !hasFailed {
		t.Fatalf("V2 compact upstream failure must emit response.failed: %s", response.Body)
	}
	if hasCompleted {
		t.Fatalf("V2 compact upstream failure must NOT emit response.completed: %s", response.Body)
	}
}

// TestNonBridgeModelDelegation verifies that a model not matching any bridge
// rule is delegated to CPA's built-in route without plugin interference.
func TestNonBridgeModelDelegation(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses", fixture(t, "native-normal.json"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("non-bridge model status = %d, body=%s\nCPA logs:\n%s", response.StatusCode, response.Body, h.logs.String())
	}
	if !strings.Contains(string(response.Body), "delegated ordinary response") {
		t.Fatalf("non-bridge model did not reach upstream: %s", response.Body)
	}
}

// TestOpaqueStatePassesThroughOnDeclaredNativeRoute verifies that opaque state is
// forwarded on a route an explicit `passthrough` rule declared native-compatible:
// no rewrite, no rejection, and the request still reaches the upstream.
func TestOpaqueStatePassesThroughOnDeclaredNativeRoute(t *testing.T) {
	h := newHarnessWithRules(t, declaredNativeRuleYAML())
	response := h.post(t, "/v1/responses", fixture(t, "native-compaction.json"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("declared native route status = %d, body=%s\nCPA logs:\n%s", response.StatusCode, response.Body, h.logs.String())
	}
	if calls := h.upstream.snapshot(); len(calls) != 1 {
		t.Fatalf("declared native route did not reach upstream once: %#v", calls)
	}
}

// TestOpaqueStateFailsClosedOnUnruledModel closes the no-match fail-open hole: an
// unruled model is only CPA's default route, so nothing declares it able to read
// opaque compaction state, and the request must never reach the upstream.
func TestOpaqueStateFailsClosedOnUnruledModel(t *testing.T) {
	for name, fixtureName := range map[string]string{
		"opaque state with reasoning": "unruled-opaque-with-reasoning.json",
		"opaque state alone":          "native-compaction.json",
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.upstream.reset()
			response := h.post(t, "/v1/responses", fixture(t, fixtureName))
			requireStateError(t, response, h.logs.String())
			if calls := h.upstream.snapshot(); len(calls) != 0 {
				t.Fatalf("opaque state on an unruled model reached upstream: %#v", calls)
			}
		})
	}
}

// TestReasoningOnlyHistoryIsUntouchedOnUnruledModel proves the state policy reads
// only the `compaction` item type: an opaque reasoning item on the same unruled
// route must not be mistaken for unreadable compaction state.
func TestReasoningOnlyHistoryIsUntouchedOnUnruledModel(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses", fixture(t, "unruled-reasoning-only.json"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reasoning-only history status = %d, body=%s\nCPA logs:\n%s", response.StatusCode, response.Body, h.logs.String())
	}
	if strings.Contains(string(response.Body), stateErrCode) {
		t.Fatalf("reasoning state was treated as compaction state: %s", response.Body)
	}
	if calls := h.upstream.snapshot(); len(calls) != 1 {
		t.Fatalf("reasoning-only history did not reach upstream once: %#v", calls)
	}
}

// TestBridgeStateNormalizesOnNativeTarget is the portability fix: plaintext
// state created by the bridge continues at summary level on a target that has
// no bridge rule at all, without pulling that target's own compact turns into
// the facade.
func TestBridgeStateNormalizesOnNativeTarget(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses", fixture(t, "passthrough-replay.json"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("native target with bridge state status = %d, body=%s\nCPA logs:\n%s", response.StatusCode, response.Body, h.logs.String())
	}
	calls := h.upstream.snapshot()
	if len(calls) != 1 {
		t.Fatalf("upstream calls = %#v, want exactly 1", calls)
	}
	body := string(calls[0].Body)
	if strings.Contains(body, "cpa_compact_fixture") || strings.Contains(body, `"type":"compaction"`) {
		t.Fatalf("bridge compaction state leaked to a native target: %s", body)
	}
	if !strings.Contains(body, "previous compacted summary") {
		t.Fatalf("bridge summary was not restored as ordinary input: %s", body)
	}
}

// TestNativeStateFailsClosedOnBridgeTarget verifies the other direction: state
// the facade cannot read is never forwarded to a bridged upstream.
func TestNativeStateFailsClosedOnBridgeTarget(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses", fixture(t, "unknown-compaction.json"))
	requireStateError(t, response, h.logs.String())
	if calls := h.upstream.snapshot(); len(calls) != 0 {
		t.Fatalf("unreadable compaction state must not reach upstream: %#v", calls)
	}
}

// TestMixedCompactionStateFailsClosedOnEveryTarget verifies that mixing bridge
// state with native state fails closed whatever the target rule says, because
// the retained history can no longer be interpreted reliably.
func TestMixedCompactionStateFailsClosedOnEveryTarget(t *testing.T) {
	for _, model := range []string{bridgeModel, nativeModel} {
		t.Run(model, func(t *testing.T) {
			h := newHarness(t)
			body := strings.ReplaceAll(string(fixture(t, "mixed-compaction.json")), `"model": "bridge-test"`, `"model": "`+model+`"`)
			response := h.post(t, "/v1/responses", []byte(body))
			requireStateError(t, response, h.logs.String())
			if calls := h.upstream.snapshot(); len(calls) != 0 {
				t.Fatalf("mixed compaction state reached upstream: %#v", calls)
			}
		})
	}
}

// TestCorruptBridgeStateFailsClosedOnEveryTarget covers missing, empty, and
// whitespace-only summary text on both target kinds.
func TestCorruptBridgeStateFailsClosedOnEveryTarget(t *testing.T) {
	variants := map[string]string{
		"missing": `{"type":"compaction","id":"cpa_compact_abc"}`,
		"empty":   `{"type":"compaction","id":"cpa_compact_abc","encrypted_content":""}`,
		"blank":   `{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"   "}`,
	}
	for _, model := range []string{bridgeModel, nativeModel} {
		for name, item := range variants {
			t.Run(model+"/"+name, func(t *testing.T) {
				h := newHarness(t)
				body := []byte(`{"model":"` + model + `","stream":false,"input":[` + item +
					`,{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
				response := h.post(t, "/v1/responses", body)
				requireStateError(t, response, h.logs.String())
			})
		}
	}
}

// TestStreamingV2StateErrorIsOutOfBand verifies a V2 trigger carrying unreadable
// state fails as an HTTP 400 error response instead of an in-band
// response.failed frame over an open 200 stream: an SSE failure frame is
// retryable for the Codex client, and this error can never resolve on retry.
func TestStreamingV2StateErrorIsOutOfBand(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses", fixture(t, "v2-trigger-native-state.json"))
	requireStateError(t, response, h.logs.String())
	if strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("state error must not open a compact stream: content-type=%q", response.Header.Get("Content-Type"))
	}
	if frames := parseSSE(t, response.Body); len(frames) != 0 {
		t.Fatalf("state error must not emit SSE frames: %q", frames)
	}
	if calls := h.upstream.snapshot(); len(calls) != 0 {
		t.Fatalf("state error must fail before the Summary Model call: %#v", calls)
	}
}

// TestV1CompactStateErrorIsNotRetryable applies the same rule to the standalone
// compact endpoint on a bridged target.
func TestV1CompactStateErrorIsNotRetryable(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses/compact", fixture(t, "unknown-compaction.json"))
	requireStateError(t, response, h.logs.String())
	if calls := h.upstream.snapshot(); len(calls) != 0 {
		t.Fatalf("V1 state error must fail before any summary call: %#v", calls)
	}
}

// TestBlankSummaryIsRuntimeCompactionFailure keeps the generation side on the
// retryable contract: an unusable summary never becomes a compaction item, and
// V1/V2 report the existing runtime failure.
func TestBlankSummaryIsRuntimeCompactionFailure(t *testing.T) {
	h := newHarness(t)
	h.upstream.setBlankSummary()

	v1 := h.post(t, "/v1/responses/compact", fixture(t, "v1-compact.json"))
	if v1.StatusCode != http.StatusBadGateway || !strings.Contains(string(v1.Body), "summary model produced no usable text") {
		t.Fatalf("V1 blank summary status = %d, body=%s", v1.StatusCode, v1.Body)
	}
	if strings.Contains(string(v1.Body), "cpa_compact_") {
		t.Fatalf("V1 stored a blank compaction item: %s", v1.Body)
	}

	h.upstream.reset()
	v2 := h.post(t, "/v1/responses", fixture(t, "v2-compact.json"))
	if v2.StatusCode != http.StatusOK {
		t.Fatalf("V2 blank summary status = %d, body=%s", v2.StatusCode, v2.Body)
	}
	events := parseSSE(t, v2.Body)
	failed, completed, item := false, false, false
	for _, event := range events {
		switch eventType(event) {
		case "response.failed":
			failed = true
			if stringField(event, "response.error.code") != bridgeErrCode {
				t.Fatalf("V2 blank summary error code = %q, want %q: %s", stringField(event, "response.error.code"), bridgeErrCode, event)
			}
		case "response.completed":
			completed = true
		case "response.output_item.done":
			item = true
		}
	}
	if !failed || completed || item {
		t.Fatalf("V2 blank summary must fail without an output item: %q", v2.Body)
	}
}

// requireStateError asserts the deterministic client-error contract the Codex
// client treats as terminal: HTTP 400 with the stable code.
func requireStateError(t *testing.T, response httpResult, logs string) {
	t.Helper()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("state error status = %d, want 400, body=%s\nCPA logs:\n%s", response.StatusCode, response.Body, logs)
	}
	if !strings.Contains(string(response.Body), stateErrCode) {
		t.Fatalf("state error body missing %q: %s", stateErrCode, response.Body)
	}
}

type httpResult struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func (h *harness) post(t *testing.T, path string, body []byte) httpResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.baseURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v\nCPA logs:\n%s", path, err, h.logs.String())
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST %s: %v", path, err)
	}
	return httpResult{StatusCode: resp.StatusCode, Header: resp.Header, Body: responseBody}
}

func bridgeRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve bridge root from test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func cpaSource(t *testing.T) string {
	t.Helper()
	path := os.Getenv("CPA_SOURCE_DIR")
	if path == "" {
		t.Skip("CPA integration skipped: set CPA_SOURCE_DIR to a CLIProxyAPI checkout")
	}
	info, err := os.Stat(filepath.Join(path, "cmd", "server"))
	if err != nil || !info.IsDir() {
		t.Skipf("CPA integration skipped: CPA_SOURCE_DIR=%q has no cmd/server: %v", path, err)
	}
	return path
}

func runGo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s in %s failed: %v\n%s", strings.Join(args, " "), dir, err, output)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve local port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitForCPA(t *testing.T, h *harness) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(h.baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("CPA did not become healthy\nCPA logs:\n%s", h.logs.String())
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join(bridgeRoot(t), "testdata", "integration", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func withStream(t *testing.T, body []byte) []byte {
	t.Helper()
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode fixture for stream: %v", err)
	}
	request["stream"] = true
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encode streaming fixture: %v", err)
	}
	return encoded
}

func websocketContinuation(t *testing.T, previousResponseID string) []byte {
	t.Helper()
	payload := map[string]any{
		"type":                 "response.create",
		"previous_response_id": previousResponseID,
		"input": []any{map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": "continue after compact"}},
		}},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode websocket continuation: %v", err)
	}
	return encoded
}

func sharedLibraryExtension() string {
	switch runtime.GOOS {
	case "darwin":
		return ".dylib"
	case "windows":
		return ".dll"
	default:
		return ".so"
	}
}

func parseSSE(t *testing.T, body []byte) []string {
	t.Helper()
	frames := strings.Split(strings.TrimSpace(string(body)), "\n\n")
	events := make([]string, 0, len(frames))
	for _, frame := range frames {
		for _, line := range strings.Split(frame, "\n") {
			if payload, ok := strings.CutPrefix(line, "data: "); ok {
				events = append(events, payload)
			}
		}
	}
	return events
}

func readWSEvents(t *testing.T, conn *websocket.Conn) []string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatalf("set websocket read deadline: %v", err)
	}
	var events []string
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket event: %v; prior events=%q", err, events)
		}
		events = append(events, string(raw))
		if eventType(string(raw)) == "response.completed" || eventType(string(raw)) == "response.failed" || eventType(string(raw)) == "error" {
			return events
		}
	}
}

func eventType(raw string) string { return stringField(raw, "type") }

func completedResponseID(events []string) string {
	for _, event := range events {
		if eventType(event) == "response.completed" {
			return stringField(event, "response.id")
		}
	}
	return ""
}

func stringField(raw, path string) string {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return ""
	}
	for _, part := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = object[part]
	}
	stringValue, _ := value.(string)
	return stringValue
}
