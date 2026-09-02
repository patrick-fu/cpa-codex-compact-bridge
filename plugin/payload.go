package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// v1CompactResponseBody builds the non-streaming V1 compact response body.
// It mirrors the current remote compact contract: retain user/developer input
// messages and append one canonical compaction item carrying the summary.
func v1CompactResponseBody(items []inputItem, itemID, summary string) ([]byte, error) {
	output := make([]json.RawMessage, 0, len(items)+1)
	for _, item := range items {
		if item.Type == "message" && (item.Role == "user" || item.Role == "developer") {
			output = append(output, item.Raw)
		}
	}
	compactionItem, err := v2CompactionItemJSON(itemID, summary)
	if err != nil {
		return nil, err
	}
	output = append(output, compactionItem)
	body := map[string]any{
		"output": output,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode v1 compact body: %w", err)
	}
	return encoded, nil
}

// v2CompactionItemJSON builds the plaintext V2 compaction item object.
// {"encrypted_content":"<summary>","id":"cpa_compact_<uuid>","type":"compaction"}
func v2CompactionItemJSON(id, summary string) ([]byte, error) {
	if strings.TrimSpace(summary) == "" {
		return nil, fmt.Errorf("refusing to store a blank compaction summary")
	}
	item := map[string]any{
		"type":              compactionType,
		"id":                id,
		"encrypted_content": summary,
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("encode v2 compaction item: %w", err)
	}
	return encoded, nil
}

// v2SSEEvents builds exactly the two SSE frames for a V2 compact response:
//
//	data: {"item":{"encrypted_content":"...","id":"cpa_compact_<uuid>","type":"compaction"},"type":"response.output_item.done"}
//	data: {"response":{"id":"resp_cpa_compact_<uuid>"},"type":"response.completed"}
//
// Map keys encode in dictionary order; consumers must not depend on field adjacency or order.
// No partial, no created, no invented usage.
func v2SSEEvents(itemID, summary string) ([]byte, error) {
	compactionItem, err := v2CompactionItemJSON(itemID, summary)
	if err != nil {
		return nil, err
	}
	outputItemDone := map[string]any{
		"type": "response.output_item.done",
		"item": json.RawMessage(compactionItem),
	}
	completed := map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"id": "resp_" + itemID},
	}
	doneBytes, err := json.Marshal(outputItemDone)
	if err != nil {
		return nil, fmt.Errorf("encode v2 output_item.done: %w", err)
	}
	completedBytes, err := json.Marshal(completed)
	if err != nil {
		return nil, fmt.Errorf("encode v2 response.completed: %w", err)
	}
	var out []byte
	out = append(out, []byte("data: ")...)
	out = append(out, doneBytes...)
	out = append(out, '\n', '\n')
	out = append(out, []byte("data: ")...)
	out = append(out, completedBytes...)
	out = append(out, '\n', '\n')
	return out, nil
}

// v2ResponseFailedSSE builds the single V2 failure frame:
//
//	data: {"response":{"error":{"code":"compact_bridge_failed","message":"<msg>"},"id":"resp_cpa_compact_failed"},"type":"response.failed"}
//
// The caller closes the stream after this without response.completed.
func v2ResponseFailedSSE(message string) []byte {
	failed := map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id": "resp_cpa_compact_failed",
			"error": map[string]any{
				"code":    errCodeCompactBridgeFailed,
				"message": message,
			},
		},
	}
	raw, _ := json.Marshal(failed)
	var out []byte
	out = append(out, []byte("data: ")...)
	out = append(out, raw...)
	out = append(out, '\n', '\n')
	return out
}

// errCodeCompactBridgeFailed is the stable failure code for retryable
// runtime compact failures: summary generation, network, and upstream errors.
const errCodeCompactBridgeFailed = "compact_bridge_failed"

// errCodeInvalidCompactionState is the stable failure code for compaction
// state that can never be continued. It is a client error and is not retried.
const errCodeInvalidCompactionState = "invalid_compaction_state"

// extractAssistantText accepts only terminal Responses and Chat Completions
// summary responses. A compaction must never persist partial or tool-call
// output as session state.
func extractAssistantText(body []byte) (string, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", err
	}
	output, hasOutput := envelope["output"]
	choices, hasChoices := envelope["choices"]
	if hasOutput && hasChoices {
		return "", summaryFailure("summary response has multiple terminal shapes")
	}
	if hasOutput {
		if !isJSONArray(output) {
			return "", summaryFailure("summary response has unknown terminal shape")
		}
		return extractResponsesAssistantText(envelope)
	}
	if hasChoices {
		if !isJSONArray(choices) {
			return "", summaryFailure("summary response has unknown terminal shape")
		}
		return extractChatAssistantText(envelope)
	}
	if _, hasStatus := envelope["status"]; hasStatus {
		return extractResponsesAssistantText(envelope)
	}
	return "", summaryFailure("summary response has unknown terminal shape")
}

func isJSONArray(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return len(trimmed) > 0 && trimmed[0] == '['
}

type responseOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responseOutputItem struct {
	Type    string                  `json:"type"`
	Role    string                  `json:"role"`
	Content []responseOutputContent `json:"content"`
}

