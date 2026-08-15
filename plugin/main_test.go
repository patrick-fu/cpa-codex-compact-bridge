package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type testEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *envelopeError  `json:"error"`
}

func decodeTestEnvelope(t *testing.T, raw []byte) testEnvelope {
	t.Helper()
	var env testEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v; raw=%s", err, raw)
	}
	return env
}

func mustPluginErr(t *testing.T, err error, wantCode string) *pluginErr {
	t.Helper()
	if err == nil {
		t.Fatalf("expected pluginErr %q, got nil", wantCode)
	}
	var pluginErrValue *pluginErr
	if !errors.As(err, &pluginErrValue) {
		t.Fatalf("expected *pluginErr, got %T (%v)", err, err)
	}
	if pluginErrValue.Code != wantCode {
		t.Fatalf("pluginErr.Code = %q, want %q", pluginErrValue.Code, wantCode)
	}
	return pluginErrValue
}

func lifecycleRequestJSON(t *testing.T, yaml string) []byte {
	t.Helper()
	raw, err := json.Marshal(lifecycleRequest{ConfigYAML: []byte(yaml), SchemaVersion: pluginabi.SchemaVersion})
	if err != nil {
		t.Fatalf("encode lifecycle request: %v", err)
	}
	return raw
}

func installTestConfig(t *testing.T, yaml string) {
	t.Helper()
	_, err := handleMethod(pluginabi.MethodPluginRegister, lifecycleRequestJSON(t, yaml))
	if err != nil {
		t.Fatalf("register config: %v", err)
	}
}

func routeRequestJSON(t *testing.T, model string, stream bool, body string) []byte {
	t.Helper()
	raw, err := json.Marshal(rpcModelRouteRequest{ModelRouteRequest: pluginapi.ModelRouteRequest{
		RequestedModel: model,
		Stream:         stream,
		SourceFormat:   "openai-response",
		Body:           []byte(body),
	}})
	if err != nil {
		t.Fatalf("encode route request: %v", err)
	}
	return raw
}

func routeResult(t *testing.T, raw []byte) pluginapi.ModelRouteResponse {
	t.Helper()
	env := decodeTestEnvelope(t, raw)
	if !env.OK {
		t.Fatalf("route returned error: %+v", env.Error)
	}
	var result pluginapi.ModelRouteResponse
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode route result: %v", err)
	}
	return result
}

