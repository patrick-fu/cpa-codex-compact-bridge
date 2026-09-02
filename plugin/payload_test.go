package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// decodeSSEDataFrames checks the wire-level framing before decoding each JSON
// payload. The bridge contract is one complete JSON object per data frame.
func decodeSSEDataFrames(t *testing.T, raw []byte) []json.RawMessage {
	t.Helper()
	if !bytes.HasSuffix(raw, []byte("\n\n")) {
		t.Fatalf("SSE response must end in a blank line: %q", raw)
	}
	frames := bytes.Split(bytes.TrimSuffix(raw, []byte("\n\n")), []byte("\n\n"))
	decoded := make([]json.RawMessage, 0, len(frames))
	for i, frame := range frames {
		if !bytes.HasPrefix(frame, []byte("data: ")) {
			t.Fatalf("frame %d is not a data frame: %q", i, frame)
		}
		payload := bytes.TrimPrefix(frame, []byte("data: "))
		if !json.Valid(payload) {
			t.Fatalf("frame %d contains invalid JSON: %q", i, payload)
		}
		decoded = append(decoded, payload)
	}
	return decoded
}

func TestV1CompactResponseBodyUsesCanonicalCompactionItem(t *testing.T) {
	items := parseInputItems([]json.RawMessage{
		json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"keep me"}]}`),
	})
	body, err := v1CompactResponseBody(items, "cpa_compact_test-uuid", "the summary text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed struct {
		Output []struct {
			Type             string `json:"type"`
			Role             string `json:"role"`
			ID               string `json:"id"`
			EncryptedContent string `json:"encrypted_content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Output) != 2 {
		t.Fatalf("expected retained history plus compaction, got %d items", len(parsed.Output))
	}
	if parsed.Output[0].Type != "message" || parsed.Output[0].Role != "user" {
		t.Fatalf("output[0] = %+v", parsed.Output[0])
	}
	compact := parsed.Output[1]
	if compact.Type != compactionType || compact.Role != "" || compact.ID != "cpa_compact_test-uuid" || compact.EncryptedContent != "the summary text" {
		t.Fatalf("output[1] = %+v", compact)
	}
}

