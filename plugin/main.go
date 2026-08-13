package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"net/http"
	"unsafe"

	"github.com/google/uuid"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Plugin identity constants.
const (
	pluginName   = "cpa-codex-compact-bridge"
	pluginAuthor = "patrick-fu"
	pluginRepo   = "https://github.com/patrick-fu/cpa-codex-compact-bridge"
)

// pluginVersion can be overridden in release builds with:
// -ldflags "-X github.com/patrick-fu/cpa-codex-compact-bridge/plugin.pluginVersion=<version>"
var pluginVersion = "0.1.0"

// configHolder holds the active parsed configuration. It is replaced atomically
// on plugin.register / plugin.reconfigure.
var configHolder = atomicConfig{}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	storeHostAPI(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		// Expected bridge failures travel in the RPC envelope so CPA can retain
		// the stable error code and HTTP status (not as a native ABI failure).
		writeResponse(response, errorEnvelopeFrom(errHandle))
		return 0
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, len C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = len
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

// lifecycleRequest is the {config_yaml, schema_version} envelope.
type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

// handleMethod dispatches a plugin RPC method.
func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return handleLifecycle(method, request)
	case pluginabi.MethodExecutorIdentifier:
		return okEnvelope(map[string]any{"identifier": pluginName})
	case pluginabi.MethodModelRoute:
		return handleModelRoute(request)
	case pluginabi.MethodRequestInterceptBefore:
		return handleRequestInterceptBefore(request)
	case pluginabi.MethodRequestInterceptAfter:
		return okEnvelope(pluginapi.RequestInterceptResponse{})
	case pluginabi.MethodExecutorExecute:
		return handleExecute(request, false)
	case pluginabi.MethodExecutorExecuteStream:
		return handleExecute(request, true)
	case pluginabi.MethodExecutorCountTokens:
		return okEnvelope(map[string]any{"Payload": []byte(`{"total_tokens":0}`)})
	case pluginabi.MethodExecutorHTTPRequest:
		return okEnvelope(map[string]any{"StatusCode": 404})
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

// handleLifecycle parses config and (re)stores it.
func handleLifecycle(method string, request []byte) ([]byte, error) {
	var req lifecycleRequest
	if len(request) > 0 {
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, &pluginErr{Code: "config_decode_failed", Message: "decode lifecycle request: " + err.Error()}
		}
	}
	cfg, err := loadConfig(req.ConfigYAML)
	if err != nil {
		return nil, &pluginErr{Code: "config_invalid", Message: err.Error()}
	}
	configHolder.store(cfg)
	return okEnvelope(registration())
}

// registration returns the plugin registration payload declaring model_router,
// executor, and the openai-response input/output formats.
func registration() map[string]any {
	return map[string]any{
		"schema_version": pluginabi.SchemaVersion,
		"metadata": map[string]any{
			"Name":             pluginName,
			"Version":          pluginVersion,
			"Author":           pluginAuthor,
			"GitHubRepository": pluginRepo,
			"ConfigFields":     []any{},
		},
		"capabilities": map[string]any{
			"model_router":            true,
			"executor":                true,
			"request_interceptor":     true,
			"executor_model_scope":    string(pluginapi.ExecutorModelScopeStatic),
			"executor_input_formats":  []string{"openai-response"},
			"executor_output_formats": []string{"openai-response"},
		},
	}
}

// rpcModelRouteRequest mirrors pluginapi.ModelRouteRequest with HostCallbackID.
type rpcModelRouteRequest struct {
	pluginapi.ModelRouteRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// handleModelRoute implements ModelRouter.RouteModel.
func handleModelRoute(request []byte) ([]byte, error) {
	var req rpcModelRouteRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, &pluginErr{Code: "route_decode_failed", Message: "decode model.route request: " + err.Error()}
	}
	cfg := configHolder.load()
	decision := decideRoute(cfg, req.RequestedModel)
	if !decision.Handled || !decision.Bridged || !shouldBridgeRoute(req.ModelRouteRequest) {
		// Not bridged: let CPA keep it on the built-in route.
		return okEnvelope(pluginapi.ModelRouteResponse{Handled: false})
	}
	return okEnvelope(pluginapi.ModelRouteResponse{
		Handled:    true,
		TargetKind: pluginapi.ModelRouteTargetSelf,
		Reason:     "bridged compact route",
	})
}

// shouldBridgeRoute leaves ordinary streaming turns on CPA's built-in path.
// Their replay state is normalized later by RequestInterceptor. Only a V2
// trigger needs the executor; non-streaming requests stay routed so V1's
// Alt-only compact endpoint can be recognized later by the executor.
func shouldBridgeRoute(req pluginapi.ModelRouteRequest) bool {
	if !req.Stream {
		return true
	}
	items, ok := parseRequestInputItems(req.Body)
	if !ok {
		return false
	}
	return detectV2Trigger(items)
}

