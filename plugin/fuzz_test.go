package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// configureFuzzBridge makes valid model-route and interceptor corpus entries
// exercise the bridge branch without involving a host callback.
func configureFuzzBridge(tb testing.TB) {
	tb.Helper()
	cfg, err := loadConfig([]byte("rules:\n  - match: bridge-*\n    action: bridge\n"))
	if err != nil {
		tb.Fatalf("load fuzz config: %v", err)
	}
	configHolder.store(cfg)
}

func compactFuzzBodies() [][]byte {
	return [][]byte{
		nil,
		[]byte(`{`),
		[]byte(`null`),
		[]byte(`[]`),
		[]byte(`{"model":"bridge-test"}`),
		[]byte(`{"model":"bridge-test","input":"scalar input"}`),
		[]byte(`{"model":"bridge-test","input":null}`),
		[]byte(`{"model":"bridge-test","input":{}}`),
		[]byte(`{"model":"bridge-test","input":[]}`),
		[]byte(`{"model":"bridge-test","input":[{"type":"message","role":"user","content":"hello"}]}`),
		[]byte(`{"model":"bridge-test","input":[{"type":"compaction","id":"cpa_compact_seed","encrypted_content":"summary"}]}`),
		[]byte(`{"model":"bridge-test","input":[{"type":"compaction","id":"opaque_seed","encrypted_content":"opaque"}]}`),
		[]byte(`{"model":"bridge-test","input":[{"type":"compaction_trigger"}]}`),
		[]byte(`{"model":"bridge-test","stream":true,"input":[{"type":"message","role":"user","content":"compact this"},{"type":"compaction_trigger"}]}`),
	}
}

func FuzzParseRequestInputItems(f *testing.F) {
	for _, body := range compactFuzzBodies() {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		items, ok := parseRequestInputItems(body)
		if !ok {
			return
		}
		for _, item := range items {
			if len(item.Raw) > 0 && !json.Valid(item.Raw) {
				t.Fatalf("accepted item is not valid JSON: %q", item.Raw)
			}
		}
	})
}

// FuzzNormalizeCompactionState checks three protocol invariants over arbitrary
// bodies: strictness is monotonic in the target (a state error on a more
// permissive route must also fail on every stricter route), the accepted
// rewrite never depends on the target, and normalization is one-way.
func FuzzNormalizeCompactionState(f *testing.F) {
	for _, body := range compactFuzzBodies() {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		var stateErr *pluginErr
		var rewritten []byte
		for _, target := range allCompactTargets {
			result, err := normalizeCompactionState(body, target)
			if current := asInvalidCompactionState(err); current != nil {
				if stateErr != nil && current.Code != stateErr.Code {
					t.Fatalf("%q: %s changed the state error code from %q to %q", body, target, stateErr.Code, current.Code)
				}
				stateErr = current
				continue
			}
			if stateErr != nil {
				t.Fatalf("%q: %s accepted a state that a more permissive target rejected: %v", body, target, err)
			}
			if !result.Changed {
				continue
			}
			if !json.Valid(result.Body) {
				t.Fatalf("%s rewrite returned invalid JSON: %q", target, result.Body)
			}
			// Normalization is one-way: no compaction item may survive it.
			items, ok := parseRequestInputItems(result.Body)
			if !ok || hasCompactionItems(items) {
				t.Fatalf("%s rewrite left compaction state in the request: %s", target, result.Body)
			}
			if rewritten != nil && string(rewritten) != string(result.Body) {
				t.Fatalf("%q: the accepted rewrite depends on the target: %s vs %s", body, rewritten, result.Body)
			}
			rewritten = result.Body
		}
	})
}
func FuzzHandleModelRoute(f *testing.F) {
	configureFuzzBridge(f)
	for _, body := range compactFuzzBodies() {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		// The ABI request itself is untrusted too.
		_, _ = handleModelRoute(body)

		raw, _ := json.Marshal(rpcModelRouteRequest{ModelRouteRequest: pluginapi.ModelRouteRequest{
			SourceFormat:   "openai-response",
			RequestedModel: "bridge-fuzz",
			Stream:         true,
			Body:           body,
		}})
		response, err := handleModelRoute(raw)
		if err != nil {
			return
		}
		assertEnvelopeJSON(t, response)
	})
}

func FuzzHandleRequestInterceptBefore(f *testing.F) {
	configureFuzzBridge(f)
	for _, body := range compactFuzzBodies() {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		// The ABI request itself is untrusted too.
		_, _ = handleRequestInterceptBefore(body)

		raw, _ := json.Marshal(rpcRequestInterceptRequest{RequestInterceptRequest: pluginapi.RequestInterceptRequest{
			SourceFormat:   "openai-response",
			RequestedModel: "bridge-fuzz",
			Body:           body,
		}})
		response, err := handleRequestInterceptBefore(raw)
		if err != nil {
			return
		}
		assertEnvelopeJSON(t, response)
	})
}

func FuzzV2SSEEvents(f *testing.F) {
	for _, seed := range [][2]string{
		{"cpa_compact_seed", "summary"},
		{"", ""},
		{"id with spaces", "quotes: \" and newline\n"},
		{"世界", "emoji 😀 and \x00"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, itemID, summary string) {
		events, err := v2SSEEvents(itemID, summary)
		if err != nil {
			return
		}
		assertSSEJSONFrames(t, events)
	})
}

func assertEnvelopeJSON(tb testing.TB, raw []byte) {
	tb.Helper()
	var decoded envelope
	if err := json.Unmarshal(raw, &decoded); err != nil {
		tb.Fatalf("successful handler response is not an envelope: %v; raw=%q", err, raw)
	}
}

func assertSSEJSONFrames(tb testing.TB, events []byte) {
	tb.Helper()
	frames := bytes.Split(events, []byte("\n\n"))
	validFrames := 0
	for _, frame := range frames {
		if len(frame) == 0 {
			continue
		}
		if !bytes.HasPrefix(frame, []byte("data: ")) {
			tb.Fatalf("SSE frame has no data prefix: %q", frame)
		}
		var payload json.RawMessage
		if err := json.Unmarshal(frame[len("data: "):], &payload); err != nil {
			tb.Fatalf("SSE frame payload is not JSON: %v; frame=%q", err, frame)
		}
		validFrames++
	}
	if validFrames == 0 {
		tb.Fatal("successful V2 SSE generation emitted no frames")
	}
}
