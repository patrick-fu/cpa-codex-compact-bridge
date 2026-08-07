package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func mustJSON(t *testing.T, v string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(v)) {
		t.Fatalf("invalid json: %s", v)
	}
	return json.RawMessage(v)
}

func TestDetectV2Trigger(t *testing.T) {
	items := []inputItem{
		{Type: "message", Raw: mustJSON(t, `{"type":"message","role":"user","content":"hi"}`)},
		{Type: "compaction_trigger", Raw: mustJSON(t, `{"type":"compaction_trigger"}`)},
	}
	if !detectV2Trigger(items) {
		t.Fatal("expected V2 trigger detected when last item is compaction_trigger")
	}
	// trigger not last
	items = []inputItem{
		{Type: "compaction_trigger", Raw: mustJSON(t, `{"type":"compaction_trigger"}`)},
		{Type: "message", Raw: mustJSON(t, `{"type":"message","role":"user","content":"hi"}`)},
	}
	if detectV2Trigger(items) {
		t.Fatal("trigger not last should not be detected")
	}
	// empty
	if detectV2Trigger(nil) {
		t.Fatal("empty items should not be detected")
	}
}

func TestDetectV2TriggerRequiresTrailingTypedItem(t *testing.T) {
	tests := []struct {
		name  string
		items []inputItem
		want  bool
	}{
		{
			name: "trailing trigger with metadata",
			items: []inputItem{
				{Type: "message", Raw: mustJSON(t, `{"type":"message","role":"user","content":"hi"}`)},
				{Type: compactionTriggerType, ID: "ignored", Raw: mustJSON(t, `{"type":"compaction_trigger","id":"ignored"}`)},
			},
			want: true,
		},
		{
			name: "trigger followed by compact state",
			items: []inputItem{
				{Type: compactionTriggerType, Raw: mustJSON(t, `{"type":"compaction_trigger"}`)},
				{Type: compactionType, ID: "cpa_compact_prior", Raw: mustJSON(t, `{"type":"compaction","id":"cpa_compact_prior","encrypted_content":"prior"}`)},
			},
			want: false,
		},
		{
			name: "untyped trailing raw item",
			items: []inputItem{
				{Type: "message", Raw: mustJSON(t, `{"type":"message","role":"user","content":"hi"}`)},
				{Raw: mustJSON(t, `"compaction_trigger"`)},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectV2Trigger(tt.items); got != tt.want {
				t.Fatalf("detectV2Trigger() = %v, want %v for %+v", got, tt.want, tt.items)
			}
		})
	}
}

func TestDetectV1Compact(t *testing.T) {
	if !detectV1Compact("responses/compact", false) {
		t.Fatal("responses/compact non-stream should be V1")
	}
	if detectV1Compact("responses/compact", true) {
		t.Fatal("responses/compact with stream=true should not be V1")
	}
	if detectV1Compact("", false) {
		t.Fatal("empty alt should not be V1")
	}
}

func TestShouldBridgeRoute(t *testing.T) {
	normalStream := pluginapi.ModelRouteRequest{
		Stream: true,
		Body:   []byte(`{"model":"bridge-test","input":[{"type":"message","role":"user","content":"hi"}]}`),
	}
	if shouldBridgeRoute(normalStream) {
		t.Fatal("ordinary streaming turn must remain on CPA's built-in route")
	}
	v2 := normalStream
	v2.Body = []byte(`{"model":"bridge-test","input":[{"type":"compaction_trigger"}]}`)
	if !shouldBridgeRoute(v2) {
		t.Fatal("V2 compaction trigger must be bridged")
	}
	replay := normalStream
	replay.Body = []byte(`{"model":"bridge-test","input":[{"type":"compaction","id":"cpa_compact_x","encrypted_content":"summary"}]}`)
	if shouldBridgeRoute(replay) {
		t.Fatal("streaming replay state must be normalized by the request interceptor, not executor routing")
	}
	if !shouldBridgeRoute(pluginapi.ModelRouteRequest{Stream: false, Body: normalStream.Body}) {
		t.Fatal("non-streaming request must be routed so V1 compact can be recognized by Alt")
	}
}

func TestIsCPACompaction(t *testing.T) {
	if !isCPACompaction(inputItem{Type: "compaction", ID: "cpa_compact_abc123"}) {
		t.Fatal("cpa_compact_abc123 should be recognized")
	}
	if isCPACompaction(inputItem{Type: "compaction", ID: "other_compact_abc"}) {
		t.Fatal("other prefix should not be recognized")
	}
	if isCPACompaction(inputItem{Type: "message", ID: "cpa_compact_abc"}) {
		t.Fatal("non-compaction type should not be recognized")
	}
}