// rpcExecutorRequest mirrors pluginapi.ExecutorRequest with HostCallbackID.
type rpcExecutorRequest struct {
	pluginapi.ExecutorRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// handleExecute implements ProviderExecutor.Execute / ExecuteStream.
func handleExecute(request []byte, stream bool) ([]byte, error) {
	var req rpcExecutorRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, &pluginErr{Code: "execute_decode_failed", Message: "decode executor request: " + err.Error()}
	}
	cfg := configHolder.load()
	decision := decideRoute(cfg, req.Model)
	if !decision.Handled || !decision.Bridged {
		// Should not reach the executor for a non-bridged model, but if it does,
		// delegate to the host as an ordinary request to stay safe.
		return delegateOrdinary(req, stream)
	}

	if detectV1Compact(req.Alt, req.Stream) {
		return executeV1Compact(cfg, req, decision)
	}
	if stream && detectV2TriggerFromRequest(req.OriginalRequest) {
		return executeV2Compact(cfg, req, decision)
	}
	if stream {
		// Streaming replay normalization is performed by the request interceptor
		// after CPA has built its WebSocket transcript. Reaching this executor for
		// any other stream would otherwise require a nested stream callback.
		return nil, &pluginErr{Code: errCodeCompactBridgeFailed, Message: "unexpected ordinary streaming bridge request", HTTPStatus: http.StatusBadGateway}
	}
	// Ordinary turn on a bridged model: replay-normalize then delegate.
	return executeOrdinaryBridged(req, stream)
}

// detectV2TriggerFromRequest inspects the original request body for a trailing
// compaction_trigger.
func detectV2TriggerFromRequest(body []byte) bool {
	items, ok := parseRequestInputItems(body)
	if !ok {
		return false
	}
	return detectV2Trigger(items)
}

// executeV1Compact handles the non-streaming /responses/compact endpoint.
func executeV1Compact(cfg Config, req rpcExecutorRequest, decision matchDecision) ([]byte, error) {
	items, ok := parseRequestInputItems(req.OriginalRequest)
	if !ok {
		return nil, &pluginErr{Code: errCodeCompactBridgeFailed, Message: "bridged compaction failed", HTTPStatus: 502}
	}
	summaryModel := pickSummaryModel(req, decision)
	summary, err := generateSummary(cfg, req, summaryModel, req.HostCallbackID)
	if err != nil {
		return nil, &pluginErr{
			Code:       errCodeCompactBridgeFailed,
			Message:    "bridged compaction failed",
			HTTPStatus: 502,
		}
	}
	itemID := compactionIDPrefix + uuid.NewString()
	body, err := v1CompactResponseBody(items, itemID, summary)
	if err != nil {
		return nil, &pluginErr{Code: errCodeCompactBridgeFailed, Message: "bridged compaction failed", HTTPStatus: 502}
	}
	return okEnvelope(map[string]any{
		"Payload": body,
		"Headers": map[string][]string{"content-type": {"application/json"}},
	})
}

// executeV2Compact handles a streaming request whose final input is a trigger.
func executeV2Compact(cfg Config, req rpcExecutorRequest, decision matchDecision) ([]byte, error) {
	summaryModel := pickSummaryModel(req, decision)
	summary, err := generateSummary(cfg, req, summaryModel, req.HostCallbackID)
	if err != nil {
		// V2 failure: emit response.failed (no partial, no completed).
		failed := v2ResponseFailedSSE("bridged compaction failed")
		return okEnvelope(map[string]any{
			"Headers": map[string][]string{"content-type": {"text/event-stream"}},
			"Chunks":  []map[string]any{{"Payload": failed}},
		})
	}
	itemID := compactionIDPrefix + uuid.NewString()
	events, err := v2SSEEvents(itemID, summary)
	if err != nil {
		failed := v2ResponseFailedSSE("bridged compaction failed: encode error")
		return okEnvelope(map[string]any{
			"Headers": map[string][]string{"content-type": {"text/event-stream"}},
			"Chunks":  []map[string]any{{"Payload": failed}},
		})
	}
	return okEnvelope(map[string]any{
		"Headers": map[string][]string{"content-type": {"text/event-stream"}},
		"Chunks":  []map[string]any{{"Payload": events}},
	})
}

// executeOrdinaryBridged normalizes cpa_compact_ items for replay then delegates
// the ordinary turn to CPA via host.model callback.
func executeOrdinaryBridged(req rpcExecutorRequest, stream bool) ([]byte, error) {
	result, err := normalizeForReplay(req.OriginalRequest)
	if err != nil {
		return nil, &pluginErr{Code: errCodeCompactBridgeFailed, Message: err.Error(), HTTPStatus: 502}
	}
	if !result.Normalized {
		return nil, &pluginErr{Code: errCodeCompactBridgeFailed, Message: "fail-closed: unknown compaction item", HTTPStatus: 502}
	}
	req.OriginalRequest = result.Body
	return delegateOrdinary(req, stream)
}

