package main

import (
	"encoding/json"
	"fmt"
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

// errCodeCompactBridgeFailed is the stable failure code for both V1 and V2.
const errCodeCompactBridgeFailed = "compact_bridge_failed"

// extractAssistantText extracts the plain text from a Responses or
// chat-completions assistant message produced by the summary model. It handles
// output_text content parts and simple string content.
func extractAssistantText(body []byte) (string, error) {
	// Responses-style: {output:[{role:assistant, content:[{type:output_text,text}]}]}
	var responses struct {
		Output []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &responses); err == nil {
		var sb []byte
		for _, out := range responses.Output {
			for _, c := range out.Content {
				if c.Type == "output_text" && c.Text != "" {
					sb = append(sb, c.Text...)
				}
			}
		}
		if len(sb) > 0 {
			return string(sb), nil
		}
	}
	// Chat-completions-style: {choices:[{message:{content}}]}
	var chat struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &chat); err == nil && len(chat.Choices) > 0 && chat.Choices[0].Message.Content != "" {
		return chat.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("no assistant text found in summary model response")
}
