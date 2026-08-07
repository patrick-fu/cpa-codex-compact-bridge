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

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// envelope is the RPC envelope {ok, result, error}.
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

// hostCallbackResult is a decoded host model callback response.
type hostCallbackResult struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

// callHostModelExecute invokes host.model.execute (non-stream) and returns the
// model execution response body. It forwards HostCallbackID to prevent the
// host from re-entering this plugin's interceptors on the nested request.
func callHostModelExecute(req hostModelExecutionRequest) (hostCallbackResult, error) {
	result, err := callHost(pluginabi.MethodHostModelExecute, req)
	if err != nil {
		return hostCallbackResult{}, err
	}
	var resp struct {
		StatusCode int                 `json:"status_code"`
		Headers    map[string][]string `json:"headers"`
		Body       []byte              `json:"body"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return hostCallbackResult{}, fmtHostDecode("host.model.execute", err)
	}
	if resp.StatusCode >= 400 {
		return hostCallbackResult{StatusCode: resp.StatusCode, Headers: resp.Headers, Body: resp.Body},
			&hostModelError{StatusCode: resp.StatusCode}
	}
	return hostCallbackResult{StatusCode: resp.StatusCode, Headers: resp.Headers, Body: resp.Body}, nil
}

// hostModelExecutionRequest is the host.model.execute request payload with the
// anti-recursion HostCallbackID.
type hostModelExecutionRequest struct {
	EntryProtocol  string              `json:"entry_protocol"`
	ExitProtocol   string              `json:"exit_protocol"`
	Model          string              `json:"model"`
	Stream         bool                `json:"stream"`
	Body           []byte              `json:"body"`
	Headers        map[string][]string `json:"headers"`
	Query          map[string][]string `json:"query"`
	Alt            string              `json:"alt"`
	HostCallbackID string              `json:"host_callback_id,omitempty"`
}

// callHost is the low-level host callback dispatcher. It marshals the payload,
// invokes the C host API, decodes the envelope, and returns the Result bytes.
func callHost(method string, payload any) (json.RawMessage, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmtHostMarshal(method, err)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var response C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(rawPayload) > 0 {
		cPayload := C.CBytes(rawPayload)
		if cPayload == nil {
			return nil, fmtHostMarshal(method, nil)
		}
		defer C.free(cPayload)
		requestPtr = (*C.uint8_t)(cPayload)
	}
	callCode := C.call_host_api(cMethod, requestPtr, C.size_t(len(rawPayload)), &response)
	var rawResponse []byte
	if response.ptr != nil && response.len > 0 {
		rawResponse = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len)
	}
	if len(rawResponse) == 0 {
		return nil, fmtHostNoResponse(method, int(callCode))
	}
	var env envelope
	if err := json.Unmarshal(rawResponse, &env); err != nil {
		return nil, fmtHostDecode(method, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, &hostCallbackErr{Code: env.Error.Code, Message: env.Error.Message}
		}
		return nil, fmtHostFailed(method)
	}
	if callCode != 0 {
		return nil, fmtHostCode(method, int(callCode))
	}
	return append(json.RawMessage(nil), env.Result...), nil
}

// storeHostAPI stores the host API pointer for later use by callHost. It must
// live in the same translation unit as the store_host_api static helper.
func storeHostAPI(host *C.cliproxy_host_api) {
	C.store_host_api(host)
}

// --- host callback error helpers ---

type hostCallbackErr struct {
	Code    string
	Message string
}

func (e *hostCallbackErr) Error() string {
	return e.Code + ": " + e.Message
}

type hostModelError struct {
	StatusCode int
}

func (e *hostModelError) Error() string {
	return "model execution failed with status"
}

func fmtHostMarshal(method string, err error) error {
	if err == nil {
		return &hostCallbackErr{Code: "host_alloc_failed", Message: "allocate host callback payload " + method}
	}
	return &hostCallbackErr{Code: "host_marshal_failed", Message: "marshal host callback payload " + method + ": " + err.Error()}
}

func fmtHostNoResponse(method string, code int) error {
	return &hostCallbackErr{Code: "host_no_response", Message: "host callback " + method + " returned no response, code=" + itoa(code)}
}

func fmtHostDecode(method string, err error) error {
	return &hostCallbackErr{Code: "host_decode_failed", Message: "decode host callback envelope " + method + ": " + err.Error()}
}

func fmtHostStreamRead(streamID string, message string) error {
	return &hostCallbackErr{Code: "host_stream_failed", Message: fmt.Sprintf("host stream %s: %s", streamID, message)}
}

func fmtHostFailed(method string) error {
	return &hostCallbackErr{Code: "host_call_failed", Message: "host callback " + method + " failed"}
}

func fmtHostCode(method string, code int) error {
	return &hostCallbackErr{Code: "host_call_failed", Message: "host callback " + method + " returned code=" + itoa(code)}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