func TestV1CompactResponseBodyRoundTripsSpecialCharacters(t *testing.T) {
	summary := "quote: \\\"; newline:\n; unicode: 雪; backslash: \\\\; tag: <summary>"
	body, err := v1CompactResponseBody(nil, "cpa_compact_special", summary)
	if err != nil {
		t.Fatalf("build V1 response: %v", err)
	}
	if !json.Valid(body) {
		t.Fatalf("V1 response is not valid JSON: %s", body)
	}
	var decoded struct {
		Output []struct {
			Type             string `json:"type"`
			ID               string `json:"id"`
			EncryptedContent string `json:"encrypted_content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode V1 response: %v", err)
	}
	if len(decoded.Output) != 1 {
		t.Fatalf("unexpected V1 output shape: %+v", decoded.Output)
	}
	item := decoded.Output[0]
	if item.Type != compactionType || item.ID != "cpa_compact_special" || item.EncryptedContent != summary {
		t.Fatalf("V1 response changed protocol fields or summary: %+v", item)
	}
}

func TestV1CompactResponseBodyMatchesRemoteRetentionContract(t *testing.T) {
	raw := []json.RawMessage{
		json.RawMessage(`{"type":"message","role":"system","content":"drop system"}`),
		json.RawMessage(`{"type":"message","role":"developer","content":"keep developer","custom":"preserved"}`),
		json.RawMessage(`{"type":"message","role":"user","content":"keep user"}`),
		json.RawMessage(`{"type":"message","role":"assistant","content":"drop assistant"}`),
		json.RawMessage(`{"type":"reasoning","encrypted_content":"drop reasoning"}`),
		json.RawMessage(`{"type":"function_call","call_id":"drop tool"}`),
		json.RawMessage(`{"type":"compaction","id":"cpa_compact_old","encrypted_content":"drop old state"}`),
		json.RawMessage(`{"type":"compaction_trigger"}`),
	}
	body, err := v1CompactResponseBody(parseInputItems(raw), "cpa_compact_new", "new summary")
	if err != nil {
		t.Fatalf("build V1 response: %v", err)
	}
	var decoded struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode V1 response: %v", err)
	}
	if len(decoded.Output) != 3 {
		t.Fatalf("output has %d items, want developer, user, compaction: %s", len(decoded.Output), body)
	}
	if !bytes.Equal(decoded.Output[0], raw[1]) || !bytes.Equal(decoded.Output[1], raw[2]) {
		t.Fatalf("retained messages were not preserved verbatim: %s", body)
	}
	if strings.Contains(string(body), "drop ") || !strings.Contains(string(body), `"id":"cpa_compact_new"`) {
		t.Fatalf("V1 retention leaked discarded state or lost new compaction: %s", body)
	}
}

func TestV1AndV2UseIdenticalCompactionItem(t *testing.T) {
	const (
		itemID  = "cpa_compact_parity"
		summary = "shared compact summary"
	)
	v1Body, err := v1CompactResponseBody(nil, itemID, summary)
	if err != nil {
		t.Fatalf("build V1 response: %v", err)
	}
	var v1 struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(v1Body, &v1); err != nil {
		t.Fatalf("decode V1 response: %v", err)
	}
	if len(v1.Output) != 1 {
		t.Fatalf("V1 output has %d items, want only the compaction item", len(v1.Output))
	}

	v2Events, err := v2SSEEvents(itemID, summary)
	if err != nil {
		t.Fatalf("build V2 events: %v", err)
	}
	frames := decodeSSEDataFrames(t, v2Events)
	var v2 struct {
		Item json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(frames[0], &v2); err != nil {
		t.Fatalf("decode V2 output-item frame: %v", err)
	}

	var v1Item, v2Item map[string]any
	if err := json.Unmarshal(v1.Output[0], &v1Item); err != nil {
		t.Fatalf("decode V1 compaction item: %v", err)
	}
	if err := json.Unmarshal(v2.Item, &v2Item); err != nil {
		t.Fatalf("decode V2 compaction item: %v", err)
	}
	v1Canonical, _ := json.Marshal(v1Item)
	v2Canonical, _ := json.Marshal(v2Item)
	if !bytes.Equal(v1Canonical, v2Canonical) {
		t.Fatalf("V1 and V2 compaction items differ: V1=%s V2=%s", v1Canonical, v2Canonical)
	}
}

func TestV1RecompactionConsumesOldStateAndEmitsOnlyNewestCompaction(t *testing.T) {
	raw := []json.RawMessage{
		json.RawMessage(`{"type":"compaction","id":"cpa_compact_old","encrypted_content":"old summary"}`),
		json.RawMessage(`{"type":"message","role":"assistant","content":"old assistant artifact"}`),
		json.RawMessage(`{"type":"message","role":"user","content":"new request"}`),
	}
	items := parseInputItems(raw)
	summaryInput, err := buildSummaryRequestInput(items)
	if err != nil {
		t.Fatalf("build summary input: %v", err)
	}
	if len(summaryInput) != 3 || !strings.Contains(string(summaryInput[0]), "old summary") || strings.Contains(string(summaryInput[0]), compactionIDPrefix) {
		t.Fatalf("old compact state was not restored for summarization: %s", summaryInput)
	}

	body, err := v1CompactResponseBody(items, "cpa_compact_new", "new summary")
	if err != nil {
		t.Fatalf("build V1 recompaction response: %v", err)
	}
	var decoded struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode V1 recompaction response: %v", err)
	}
	if len(decoded.Output) != 2 {
		t.Fatalf("output has %d items, want retained user message plus newest compaction: %s", len(decoded.Output), body)
	}
	if !bytes.Equal(decoded.Output[0], raw[2]) {
		t.Fatalf("new user message was not retained verbatim: %s", decoded.Output[0])
	}
	window := string(body)
	if strings.Contains(window, "cpa_compact_old") || strings.Contains(window, "old summary") || strings.Contains(window, "old assistant artifact") {
		t.Fatalf("stale compact state leaked into the replacement window: %s", body)
	}
	if !strings.Contains(window, `"id":"cpa_compact_new"`) || !strings.Contains(window, `"encrypted_content":"new summary"`) {
		t.Fatalf("new compaction state is missing: %s", body)
	}
}

func TestV2CompactionItemJSON(t *testing.T) {
	raw, err := v2CompactionItemJSON("cpa_compact_test-uuid", "plaintext summary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if item["type"] != "compaction" {
		t.Fatalf("type = %v", item["type"])
	}
	if item["id"] != "cpa_compact_test-uuid" {
		t.Fatalf("id = %v", item["id"])
	}
	if item["encrypted_content"] != "plaintext summary" {
		t.Fatalf("encrypted_content = %v", item["encrypted_content"])
	}
}

func TestV2CompactionItemJSONRoundTripsSpecialCharacters(t *testing.T) {
	id := "cpa_compact_雪-\\\"-id"
	summary := "line one\nline two \\\\ \"quoted\" <xml/>"
	raw, err := v2CompactionItemJSON(id, summary)
	if err != nil {
		t.Fatalf("build V2 item: %v", err)
	}
	var item struct {
		Type             string `json:"type"`
		ID               string `json:"id"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("decode V2 item: %v", err)
	}
	if item.Type != compactionType || item.ID != id || item.EncryptedContent != summary {
		t.Fatalf("V2 item changed protocol fields or summary: %+v", item)
	}
}

func TestV2SSEEvents(t *testing.T) {
	events, err := v2SSEEvents("cpa_compact_test-uuid", "the summary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(events)
	// Must contain exactly two data: frames
	frameCount := strings.Count(s, "data: ")
	if frameCount != 2 {
		t.Fatalf("expected 2 SSE frames, got %d", frameCount)
	}
	// First frame: response.output_item.done with compaction item
	if !strings.Contains(s, `"type":"response.output_item.done"`) {
		t.Fatal("missing response.output_item.done")
	}
	if !strings.Contains(s, `"cpa_compact_test-uuid"`) {
		t.Fatal("missing item id")
	}
	// Second frame: response.completed with resp_ id
	if !strings.Contains(s, `"type":"response.completed"`) {
		t.Fatal("missing response.completed")
	}
	if !strings.Contains(s, `"resp_cpa_compact_test-uuid"`) {
		t.Fatal("missing resp_ id")
	}
	// Must end with double newline (SSE frame terminator)
	if !strings.HasSuffix(s, "\n\n") {
		t.Fatal("SSE events must end with \\n\\n")
	}
}

func TestV2SSEEventsDecodeEachFrameAndPreserveContract(t *testing.T) {
	itemID := "cpa_compact_雪-123"
	summary := "one\ntwo with \\\\ and \"quotes\""
	events, err := v2SSEEvents(itemID, summary)
	if err != nil {
		t.Fatalf("build V2 events: %v", err)
	}
	frames := decodeSSEDataFrames(t, events)
	if len(frames) != 2 {
		t.Fatalf("expected exactly two frames, got %d", len(frames))
	}
	var done struct {
		Type string `json:"type"`
		Item struct {
			Type             string `json:"type"`
			ID               string `json:"id"`
			EncryptedContent string `json:"encrypted_content"`
		} `json:"item"`
	}
	if err := json.Unmarshal(frames[0], &done); err != nil {
		t.Fatalf("decode output-item frame: %v", err)
	}
	if done.Type != "response.output_item.done" || done.Item.Type != compactionType || done.Item.ID != itemID || done.Item.EncryptedContent != summary {
		t.Fatalf("unexpected V2 item frame: %+v", done)
	}
	var completed struct {
		Type     string `json:"type"`
		Response struct {
			ID string `json:"id"`
		} `json:"response"`
	}
	if err := json.Unmarshal(frames[1], &completed); err != nil {
		t.Fatalf("decode completed frame: %v", err)
	}
	if completed.Type != "response.completed" || completed.Response.ID != "resp_"+itemID {
		t.Fatalf("unexpected V2 completed frame: %+v", completed)
	}
}

func TestV2ResponseFailedSSE(t *testing.T) {
	raw := v2ResponseFailedSSE("something went wrong")
	s := string(raw)
	if !strings.HasPrefix(s, "data: ") {
		t.Fatal("must start with data: ")
	}
	if !strings.Contains(s, `"type":"response.failed"`) {
		t.Fatal("missing response.failed")
	}
	if !strings.Contains(s, `"compact_bridge_failed"`) {
		t.Fatal("missing error code")
	}
	if !strings.Contains(s, `"response":{"error"`) && !strings.Contains(s, `"response":{"id"`) {
		t.Fatal("response.failed must carry its error under response")
	}
	if !strings.Contains(s, "something went wrong") {
		t.Fatal("missing message")
	}
	// Must NOT contain response.completed
	if strings.Contains(s, "response.completed") {
		t.Fatal("failure frame must not contain response.completed")
	}
}

func TestV2ResponseFailedSSEIsOneStructuredFrame(t *testing.T) {
	message := "summary failed: \"quota\"\nplease retry"
	frames := decodeSSEDataFrames(t, v2ResponseFailedSSE(message))
	if len(frames) != 1 {
		t.Fatalf("failure must contain one frame, got %d", len(frames))
	}
	var failed struct {
		Type     string `json:"type"`
		Response struct {
			ID    string `json:"id"`
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal(frames[0], &failed); err != nil {
		t.Fatalf("decode failure frame: %v", err)
	}
	if failed.Type != "response.failed" || failed.Response.ID != "resp_cpa_compact_failed" || failed.Response.Error.Code != errCodeCompactBridgeFailed || failed.Response.Error.Message != message {
		t.Fatalf("unexpected failure frame: %+v", failed)
	}
}

func TestV2SSEEventsNoPartial(t *testing.T) {
	events, _ := v2SSEEvents("cpa_compact_x", "summary")
	s := string(events)
	// Must not contain response.created or partial events
	if strings.Contains(s, "response.created") {
		t.Fatal("must not emit response.created")
	}
	if strings.Contains(s, "response.output_text") {
		t.Fatal("must not emit partial output_text events")
	}
}

// TestExtractSummaryTextRejectsBlankSummary is the generation half of the
// corrupt-state rule: a blank summary must never become a compaction item, and
// it stays a retryable runtime failure instead of a client error.
func TestExtractSummaryTextRejectsBlankSummary(t *testing.T) {
	for name, body := range map[string][]byte{
		"Responses whitespace output": []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"  \n "}]}]}`),
		"Chat whitespace content":     []byte(`{"choices":[{"message":{"content":" \t "},"finish_reason":"stop"}]}`),
	} {
		t.Run(name, func(t *testing.T) {
			text, err := extractSummaryText(body)
			pluginErrValue := mustPluginErr(t, err, errCodeCompactBridgeFailed)
			if text != "" || asInvalidCompactionState(pluginErrValue) != nil {
				t.Fatalf("blank summary = %q err = %+v", text, pluginErrValue)
			}
		})
	}
	usable := []struct {
		name string
		body []byte
		want string
	}{
		{"Responses", []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"  kept summary  "}]}]}`), "kept summary"},
		{"Chat", []byte(`{"choices":[{"message":{"content":"chat summary"},"finish_reason":"stop"}]}`), "chat summary"},
	}
	for _, tt := range usable {
		t.Run(tt.name, func(t *testing.T) {
			text, err := extractSummaryText(tt.body)
			if err != nil || text != tt.want {
				t.Fatalf("usable summary rejected or altered: text = %q err = %v", text, err)
			}
		})
	}
}