func TestIsCPACompactionMarkerBoundaries(t *testing.T) {
	tests := []struct {
		name string
		item inputItem
		want bool
	}{
		{"minimum suffix", inputItem{Type: compactionType, ID: compactionIDPrefix + "x"}, true},
		{"prefix only", inputItem{Type: compactionType, ID: compactionIDPrefix}, false},
		{"empty id", inputItem{Type: compactionType}, false},
		{"prefix appears later", inputItem{Type: compactionType, ID: "native_" + compactionIDPrefix + "x"}, false},
		{"same id but wrong type", inputItem{Type: "message", ID: compactionIDPrefix + "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCPACompaction(tt.item); got != tt.want {
				t.Fatalf("isCPACompaction(%+v) = %v, want %v", tt.item, got, tt.want)
			}
		})
	}
}

func TestRemoveLastCompactionTrigger(t *testing.T) {
	items := []inputItem{
		{Type: "message", Raw: mustJSON(t, `{"type":"message"}`)},
		{Type: "compaction_trigger", Raw: mustJSON(t, `{"type":"compaction_trigger"}`)},
	}
	out := removeLastCompactionTrigger(items)
	if len(out) != 1 || out[0].Type != "message" {
		t.Fatalf("expected 1 message item, got %+v", out)
	}
	// no trailing trigger: unchanged
	items = []inputItem{{Type: "message", Raw: mustJSON(t, `{"type":"message"}`)}}
	out = removeLastCompactionTrigger(items)
	if len(out) != 1 {
		t.Fatalf("expected unchanged, got len=%d", len(out))
	}
}

func TestBuildSummaryRequestInput(t *testing.T) {
	items := []inputItem{
		{Type: "message", Raw: mustJSON(t, `{"type":"message","role":"user","content":"hi"}`)},
		{Type: "compaction", ID: "cpa_compact_x", EncryptedContent: "summary", Raw: mustJSON(t, `{"type":"compaction","id":"cpa_compact_x","encrypted_content":"summary"}`)},
		{Type: "compaction_trigger", Raw: mustJSON(t, `{"type":"compaction_trigger"}`)},
	}
	out, err := buildSummaryRequestInput(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected message plus restored summary, got %d", len(out))
	}
	var item map[string]any
	_ = json.Unmarshal(out[0], &item)
	if item["type"] != "message" {
		t.Fatalf("expected message, got %v", item["type"])
	}
	var summary map[string]any
	_ = json.Unmarshal(out[1], &summary)
	if summary["role"] != "user" || summary["content"] != "summary" {
		t.Fatalf("expected restored user summary, got %+v", summary)
	}
}

func TestBuildSummaryRequestInputFailsForUnknownCompaction(t *testing.T) {
	items := []inputItem{{
		Type:             "compaction",
		ID:               "native_compaction",
		EncryptedContent: "opaque",
		Raw:              mustJSON(t, `{"type":"compaction","id":"native_compaction","encrypted_content":"opaque"}`),
	}}
	if _, err := buildSummaryRequestInput(items); err == nil {
		t.Fatal("expected fail-closed error for unknown compaction")
	}
}

