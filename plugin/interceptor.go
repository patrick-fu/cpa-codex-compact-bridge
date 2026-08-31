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

// normalizeInterceptedReplay applies the compaction state policy to every
// Responses request, whichever route its target model uses. Bridge state is
// plugin-owned and always normalizes onward; opaque native state only continues
// on a route an explicit `passthrough` rule declared native-compatible.
func normalizeInterceptedReplay(cfg Config, req pluginapi.RequestInterceptRequest) pluginapi.RequestInterceptResponse {
	if req.SourceFormat != "openai-response" {
		return pluginapi.RequestInterceptResponse{}
	}
	target := compactionTargetFor(decideRoute(cfg, req.RequestedModel))
	items, ok := parseRequestInputItems(req.Body)
	if !ok || !hasCompactionItems(items) {
		return pluginapi.RequestInterceptResponse{}
	}
	result, err := normalizeCompactionState(req.Body, target)
	if err != nil {
		if stateErr := asInvalidCompactionState(err); stateErr != nil {
			return pluginapi.RequestInterceptResponse{
				Terminate:       true,
				StatusCode:      stateErr.HTTPStatus,
				ResponseHeaders: jsonHeaders(),
				ResponseBody:    errorBody(stateErr.Code, stateErr.Message, "invalid_request_error"),
			}
		}
		return pluginapi.RequestInterceptResponse{
			Terminate:       true,
			StatusCode:      http.StatusBadGateway,
			ResponseHeaders: jsonHeaders(),
			ResponseBody:    compactBridgeFailureBody(),
		}
	}
	if !result.Changed {
		return pluginapi.RequestInterceptResponse{}
	}
	return pluginapi.RequestInterceptResponse{Body: result.Body}
}

// compactBridgeFailureBody is the retryable runtime failure returned when the
// facade cannot normalize state it already owns.
func compactBridgeFailureBody() []byte {
	return errorBody(errCodeCompactBridgeFailed, "bridged compaction failed", "server_error")
}

// errorBody builds a standard OpenAI error envelope.
func errorBody(code, message, errType string) []byte {
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	})
	return body
}

func jsonHeaders() http.Header {
	return http.Header{"Content-Type": []string{"application/json"}}
}
