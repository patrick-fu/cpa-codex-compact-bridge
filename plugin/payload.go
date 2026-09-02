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
// {"type":"compaction","id":"cpa_compact_<uuid>","encrypted_content":"<summary>"}
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
//	data: {"type":"response.output_item.done","item":{...compaction...}}
//	data: {"type":"response.completed","response":{"id":"resp_cpa_compact_<uuid>"}}
//
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
//	data: {"type":"response.failed",...,"error":{"code":"compact_bridge_failed","message":"<msg>"}}
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
	if hasOutput && isJSONArray(output) {
		return extractResponsesAssistantText(envelope)
	}
	if hasChoices && isJSONArray(choices) {
		return extractChatAssistantText(envelope)
	}
	return "", summaryFailure("summary response has unknown terminal shape")
}

func isJSONArray(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return len(trimmed) > 0 && trimmed[0] == '['
}

func extractResponsesAssistantText(envelope map[string]json.RawMessage) (string, error) {
	var response struct {
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(envelope["output"], &response.Output); err != nil {
		return "", err
	}
	status, hasStatus, err := responseStatus(envelope)
	if err != nil {
		return "", err
	}
	if hasStatus && status != "completed" {
		if status == "failed" {
			return "", summaryFailure("summary upstream failed (status=%s)", status)
		}
		if reason := incompleteReason(envelope); reason != "" {
			return "", summaryFailure("summary upstream incomplete (reason=%s)", reason)
		}
		return "", summaryFailure("summary upstream incomplete (status=%s)", status)
	}
	if responseContainsToolCall(response.Output) {
		return "", summaryFailure("summary upstream returned tool call")
	}
	if reason := incompleteReason(envelope); reason != "" {
		return "", summaryFailure("summary upstream incomplete (reason=%s)", reason)
	}
	if hasJSONValue(envelope["incomplete_details"]) {
		return "", summaryFailure("summary upstream incomplete (reason=unknown)")
	}
	var text strings.Builder
	for _, output := range response.Output {
		if output.Role != "assistant" {
			continue
		}
		for _, content := range output.Content {
			if content.Type == "output_text" && content.Text != "" {
				text.WriteString(content.Text)
			}
		}
	}
	if text.Len() > 0 {
		return text.String(), nil
	}
	if responseContainsReasoning(response.Output) {
		return "", summaryFailure("summary model produced no usable text")
	}
	if !hasStatus {
		return "", summaryFailure("summary text missing terminal status")
	}
	return "", summaryFailure("summary model produced no usable text")
}

func responseStatus(envelope map[string]json.RawMessage) (string, bool, error) {
	raw, ok := envelope["status"]
	if !ok || string(raw) == "null" {
		return "", false, nil
	}
	var status string
	if err := json.Unmarshal(raw, &status); err != nil {
		return "", false, summaryFailure("summary upstream incomplete (status=invalid)")
	}
	return status, true, nil
}

func responseContainsToolCall(output []struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}) bool {
	for _, item := range output {
		switch item.Type {
		case "function_call", "custom_tool_call", "tool_call", "function_call_output":
			return true
		}
		for _, content := range item.Content {
			switch content.Type {
			case "function_call", "custom_tool_call", "tool_call", "function_call_output":
				return true
			}
		}
	}
	return false
}

func responseContainsReasoning(output []struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}) bool {
	for _, item := range output {
		if item.Type == "reasoning" {
			return true
		}
	}
	return false
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
	default:
		return "", summaryFailure("summary upstream incomplete (finish_reason=%s)", *choice.FinishReason)
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

func incompleteReason(envelope map[string]json.RawMessage) string {
	raw, ok := envelope["incomplete_details"]
	if !ok || !hasJSONValue(raw) {
		return ""
	}
	var details struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &details); err != nil || details.Reason == "" {
		return ""
	}
	return details.Reason
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