// TestV2CompactionItemJSONRejectsBlankSummary keeps the item builder itself
// unable to persist unreadable state.
func TestV2CompactionItemJSONRejectsBlankSummary(t *testing.T) {
	for _, summary := range []string{"", "   ", "\n\t "} {
		if item, err := v2CompactionItemJSON("cpa_compact_x", summary); err == nil || item != nil {
			t.Fatalf("blank summary %q produced an item: %s %v", summary, item, err)
		}
	}
	item, err := v2CompactionItemJSON("cpa_compact_x", "real summary")
	if err != nil || !strings.Contains(string(item), "real summary") {
		t.Fatalf("usable summary rejected: %s %v", item, err)
	}
}

func TestExtractAssistantTextResponses(t *testing.T) {
	body := []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}]}]}`)
	text, err := extractAssistantText(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q", text)
	}
}

func TestExtractAssistantTextResponsesConcatenatesOutputTextParts(t *testing.T) {
	body := []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first "},{"type":"refusal","text":"ignored"},{"type":"output_text","text":"second"}]}]}`)
	text, err := extractAssistantText(body)
	if err != nil {
		t.Fatalf("extract Responses text: %v", err)
	}
	if text != "first second" {
		t.Fatalf("text = %q", text)
	}
}

func TestExtractAssistantTextChat(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"chat reply"},"finish_reason":"stop"}]}`)
	text, err := extractAssistantText(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "chat reply" {
		t.Fatalf("text = %q", text)
	}
}

func TestExtractAssistantTextEmpty(t *testing.T) {
	body := []byte(`{"status":"completed","output":[]}`)
	_, err := extractAssistantText(body)
	if err == nil {
		t.Fatal("expected error for empty output")
	}
}

func TestExtractAssistantTextRejectsMalformedAndEmptyVariants(t *testing.T) {
	for name, body := range map[string][]byte{
		"malformed JSON":           []byte(`{"output":`),
		"no output text":           []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","text":"no"}]}]}`),
		"empty Responses text":     []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}]}`),
		"empty Chat choices":       []byte(`{"choices":[]}`),
		"empty Chat message":       []byte(`{"choices":[{"message":{"content":""}}]}`),
		"unrelated valid response": []byte(`{"status":"ok"}`),
	} {
		t.Run(name, func(t *testing.T) {
			text, err := extractAssistantText(body)
			if err == nil || text != "" {
				t.Fatalf("expected no assistant text, got text=%q err=%v", text, err)
			}
		})
	}
}

func TestExtractSummaryTextCompletionStates(t *testing.T) {
	// This fixture preserves the observed qwen HTTP 200 Chat payload. CPA
	// translates it to a Responses function_call while retaining status="completed".
	qwenToolCall, err := os.ReadFile("../testdata/qwen-summary-tool-call.json")
	if err != nil {
		t.Fatalf("read qwen tool-call fixture: %v", err)
	}
	cases := []struct {
		name    string
		body    []byte
		want    string
		wantErr string
	}{
		{"responses completed", []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]}]}`), "summary", ""},
		{"responses incomplete", []byte(`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]}]}`), "", "summary upstream incomplete (reason=max_output_tokens)"},
		{"responses failed", []byte(`{"status":"failed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]}]}`), "", "summary upstream failed (status=failed)"},
		{"responses unknown status is sanitized", []byte(`{"status":"<arbitrary>","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]}]}`), "", "summary upstream invalid terminal status"},
		{"responses missing status with output text", []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]}]}`), "", "summary upstream invalid terminal status"},
		{"responses null status with output text", []byte(`{"status":null,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]}]}`), "", "summary upstream invalid terminal status"},
		{"responses missing status with only tool call", []byte(`{"output":[{"type":"function_call","name":"search","arguments":"{}"}]}`), "", "summary upstream invalid terminal status"},
		{"responses completed with translated tool call", []byte(`{"status":"completed","output":[{"type":"function_call","name":"search","arguments":"{}"}]}`), "", "summary upstream returned tool call"},
		{"responses completed with computer call", []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]},{"type":"computer_call","id":"call_1"}]}`), "", "summary upstream returned tool call"},
		{"responses completed with mcp call", []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]},{"type":"mcp_call","id":"call_1"}]}`), "", "summary upstream returned tool call"},
		{"responses completed with unknown item", []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]},{"type":"future_call"}]}`), "", "summary upstream returned unsupported output item"},
		{"qwen tool-call fixture", qwenToolCall, "", "summary upstream returned tool call"},
		{"responses reasoning has no text fallback", []byte(`{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"hidden"}]}]}`), "", "summary model produced no usable text"},
		{"responses incomplete details", []byte(`{"status":"completed","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]}]}`), "", "summary upstream incomplete (reason=max_output_tokens)"},
		{"ambiguous response shapes", []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]}],"choices":[{"message":{"content":"summary"},"finish_reason":"length"}]}`), "", "summary response has multiple terminal shapes"},
		{"completed response with no output", []byte(`{"status":"completed"}`), "", "summary model produced no usable text"},
		{"chat stop", []byte(`{"choices":[{"message":{"content":"summary"},"finish_reason":"stop"}]}`), "summary", ""},
		{"chat length", []byte(`{"choices":[{"message":{"content":"summary"},"finish_reason":"length"}]}`), "", "summary upstream truncated (finish_reason=length)"},
		{"chat max tokens", []byte(`{"choices":[{"message":{"content":"summary"},"finish_reason":"max_tokens"}]}`), "", "summary upstream truncated (finish_reason=max_tokens)"},
		{"chat tool calls", []byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1"}]},"finish_reason":"tool_calls"}]}`), "", "summary upstream returned tool call"},
		{"chat content filter", []byte(`{"choices":[{"message":{"content":"summary"},"finish_reason":"content_filter"}]}`), "", "summary upstream incomplete (finish_reason=content_filter)"},
		{"chat missing finish reason", []byte(`{"choices":[{"message":{"content":"summary"}}]}`), "", "summary text missing terminal status"},
		{"unknown incomplete reason is sanitized", []byte(`{"status":"incomplete","incomplete_details":{"reason":"<arbitrary>"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]}]}`), "", "summary upstream incomplete"},
		{"unknown chat finish reason is sanitized", []byte(`{"choices":[{"message":{"content":"summary"},"finish_reason":"<arbitrary>"}]}`), "", "summary upstream invalid terminal status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, err := extractSummaryText(tc.body)
			if tc.wantErr == "" {
				if err != nil || text != tc.want {
					t.Fatalf("text=%q err=%v, want %q", text, err, tc.want)
				}
				return
			}
			pluginErrValue := mustPluginErr(t, err, errCodeCompactBridgeFailed)
			if text != "" || pluginErrValue.Message != tc.wantErr {
				t.Fatalf("text=%q error=%+v, want message %q", text, pluginErrValue, tc.wantErr)
			}
		})
	}
}