func extractResponsesAssistantText(envelope map[string]json.RawMessage) (string, error) {
	var output []responseOutputItem
	if raw, ok := envelope["output"]; ok {
		if err := json.Unmarshal(raw, &output); err != nil {
			return "", err
		}
	}
	status, err := responseStatus(envelope)
	if err != nil {
		return "", err
	}
	if err := responseOutputItemFailure(output); err != nil {
		return "", err
	}
	if status == "failed" {
		return "", summaryFailure("summary upstream failed (status=failed)")
	}
	if status == "incomplete" {
		return "", incompleteResponseFailure(envelope)
	}
	if hasJSONValue(envelope["incomplete_details"]) {
		return "", incompleteResponseFailure(envelope)
	}
	var text strings.Builder
	for _, item := range output {
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, content := range item.Content {
			if (content.Type == "output_text" || content.Type == "text") && content.Text != "" {
				text.WriteString(content.Text)
			}
		}
	}
	if text.Len() > 0 {
		return text.String(), nil
	}
	return "", summaryFailure("summary model produced no usable text")
}

func responseStatus(envelope map[string]json.RawMessage) (string, error) {
	raw, ok := envelope["status"]
	if !ok || string(raw) == "null" {
		return "", summaryFailure("summary upstream invalid terminal status")
	}
	var status string
	if err := json.Unmarshal(raw, &status); err != nil {
		return "", summaryFailure("summary upstream invalid terminal status")
	}
	switch status {
	case "completed", "incomplete", "failed":
		return status, nil
	default:
		return "", summaryFailure("summary upstream invalid terminal status")
	}
}

func responseOutputItemFailure(output []responseOutputItem) error {
	for _, item := range output {
		switch item.Type {
		case "message", "reasoning":
		case "function_call", "custom_tool_call", "tool_call", "function_call_output", "computer_call", "mcp_call", "web_search_call", "file_search_call", "code_interpreter_call":
			return summaryFailure("summary upstream returned tool call")
		default:
			return summaryFailure("summary upstream returned unsupported output item")
		}
		for _, content := range item.Content {
			switch content.Type {
			case "function_call", "custom_tool_call", "tool_call", "function_call_output":
				return summaryFailure("summary upstream returned tool call")
			}
		}
	}
	return nil
}

func extractChatAssistantText(envelope map[string]json.RawMessage) (string, error) {
	var chat struct {
		Choices []struct {
			FinishReason *string `json:"finish_reason"`
			Message      struct {
				Content   json.RawMessage `json:"content"`
				ToolCalls json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(envelope["choices"], &chat.Choices); err != nil {
		return "", err
	}
	if len(chat.Choices) == 0 {
		return "", summaryFailure("summary text missing terminal status")
	}
	choice := chat.Choices[0]
	if choice.FinishReason == nil {
		return "", summaryFailure("summary text missing terminal status")
	}
	switch *choice.FinishReason {
	case "stop":
	case "length":
		return "", summaryFailure("summary upstream truncated (finish_reason=length)")
	case "max_tokens":
		return "", summaryFailure("summary upstream truncated (finish_reason=max_tokens)")
	case "tool_calls":
		return "", summaryFailure("summary upstream returned tool call")
	case "content_filter":
		return "", summaryFailure("summary upstream incomplete (finish_reason=content_filter)")
	default:
		return "", summaryFailure("summary upstream invalid terminal status")
	}
	if hasJSONValue(choice.Message.ToolCalls) {
		return "", summaryFailure("summary upstream returned tool call")
	}
	var content string
	if err := json.Unmarshal(choice.Message.Content, &content); err != nil {
		return "", summaryFailure("summary model produced no usable text")
	}
	if content == "" {
		return "", summaryFailure("summary model produced no usable text")
	}
	return content, nil
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "[]"
}

func incompleteResponseFailure(envelope map[string]json.RawMessage) error {
	raw, ok := envelope["incomplete_details"]
	if !ok || !hasJSONValue(raw) {
		return summaryFailure("summary upstream incomplete")
	}
	var details struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		return summaryFailure("summary upstream incomplete")
	}
	switch details.Reason {
	case "max_output_tokens", "content_filter":
		return summaryFailure("summary upstream incomplete (reason=%s)", details.Reason)
	default:
		return summaryFailure("summary upstream incomplete")
	}
}

func summaryFailure(format string, args ...any) *pluginErr {
	return &pluginErr{Code: errCodeCompactBridgeFailed, Message: fmt.Sprintf(format, args...)}
}

// extractSummaryText returns the summary text of a Summary Model response. A
// blank result is a runtime compaction failure: storing it would install a
// compaction item that later turns can only reject.
func extractSummaryText(body []byte) (string, error) {
	text, err := extractAssistantText(body)
	if err != nil {
		var summaryErr *pluginErr
		if errors.As(err, &summaryErr) {
			return "", summaryErr
		}
		return "", summaryFailure("summary model produced no usable text")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", summaryFailure("summary model produced no usable text")
	}
	return text, nil
}
