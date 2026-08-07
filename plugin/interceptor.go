package main

import (
	"encoding/json"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// rpcRequestInterceptRequest mirrors pluginapi.RequestInterceptRequest with
// HostCallbackID. The interceptor runs after CPA has normalized a WebSocket
// continuation, which is the only point where the compacted history is visible
// for a request that ModelRouter initially declined.
type rpcRequestInterceptRequest struct {
	pluginapi.RequestInterceptRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func handleRequestInterceptBefore(request []byte) ([]byte, error) {
	var req rpcRequestInterceptRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, &pluginErr{Code: "intercept_decode_failed", Message: "decode request interceptor request: " + err.Error()}
	}
	response := normalizeInterceptedReplay(configHolder.load(), req.RequestInterceptRequest)
	return okEnvelope(response)
}

// normalizeInterceptedReplay transforms only marked bridge state. It leaves
// ordinary requests untouched and rejects opaque native compaction state.
func normalizeInterceptedReplay(cfg Config, req pluginapi.RequestInterceptRequest) pluginapi.RequestInterceptResponse {
	decision := decideRoute(cfg, req.RequestedModel)
	if !decision.Handled || !decision.Bridged || req.SourceFormat != "openai-response" {
		return pluginapi.RequestInterceptResponse{}
	}
	items, ok := parseRequestInputItems(req.Body)
	if !ok || !hasCompactionItems(items) {
		return pluginapi.RequestInterceptResponse{}
	}
	result, err := normalizeForReplay(req.Body)
	if err != nil {
		return pluginapi.RequestInterceptResponse{
			Terminate:       true,
			StatusCode:      http.StatusBadGateway,
			ResponseHeaders: http.Header{"Content-Type": []string{"application/json"}},
			ResponseBody:    compactBridgeFailureBody(),
		}
	}
	return pluginapi.RequestInterceptResponse{Body: result.Body}
}

func compactBridgeFailureBody() []byte {
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": "bridged compaction failed",
			"type":    "server_error",
			"code":    errCodeCompactBridgeFailed,
		},
	})
	return body
}