func TestFinalSummaryIsTrimmedAndBounded(t *testing.T) {
	text, err := extractSummaryText([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"  1234  "}]}]}`))
	if err != nil || text != "1234" {
		t.Fatalf("final summary = %q, %v", text, err)
	}
	cfg := defaultConfig()
	cfg.MaxSummaryBytes = 4
	if err := validateSummarySize(text, cfg.MaxSummaryBytes); err != nil {
		t.Fatalf("summary at configured byte limit rejected: %v", err)
	}
	err = validateSummarySize("12345", cfg.MaxSummaryBytes)
	pluginErrValue := mustPluginErr(t, err, errCodeCompactBridgeFailed)
	if pluginErrValue.Message != "summary exceeds 4 bytes" {
		t.Fatalf("summary size error = %+v", pluginErrValue)
	}
	if err := validateSummarySize("雪", len([]byte("雪"))); err != nil {
		t.Fatalf("exact byte limit rejected: %v", err)
	}
}

func TestErrorEnvelopeFromPreservesBridgeContract(t *testing.T) {
	raw := errorEnvelopeFrom(&pluginErr{
		Code:       errCodeCompactBridgeFailed,
		Message:    "summary request failed",
		HTTPStatus: 502,
	})
	var got envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal error envelope: %v", err)
	}
	if got.OK || got.Error == nil {
		t.Fatalf("expected failed envelope, got %+v", got)
	}
	if got.Error.Code != errCodeCompactBridgeFailed || got.Error.HTTPStatus != 502 {
		t.Fatalf("error = %+v", got.Error)
	}
}

func TestCompactFailureMessageIsSanitized(t *testing.T) {
	frame := string(v2ResponseFailedSSE("bridged compaction failed"))
	if strings.Contains(frame, "api-key") || strings.Contains(frame, "upstream body") {
		t.Fatalf("failure frame leaks detail: %s", frame)
	}
	if !strings.Contains(frame, "bridged compaction failed") {
		t.Fatalf("failure frame lost stable message: %s", frame)
	}
}

func TestBuildSummaryRequestBodyUsesCodexLocalDefaultPrompt(t *testing.T) {
	req := rpcExecutorRequest{}
	req.OriginalRequest = []byte(`{"model":"bridge-test","stream":true,"input":[{"type":"message","role":"user","content":"old"}]}`)
	body, err := buildSummaryRequestBody(req, "summary-test", []json.RawMessage{json.RawMessage(`{"type":"message","role":"user","content":"old"}`)}, defaultConfig())
	if err != nil {
		t.Fatalf("build summary request: %v", err)
	}
	var parsed struct {
		Model  string            `json:"model"`
		Stream bool              `json:"stream"`
		Input  []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode summary request: %v", err)
	}
	if parsed.Model != "summary-test" || parsed.Stream {
		t.Fatalf("summary request model/stream = %q/%v", parsed.Model, parsed.Stream)
	}
	var instruction struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if len(parsed.Input) != 2 {
		t.Fatalf("summary request input = %s", parsed.Input)
	}
	if err := json.Unmarshal(parsed.Input[1], &instruction); err != nil {
		t.Fatalf("decode compact instruction: %v", err)
	}
	want := "You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM that will resume the task.\n\n" +
		"Include:\n" +
		"- Current progress and key decisions made\n" +
		"- Important context, constraints, or user preferences\n" +
		"- What remains to be done (clear next steps)\n" +
		"- Any critical data, examples, or references needed to continue\n\n" +
		"Be concise, structured, and focused on helping the next LLM seamlessly continue the work.\n" + compactGuard
	if instruction.Type != "message" || instruction.Role != "user" || instruction.Content != want {
		t.Fatalf("compact instruction = %+v, want exact Codex local prompt %q", instruction, want)
	}
}

func TestBuildSummaryRequestBodyUsesConfiguredCompactPrompt(t *testing.T) {
	req := rpcExecutorRequest{}
	req.OriginalRequest = []byte(`{"model":"bridge-test","input":[]}`)
	cfg := defaultConfig()
	cfg.CompactPrompt = "Preserve ticket CPA-42 and the exact next command."
	body, err := buildSummaryRequestBody(req, "summary-test", nil, cfg)
	if err != nil {
		t.Fatalf("build summary request: %v", err)
	}
	var parsed struct {
		Input []struct {
			Content string `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode summary request: %v", err)
	}
	if len(parsed.Input) != 1 || parsed.Input[0].Content != "Preserve ticket CPA-42 and the exact next command.\n"+compactGuard {
		t.Fatalf("summary input = %+v", parsed.Input)
	}
}