// delegateOrdinary delegates a request to CPA via host.model callback.
func delegateOrdinary(req rpcExecutorRequest, stream bool) ([]byte, error) {
	resp, err := callHostModelExecute(hostModelExecutionRequest{
		EntryProtocol:  "openai-response",
		ExitProtocol:   "openai-response",
		Model:          req.Model,
		Stream:         stream,
		Body:           req.OriginalRequest,
		Headers:        headerToMap(req.Headers),
		Query:          valuesToMap(req.Query),
		Alt:            req.Alt,
		HostCallbackID: req.HostCallbackID,
	})
	if err != nil {
		return nil, &pluginErr{Code: errCodeCompactBridgeFailed, Message: err.Error(), HTTPStatus: 502}
	}
	return okEnvelope(map[string]any{
		"Payload":  resp.Body,
		"Headers":  resp.Headers,
		"Metadata": map[string]any{},
	})
}

// pickSummaryModel selects the summary model: the rule override if set,
// otherwise the request model itself.
func pickSummaryModel(req rpcExecutorRequest, decision matchDecision) string {
	if decision.SummaryModel != "" {
		return decision.SummaryModel
	}
	return req.Model
}

// generateSummary builds a clean summary request (no compaction items, no
// trigger) and calls the summary model via host.model.execute.
func generateSummary(cfg Config, req rpcExecutorRequest, summaryModel, hostCallbackID string) (string, error) {
	items, ok := parseRequestInputItems(req.OriginalRequest)
	if !ok {
		return "", &pluginErr{Code: "summary_parse_failed", Message: "parse request input for summary"}
	}
	items = removeLastCompactionTrigger(items)
	cleanInput, err := buildSummaryRequestInput(items)
	if err != nil {
		return "", err
	}
	summaryBody, err := buildSummaryRequestBody(req, summaryModel, cleanInput)
	if err != nil {
		return "", err
	}
	resp, err := callHostModelExecute(hostModelExecutionRequest{
		EntryProtocol:  "openai-response",
		ExitProtocol:   "openai-response",
		Model:          summaryModel,
		Stream:         false,
		Body:           summaryBody,
		HostCallbackID: hostCallbackID,
	})
	if err != nil {
		return "", err
	}
	text, err := extractAssistantText(resp.Body)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", &pluginErr{Code: "empty_summary", Message: "summary model produced no text"}
	}
	return text, nil
}

// buildSummaryRequestBody constructs the summary request body: keep the model,
// disable streaming, and replace input with the de-compacted items.
func buildSummaryRequestBody(req rpcExecutorRequest, summaryModel string, cleanInput []json.RawMessage) ([]byte, error) {
	var parsed map[string]json.RawMessage
	if len(req.OriginalRequest) > 0 {
		if err := json.Unmarshal(req.OriginalRequest, &parsed); err != nil {
			return nil, fmtSummaryBody(err)
		}
	} else {
		parsed = map[string]json.RawMessage{}
	}
	parsed["model"] = jsonRawString(summaryModel)
	parsed["stream"] = jsonRawFalse()
	compactInstruction, err := json.Marshal(map[string]any{
		"type":    "message",
		"role":    "user",
		"content": "Summarize the conversation and current task for a successor coding agent. Preserve goals, decisions, constraints, completed work, important file paths, commands and results, and unresolved risks. Return only the handoff summary.",
	})
	if err != nil {
		return nil, fmtSummaryBody(err)
	}
	cleanInput = append(cleanInput, compactInstruction)
	inputEncoded, err := json.Marshal(cleanInput)
	if err != nil {
		return nil, fmtSummaryBody(err)
	}
	parsed["input"] = inputEncoded
	out, err := json.Marshal(parsed)
	if err != nil {
		return nil, fmtSummaryBody(err)
	}
	return out, nil
}

// parseRequestInputItems extracts the input items array from a request body.
func parseRequestInputItems(body []byte) ([]inputItem, bool) {
	if len(body) == 0 {
		return nil, false
	}
	var parsed struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, false
	}
	return parseInputItems(parsed.Input), true
}

// pluginErr carries a code, message, and optional HTTP status.
type pluginErr struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *pluginErr) Error() string {
	return e.Code + ": " + e.Message
}

// okEnvelope wraps a value in a successful RPC envelope.
func okEnvelope(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

// errorEnvelope builds a failed RPC envelope.
func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func errorEnvelopeFrom(err error) []byte {
	var bridgeErr *pluginErr
	if errors.As(err, &bridgeErr) {
		raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{
			Code:       bridgeErr.Code,
			Message:    bridgeErr.Message,
			HTTPStatus: bridgeErr.HTTPStatus,
		}})
		return raw
	}
	return errorEnvelope("plugin_error", err.Error())
}

// writeResponse copies raw into the C response buffer.
func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

// fmtSummaryBody wraps a JSON encoding error.
func fmtSummaryBody(err error) error {
	return &pluginErr{Code: "summary_encode_failed", Message: "encode summary body: " + err.Error()}
}

func jsonRawString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func jsonRawFalse() json.RawMessage {
	return json.RawMessage("false")
}