func TestBuildSummaryRequestInputRestoresMarkedStateAndDropsTriggers(t *testing.T) {
	items := []inputItem{
		{Type: compactionTriggerType, Raw: mustJSON(t, `{"type":"compaction_trigger"}`)},
		{Type: "message", Raw: mustJSON(t, `{"type":"message","role":"user","content":"before"}`)},
		{Type: compactionType, ID: "cpa_compact_abc", EncryptedContent: "summary with \"quotes\"\nnext", Raw: mustJSON(t, `{"type":"compaction","id":"cpa_compact_abc","encrypted_content":"summary with \"quotes\"\\nnext"}`)},
		{Type: compactionTriggerType, Raw: mustJSON(t, `{"type":"compaction_trigger"}`)},
	}
	out, err := buildSummaryRequestInput(items)
	if err != nil {
		t.Fatalf("build summary input: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("all trigger items must be removed, got %d items: %s", len(out), out)
	}
	var first, restored map[string]any
	if err := json.Unmarshal(out[0], &first); err != nil {
		t.Fatalf("decode preserved input: %v", err)
	}
	if err := json.Unmarshal(out[1], &restored); err != nil {
		t.Fatalf("decode restored input: %v", err)
	}
	if first["content"] != "before" || restored["type"] != "message" || restored["role"] != "user" || restored["content"] != "summary with \"quotes\"\nnext" {
		t.Fatalf("unexpected restored summary input: first=%+v restored=%+v", first, restored)
	}
}

func TestBuildSummaryRequestInputFailsClosedForInvalidCompactState(t *testing.T) {
	tests := []struct {
		name string
		item inputItem
	}{
		{"unknown compact id", inputItem{Type: compactionType, ID: "native_compact_abc", EncryptedContent: "opaque", Raw: mustJSON(t, `{"type":"compaction","id":"native_compact_abc","encrypted_content":"opaque"}`)}},
		{"prefix only id", inputItem{Type: compactionType, ID: compactionIDPrefix, EncryptedContent: "opaque", Raw: mustJSON(t, `{"type":"compaction","id":"cpa_compact_","encrypted_content":"opaque"}`)}},
		{"missing content", inputItem{Type: compactionType, ID: "cpa_compact_abc", Raw: mustJSON(t, `{"type":"compaction","id":"cpa_compact_abc"}`)}},
		{"whitespace content", inputItem{Type: compactionType, ID: "cpa_compact_abc", EncryptedContent: " \t\n ", Raw: mustJSON(t, `{"type":"compaction","id":"cpa_compact_abc","encrypted_content":" \\t\\n "}`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := buildSummaryRequestInput([]inputItem{tt.item})
			if err == nil || out != nil || !strings.Contains(err.Error(), "refusing to summarize opaque compact state") {
				t.Fatalf("expected fail-closed error, got out=%s err=%v", out, err)
			}
		})
	}
}

func TestRewriteInputItemsFailsClosedForInvalidCompactState(t *testing.T) {
	tests := []struct {
		name string
		item inputItem
	}{
		{"unknown compact id", inputItem{Type: compactionType, ID: "native_compact_abc", EncryptedContent: "opaque", Raw: mustJSON(t, `{"type":"compaction","id":"native_compact_abc","encrypted_content":"opaque"}`)}},
		{"marked but whitespace summary", inputItem{Type: compactionType, ID: "cpa_compact_abc", EncryptedContent: " \n ", Raw: mustJSON(t, `{"type":"compaction","id":"cpa_compact_abc","encrypted_content":" \\n "}`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := rewriteInputItems([]inputItem{tt.item})
			if err == nil || out != nil {
				t.Fatalf("expected fail-closed rewrite, got out=%s err=%v", out, err)
			}
		})
	}
}

func TestParseRequestInputItems(t *testing.T) {
	body := []byte(`{"model":"glm-4.7","input":[{"type":"message","role":"user","content":"hi"},{"type":"compaction_trigger"}]}`)
	items, ok := parseRequestInputItems(body)
	if !ok {
		t.Fatal("expected ok")
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if !detectV2Trigger(items) {
		t.Fatal("expected V2 trigger as last item")
	}
}

func TestParseRequestInputItemsEmptyBody(t *testing.T) {
	items, ok := parseRequestInputItems(nil)
	if ok || items != nil {
		t.Fatal("empty body should return not-ok nil")
	}
}

func TestParseRequestInputItemsBoundaryInputs(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		wantOK   bool
		wantLen  int
		lastType string
	}{
		{"empty object", []byte(`{}`), true, 0, ""},
		{"empty input array", []byte(`{"input":[]}`), true, 0, ""},
		{"null input", []byte(`{"input":null}`), true, 0, ""},
		{"scalar input", []byte(`{"input":"plain text"}`), false, 0, ""},
		{"object input", []byte(`{"input":{"type":"message"}}`), false, 0, ""},
		{"malformed body", []byte(`{"input":`), false, 0, ""},
		{"non-object entries remain typed empty", []byte(`{"input":["plain text",null,{}]}`), true, 3, ""},
		{"trailing trigger", []byte(`{"input":[{"type":"message"},{"type":"compaction_trigger"}]}`), true, 2, compactionTriggerType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, ok := parseRequestInputItems(tt.body)
			if ok != tt.wantOK || len(items) != tt.wantLen {
				t.Fatalf("parseRequestInputItems(%s) = ok=%v len=%d, want ok=%v len=%d", tt.body, ok, len(items), tt.wantOK, tt.wantLen)
			}
			if tt.lastType != "" && items[len(items)-1].Type != tt.lastType {
				t.Fatalf("last item type = %q, want %q", items[len(items)-1].Type, tt.lastType)
			}
			if !tt.wantOK && items != nil {
				t.Fatalf("failed parse must not expose partial items: %+v", items)
			}
		})
	}
}

func TestParseInputItemsPreservesRawJSONAndCompactionFields(t *testing.T) {
	raw := []json.RawMessage{
		mustJSON(t, `{"type":"compaction","id":"cpa_compact_abc","role":"user","encrypted_content":"summary"}`),
		mustJSON(t, `{"type":"message","role":"user","content":"continue","extra":{"nested":true}}`),
	}
	items := parseInputItems(raw)
	if len(items) != len(raw) {
		t.Fatalf("expected %d items, got %d", len(raw), len(items))
	}
	if items[0].Type != compactionType || items[0].ID != "cpa_compact_abc" || items[0].Role != "user" || items[0].EncryptedContent != "summary" || string(items[0].Raw) != string(raw[0]) {
		t.Fatalf("compaction fields/raw were not retained: %+v", items[0])
	}
	if items[1].Type != "message" || items[1].Role != "user" || string(items[1].Raw) != string(raw[1]) {
		t.Fatalf("message fields/raw were not retained: %+v", items[1])
	}
}