func TestBuildSummaryRequestBodyOmitsInstructionForExplicitBlankPrompt(t *testing.T) {
	cfg, err := loadConfig([]byte("compact_prompt: ''\nappend_tool_guard: true\n"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	req := rpcExecutorRequest{}
	req.OriginalRequest = []byte(`{"input":[]}`)
	cleanInput := []json.RawMessage{mustJSON(t, `{"type":"message","role":"user","content":"history"}`)}
	body, err := buildSummaryRequestBody(req, "summary-model", cleanInput, cfg)
	if err != nil {
		t.Fatalf("build summary request: %v", err)
	}
	var parsed struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode summary request: %v", err)
	}
	if len(parsed.Input) != 1 || strings.Contains(string(body), compactGuard) || strings.Contains(string(body), "CONTEXT CHECKPOINT COMPACTION") {
		t.Fatalf("blank compact prompt added an instruction: %s", body)
	}
}

func TestBuildSummaryRequestBodyUsesWhitelistAndPreservesAllowedFields(t *testing.T) {
	req := rpcExecutorRequest{}
	req.OriginalRequest = []byte(`{
		"model":"original-model",
		"stream":true,
		"instructions":"keep this",
		"reasoning":{"effort":"high"},
		"service_tier":"priority",
		"previous_response_id":"resp_old",
		"conversation":"conv_old",
		"prompt_cache_key":"cache_old",
		"store":true,
		"include":["reasoning.encrypted_content"],
		"metadata":{"attempt":2},
		"text":{"format":{"type":"text"}},
		"tool_choice":"required",
		"truncation":"auto",
		"temperature":0.4,
		"top_p":0.9,
		"top_k":20,
		"frequency_penalty":0.2,
		"presence_penalty":0.1,
		"user":"user-1",
		"safety_identifier":"safe-1",
		"prompt":{"id":"pmpt_1"},
		"tools":[{"type":"function","name":"must_not_forward"}],
		"parallel_tool_calls":true,
		"input":[{"type":"message","role":"user","content":"old"}]
	}`)
	cleanInput := []json.RawMessage{mustJSON(t, `{"type":"message","role":"user","content":"keep \"this\""}`)}
	cfg := defaultConfig()
	cfg.MaxSummaryTokens = 1234
	cfg.ForwardServiceTier = true
	body, err := buildSummaryRequestBody(req, "summary-model", cleanInput, cfg)
	if err != nil {
		t.Fatalf("build summary request: %v", err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode summary request: %v", err)
	}
	if string(parsed["model"]) != `"summary-model"` || string(parsed["stream"]) != "false" || string(parsed["tools"]) != "[]" || string(parsed["parallel_tool_calls"]) != "false" || string(parsed["max_output_tokens"]) != "1234" {
		t.Fatalf("summary protocol fields = %s", body)
	}
	if string(parsed["instructions"]) != `"keep this"` || string(parsed["reasoning"]) != `{"effort":"high"}` || string(parsed["service_tier"]) != `"priority"` {
		t.Fatalf("allowed fields were not preserved: %s", body)
	}
	for _, field := range []string{"previous_response_id", "conversation", "prompt_cache_key", "store", "include", "metadata", "text", "tool_choice", "truncation", "temperature", "top_p", "top_k", "frequency_penalty", "presence_penalty", "user", "safety_identifier", "prompt"} {
		if _, ok := parsed[field]; ok {
			t.Fatalf("whitelist leaked %q in %s", field, body)
		}
	}
	if strings.Contains(string(body), `"tool_choice"`) {
		t.Fatalf("summary request wire body leaked tool_choice: %s", body)
	}
	var input []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(parsed["input"], &input); err != nil {
		t.Fatalf("decode summary input: %v", err)
	}
	if len(input) != 2 || input[0].Content != `keep "this"` || input[1].Type != "message" || input[1].Role != "user" || input[1].Content != codexLocalCompactPrompt+compactGuard {
		t.Fatalf("unexpected summary input: %+v", input)
	}
}

func TestBuildSummaryRequestBodyDoesNotForwardServiceTierByDefault(t *testing.T) {
	req := rpcExecutorRequest{}
	req.OriginalRequest = []byte(`{"input":[],"service_tier":"priority"}`)
	body, err := buildSummaryRequestBody(req, "summary-model", nil, defaultConfig())
	if err != nil {
		t.Fatalf("build summary request: %v", err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode summary request: %v", err)
	}
	if _, ok := parsed["service_tier"]; ok {
		t.Fatalf("service_tier leaked into summary request: %s", body)
	}
}

func TestBuildSummaryRequestBodyImageFiltering(t *testing.T) {
	cleanInput := []json.RawMessage{mustJSON(t, `{"type":"message","role":"user","summary":"s","content":[{"type":"input_text","text":"before"},{"type":"input_image","image_url":{"url":"data:image/png;base64,AA==","detail":"low"}}]}`)}
	req := rpcExecutorRequest{}
	req.OriginalRequest = []byte(`{"input":[]}`)
	for _, tc := range []struct {
		name        string
		imageModels []string
		wantImage   bool
	}{
		{name: "default strips images"},
		{name: "nonmatching allowlist strips images", imageModels: []string{"other-vision-*"}},
		{name: "allowlisted summary model retains images", imageModels: []string{"vision-*"}, wantImage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.SummaryImageModels = tc.imageModels
			body, err := buildSummaryRequestBody(req, "vision-summary", cleanInput, cfg)
			if err != nil {
				t.Fatalf("build summary request: %v", err)
			}
			if got := strings.Contains(string(body), "input_image"); got != tc.wantImage {
				t.Fatalf("image retained=%v, body=%s", got, body)
			}
			if !tc.wantImage && !strings.Contains(string(body), "[image removed]") {
				t.Fatalf("image was not replaced: %s", body)
			}
			if !tc.wantImage {
				var parsed struct {
					Input []json.RawMessage `json:"input"`
				}
				if err := json.Unmarshal(body, &parsed); err != nil {
					t.Fatalf("decode image-filtered request: %v", err)
				}
				var message struct {
					Summary json.RawMessage              `json:"summary"`
					Content []map[string]json.RawMessage `json:"content"`
				}
				if err := json.Unmarshal(parsed.Input[0], &message); err != nil {
					t.Fatalf("decode image message: %v", err)
				}
				image := message.Content[1]
				if string(image["type"]) != `"input_text"` || string(message.Summary) != `"s"` || string(image["detail"]) != `"low"` || image["image_url"] != nil || string(message.Content[0]["text"]) != `"before"` {
					t.Fatalf("image filtering lost sibling fields: %s", body)
				}
			}
		})
	}
}

func TestBuildSummaryRequestBodyCanDisableToolGuard(t *testing.T) {
	cfg := defaultConfig()
	cfg.AppendToolGuard = false
	req := rpcExecutorRequest{}
	req.OriginalRequest = []byte(`{"input":[]}`)
	body, err := buildSummaryRequestBody(req, "summary-model", nil, cfg)
	if err != nil {
		t.Fatalf("build summary request: %v", err)
	}
	if strings.Contains(string(body), compactGuard) {
		t.Fatalf("tool guard was not disabled: %s", body)
	}
}

func TestBuildSummaryRequestBodyRejectsMalformedOriginalRequest(t *testing.T) {
	req := rpcExecutorRequest{}
	req.OriginalRequest = []byte(`{"input":`)
	_, err := buildSummaryRequestBody(req, "summary-model", nil, defaultConfig())
	if err == nil || !strings.Contains(err.Error(), "summary_encode_failed") {
		t.Fatalf("expected wrapped malformed-request error, got %v", err)
	}
}

func Example_v2SSEEvents() {
	events, err := v2SSEEvents("cpa_compact_example", "summary")
	if err != nil {
		return
	}
	frames := bytes.Split(bytes.TrimSpace(events), []byte("\n\n"))
	fmt.Println(len(frames))
	// Output: 2
}