func registeredPluginVersion(t *testing.T) string {
	t.Helper()
	raw, err := handleMethod(pluginabi.MethodPluginRegister, lifecycleRequestJSON(t, "rules:\n  - match: '*'\n    action: bridge\n"))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	env := decodeTestEnvelope(t, raw)
	if !env.OK {
		t.Fatalf("register envelope: %+v", env.Error)
	}
	var response struct {
		Metadata struct {
			Version string `json:"Version"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(env.Result, &response); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}
	return response.Metadata.Version
}

func TestPluginRegisterResponseUsesConfiguredVersion(t *testing.T) {
	want := os.Getenv("CPA_COMPACT_EXPECTED_PLUGIN_VERSION")
	if want == "" {
		want = "0.1.2"
	}
	if got := registeredPluginVersion(t); got != want {
		t.Fatalf("plugin.register metadata.Version = %q, want %q", got, want)
	}
}

func TestHandleLifecycleRegisterAndReconfigure(t *testing.T) {
	register := lifecycleRequestJSON(t, "rules:\n  - match: 'deepseek-*'\n    action: bridge\non_no_match: passthrough\n")
	raw, err := handleMethod(pluginabi.MethodPluginRegister, register)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	env := decodeTestEnvelope(t, raw)
	if !env.OK {
		t.Fatalf("register envelope: %+v", env.Error)
	}
	var registration map[string]any
	if err := json.Unmarshal(env.Result, &registration); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if registration["schema_version"] != float64(pluginabi.SchemaVersion) {
		t.Fatalf("schema_version = %v", registration["schema_version"])
	}
	metadata, ok := registration["metadata"].(map[string]any)
	if !ok || metadata["Name"] != pluginName || metadata["Version"] != pluginVersion {
		t.Fatalf("metadata = %v", registration["metadata"])
	}
	capabilities, ok := registration["capabilities"].(map[string]any)
	if !ok || capabilities["model_router"] != true || capabilities["executor"] != true || capabilities["request_interceptor"] != true {
		t.Fatalf("capabilities = %v", registration["capabilities"])
	}

	// Reconfigure must replace the active rules, rather than merge with them.
	reconfigure := lifecycleRequestJSON(t, "rules:\n  - match: 'glm-*'\n    action: passthrough\non_no_match: passthrough\n")
	if _, err := handleMethod(pluginabi.MethodPluginReconfigure, reconfigure); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}
	if got := routeResult(t, mustRoute(t, "deepseek-v4", false, `{"model":"deepseek-v4","input":"hello"}`)); got.Handled {
		t.Fatalf("old rule remained active: %+v", got)
	}
	if got := routeResult(t, mustRoute(t, "glm-5.2", false, `{"model":"glm-5.2","input":"hello"}`)); got.Handled {
		t.Fatalf("passthrough rule should not be handled: %+v", got)
	}
}

func TestHandleLifecycleRejectsMalformedAndInvalidConfig(t *testing.T) {
	for name, request := range map[string][]byte{
		"malformed json":      []byte("{"),
		"invalid yaml config": lifecycleRequestJSON(t, "rules:\n  - match: ''\n    action: bridge\n"),
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := handleMethod(pluginabi.MethodPluginRegister, request)
			want := "config_decode_failed"
			if name == "invalid yaml config" {
				want = "config_invalid"
			}
			if err == nil {
				t.Fatalf("expected %s error, got response %s", want, raw)
			}
			var pluginErrValue *pluginErr
			if !errors.As(err, &pluginErrValue) || pluginErrValue.Code != want {
				t.Fatalf("error = %v, want pluginErr code %q", err, want)
			}
		})
	}
}

func TestHandleMethodUnknownAndIdentifiers(t *testing.T) {
	raw, err := handleMethod("method.does.not.exist", nil)
	if err != nil {
		t.Fatalf("unknown method returned Go error: %v", err)
	}
	env := decodeTestEnvelope(t, raw)
	if env.OK || env.Error == nil || env.Error.Code != "unknown_method" {
		t.Fatalf("unknown method envelope = %+v", env)
	}

	raw, err = handleMethod(pluginabi.MethodExecutorIdentifier, nil)
	if err != nil {
		t.Fatalf("identifier: %v", err)
	}
	env = decodeTestEnvelope(t, raw)
	if !env.OK || !strings.Contains(string(env.Result), pluginName) {
		t.Fatalf("identifier result = %s", env.Result)
	}
}

func TestHandleModelRouteBridgeAndPassthroughDecisions(t *testing.T) {
	installTestConfig(t, "rules:\n  - match: 'deepseek-*'\n    action: bridge\n  - match: 'gpt-*'\n    action: passthrough\non_no_match: passthrough\n")
	ordinary := `{"model":"deepseek-v4","input":"hello"}`
	trigger := `{"model":"deepseek-v4","stream":true,"input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]}`

	// Non-streaming bridged requests are routed to the executor so V1 compact
	// and ordinary replay requests can be distinguished there.
	got := routeResult(t, mustRoute(t, "deepseek-v4", false, ordinary))
	if !got.Handled || got.TargetKind != pluginapi.ModelRouteTargetSelf || got.Reason == "" {
		t.Fatalf("ordinary bridged route = %+v", got)
	}
	// A V2 trigger is the only streaming request that needs the executor.
	got = routeResult(t, mustRoute(t, "deepseek-v4", true, trigger))
	if !got.Handled || got.TargetKind != pluginapi.ModelRouteTargetSelf {
		t.Fatalf("V2 trigger route = %+v", got)
	}
	// Ordinary streams remain on CPA's built-in path.
	got = routeResult(t, mustRoute(t, "deepseek-v4", true, ordinary))
	if got.Handled {
		t.Fatalf("ordinary stream should pass through: %+v", got)
	}
	for _, model := range []string{"gpt-5.4", "claude-opus", "unknown-model"} {
		got = routeResult(t, mustRoute(t, model, false, ordinary))
		if got.Handled {
			t.Fatalf("%s should pass through: %+v", model, got)
		}
	}
}

func TestHandleModelRouteDecodeErrors(t *testing.T) {
	installTestConfig(t, "rules:\n  - match: '*'\n    action: bridge\n")
	_, err := handleMethod(pluginabi.MethodModelRoute, []byte("{"))
	var pluginErrValue *pluginErr
	if !errors.As(err, &pluginErrValue) || pluginErrValue.Code != "route_decode_failed" {
		t.Fatalf("route decode error = %v", err)
	}

	_, err = handleMethod(pluginabi.MethodExecutorExecute, []byte("{"))
	if !errors.As(err, &pluginErrValue) || pluginErrValue.Code != "execute_decode_failed" {
		t.Fatalf("execute decode error = %v", err)
	}
}

func mustRoute(t *testing.T, model string, stream bool, body string) []byte {
	t.Helper()
	raw, err := handleMethod(pluginabi.MethodModelRoute, routeRequestJSON(t, model, stream, body))
	if err != nil {
		t.Fatalf("handle route: %v", err)
	}
	return raw
}

func executorRequestJSON(t *testing.T, req rpcExecutorRequest) []byte {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("encode executor request: %v", err)
	}
	return raw
}

func decodeExecuteSuccess(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	env := decodeTestEnvelope(t, raw)
	if !env.OK {
		t.Fatalf("execute returned error envelope: %+v", env.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode execute result: %v", err)
	}
	return result
}

func TestHandleRequestInterceptBeforeDecodeError(t *testing.T) {
	_, err := handleMethod(pluginabi.MethodRequestInterceptBefore, []byte("{"))
	mustPluginErr(t, err, "intercept_decode_failed")
}

func TestHandleRequestInterceptBeforeFailClosedBody(t *testing.T) {
	installTestConfig(t, "rules:\n  - match: 'bridge-*'\n    action: bridge\n")
	request, err := json.Marshal(rpcRequestInterceptRequest{
		RequestInterceptRequest: pluginapi.RequestInterceptRequest{
			SourceFormat:   "openai-response",
			RequestedModel: "bridge-test",
			Body:           []byte(`{"input":[{"type":"compaction","id":"native_compact","encrypted_content":"opaque"}]}`),
		},
	})
	if err != nil {
		t.Fatalf("encode request intercept request: %v", err)
	}
	raw, err := handleMethod(pluginabi.MethodRequestInterceptBefore, request)
	if err != nil {
		t.Fatalf("handle request intercept: %v", err)
	}
	env := decodeTestEnvelope(t, raw)
	if !env.OK {
		t.Fatalf("expected successful RPC envelope, got %+v", env.Error)
	}
	var result pluginapi.RequestInterceptResponse
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode request intercept result: %v", err)
	}
	if !result.Terminate || result.StatusCode != http.StatusBadGateway {
		t.Fatalf("unexpected request intercept response: %+v", result)
	}
	if result.ResponseHeaders.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %v", result.ResponseHeaders)
	}
	if !strings.Contains(string(result.ResponseBody), errCodeCompactBridgeFailed) {
		t.Fatalf("response body = %s", result.ResponseBody)
	}
}

func TestHandleExecuteUnexpectedOrdinaryStreamFailsClosed(t *testing.T) {
	installTestConfig(t, "rules:\n  - match: 'bridge-*'\n    action: bridge\n")
	_, err := handleMethod(pluginabi.MethodExecutorExecuteStream, executorRequestJSON(t, rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "bridge-test",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"bridge-test","stream":true,"input":[{"type":"message","role":"user","content":"continue"}]}`),
		},
	}))
	pluginErrValue := mustPluginErr(t, err, errCodeCompactBridgeFailed)
	if pluginErrValue.HTTPStatus != http.StatusBadGateway || !strings.Contains(pluginErrValue.Message, "unexpected ordinary streaming bridge request") {
		t.Fatalf("unexpected error payload: %+v", pluginErrValue)
	}
}

func TestHandleExecuteOrdinaryReplayUnknownCompactionFailsClosed(t *testing.T) {
	installTestConfig(t, "rules:\n  - match: 'bridge-*'\n    action: bridge\n")
	_, err := handleMethod(pluginabi.MethodExecutorExecute, executorRequestJSON(t, rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "bridge-test",
			Stream:          false,
			OriginalRequest: []byte(`{"model":"bridge-test","input":[{"type":"compaction","id":"opaque_compact","encrypted_content":"opaque"}]}`),
		},
	}))
	pluginErrValue := mustPluginErr(t, err, errCodeCompactBridgeFailed)
	if pluginErrValue.HTTPStatus != http.StatusBadGateway || !strings.Contains(pluginErrValue.Message, "unknown compaction item") {
		t.Fatalf("unexpected error payload: %+v", pluginErrValue)
	}
}

func TestHandleExecuteV1HostFailureReturnsStablePluginError(t *testing.T) {
	installTestConfig(t, "rules:\n  - match: 'bridge-*'\n    action: bridge\n")
	_, err := handleMethod(pluginabi.MethodExecutorExecute, executorRequestJSON(t, rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "bridge-test",
			Alt:             altResponsesCompact,
			Stream:          false,
			OriginalRequest: []byte(`{"model":"bridge-test","input":[{"type":"message","role":"user","content":"summarize"},{"type":"message","role":"user","content":"more"}]}`),
		},
	}))
	pluginErrValue := mustPluginErr(t, err, errCodeCompactBridgeFailed)
	if pluginErrValue.HTTPStatus != http.StatusBadGateway || pluginErrValue.Message != "bridged compaction failed" {
		t.Fatalf("unexpected V1 error payload: %+v", pluginErrValue)
	}
}

func TestHandleExecuteV1RejectsNonArrayInputBeforeHostCallback(t *testing.T) {
	installTestConfig(t, "rules:\n  - match: 'bridge-*'\n    action: bridge\n")
	for name, body := range map[string][]byte{
		"scalar input": []byte(`{"model":"bridge-test","input":"summarize this"}`),
		"object input": []byte(`{"model":"bridge-test","input":{"type":"message","role":"user","content":"summarize this"}}`),
		"malformed":    []byte(`{"model":"bridge-test","input":`),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := handleMethod(pluginabi.MethodExecutorExecute, executorRequestJSON(t, rpcExecutorRequest{
				ExecutorRequest: pluginapi.ExecutorRequest{
					Model:           "bridge-test",
					Alt:             altResponsesCompact,
					Stream:          false,
					OriginalRequest: body,
				},
			}))
			pluginErrValue := mustPluginErr(t, err, errCodeCompactBridgeFailed)
			if pluginErrValue.HTTPStatus != http.StatusBadGateway || pluginErrValue.Message != "bridged compaction failed" {
				t.Fatalf("unexpected V1 input error payload: %+v", pluginErrValue)
			}
		})
	}
}

func TestHandleExecuteV2HostFailureReturnsResponseFailedSSE(t *testing.T) {
	installTestConfig(t, "rules:\n  - match: 'bridge-*'\n    action: bridge\n")
	raw, err := handleMethod(pluginabi.MethodExecutorExecuteStream, executorRequestJSON(t, rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "bridge-test",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"bridge-test","stream":true,"input":[{"type":"message","role":"user","content":"compact this"},{"type":"compaction_trigger"}]}`),
		},
	}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := decodeExecuteSuccess(t, raw)
	headers, _ := result["Headers"].(map[string]any)
	if headers == nil {
		t.Fatalf("missing headers: %+v", result)
	}
	contentType, _ := headers["content-type"].([]any)
	if len(contentType) != 1 || contentType[0] != "text/event-stream" {
		t.Fatalf("content-type = %#v", headers["content-type"])
	}
	chunks, _ := result["Chunks"].([]any)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %#v", result["Chunks"])
	}
	chunk, _ := chunks[0].(map[string]any)
	payload, _ := chunk["Payload"].(string)
	decodedPayload, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !strings.Contains(string(decodedPayload), `"type":"response.failed"`) || !strings.Contains(string(decodedPayload), errCodeCompactBridgeFailed) {
		t.Fatalf("unexpected V2 failed payload: %s", decodedPayload)
	}
	if strings.Contains(string(decodedPayload), "response.completed") {
		t.Fatalf("V2 failed payload must not complete: %s", decodedPayload)
	}
}
