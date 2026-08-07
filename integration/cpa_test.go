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
	server       *httptest.Server
	mu           sync.Mutex
	calls        []upstreamRequest
	failRequests bool
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
		if strings.Contains(string(body), "Summarize the conversation and current task") {
			text = "fixture compact summary"
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

type harness struct {
	baseURL  string
	upstream *fakeUpstream
	command  *exec.Cmd
	logs     bytes.Buffer
}

func newHarness(t *testing.T) *harness {
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
      rules:
        - match: %q
          action: bridge
          summary_model: %q
openai-compatibility:
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
`, port, filepath.Join(temp, "auth"), accessKey, filepath.Join(temp, "plugins"), bridgeModel, summaryModel, upstream.server.URL, bridgeModel, bridgeModel, summaryModel, summaryModel, nativeModel, nativeModel)
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
	if !strings.Contains(string(response.Body), "fixture compact summary") || !strings.Contains(string(response.Body), `"output"`) {
		t.Fatalf("V1 compact response = %s", response.Body)
	}
	calls := h.upstream.snapshot()
	if len(calls) != 1 || !strings.Contains(string(calls[0].Body), `"model":"summary-test"`) {
		t.Fatalf("V1 compact must call only summary model: %#v", calls)
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

const bridgeErrCode = "compact_bridge_failed"

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

// TestNonBridgeModelCompactionNotIntercepted verifies that the plugin's
// request interceptor does not fail-closed on compaction items when the model
// does not match any bridge rule.
func TestNonBridgeModelCompactionNotIntercepted(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses", fixture(t, "native-compaction.json"))
	if response.StatusCode == http.StatusBadGateway && strings.Contains(string(response.Body), bridgeErrCode) {
		t.Fatalf("non-bridge model with compaction was fail-closed by plugin (must not intercept): %s", response.Body)
	}
	calls := h.upstream.snapshot()
	if len(calls) < 1 {
		t.Fatalf("non-bridge model with compaction did not reach upstream (plugin may have intercepted): %d calls", len(calls))
	}
}

// TestUnknownCompactionItemFailClosed verifies that an unknown (non-cpa_compact_)
// compaction item on a bridged model triggers fail-closed 502 and is never
// forwarded to the upstream.
func TestUnknownCompactionItemFailClosed(t *testing.T) {
	h := newHarness(t)
	response := h.post(t, "/v1/responses", fixture(t, "unknown-compaction.json"))
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("unknown compaction item status = %d, want 502, body=%s", response.StatusCode, response.Body)
	}
	if !strings.Contains(string(response.Body), bridgeErrCode) {
		t.Fatalf("unknown compaction item body missing %q: %s", bridgeErrCode, response.Body)
	}
	calls := h.upstream.snapshot()
	if len(calls) != 0 {
		t.Fatalf("unknown compaction item must not reach upstream: %#v", calls)
	}
}

// TestEmptyEncryptedContentFailClosed verifies that a cpa_compact_ item with
// empty encrypted_content on a bridged model triggers fail-closed 502.
func TestEmptyEncryptedContentFailClosed(t *testing.T) {
	h := newHarness(t)
	body := []byte(`{"model":"bridge-test","stream":false,"input":[` +
		`{"type":"compaction","id":"cpa_compact_abc","encrypted_content":""},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}` +
		`]}`)
	response := h.post(t, "/v1/responses", body)
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("empty encrypted_content status = %d, want 502, body=%s", response.StatusCode, response.Body)
	}
	if !strings.Contains(string(response.Body), bridgeErrCode) {
		t.Fatalf("empty encrypted_content body missing %q: %s", bridgeErrCode, response.Body)
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
