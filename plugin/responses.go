package main

import "encoding/json"

// inputItem is a minimal view of a Responses API input item. Only the fields
// relevant to compaction detection and replay normalization are decoded.
type inputItem struct {
	Type             string          `json:"type"`
	ID               string          `json:"id,omitempty"`
	Role             string          `json:"role,omitempty"`
	EncryptedContent string          `json:"encrypted_content,omitempty"`
	Raw              json.RawMessage `json:"-"`
}

// responsesRequestBody is the minimal Responses request shape the bridge
// inspects. Extra fields are preserved for passthrough/replay delegation.
type responsesRequestBody struct {
	Model  string            `json:"model"`
	Stream bool              `json:"stream"`
	Input  []json.RawMessage `json:"input"`
	Raw    json.RawMessage   `json:"-"`
}

// compactionIDPrefix marks plaintext compaction items produced by this plugin.
const compactionIDPrefix = "cpa_compact_"

// compactionTriggerType is the V2 trigger input item type.
const compactionTriggerType = "compaction_trigger"

// compactionType is the persisted compaction input item type.
const compactionType = "compaction"

// parseInputItems decodes only the discriminator fields needed by the bridge.
func parseInputItems(raw []json.RawMessage) []inputItem {
	items := make([]inputItem, 0, len(raw))
	for _, entry := range raw {
		var item inputItem
		_ = json.Unmarshal(entry, &item)
		item.Raw = entry
		items = append(items, item)
	}
	return items
}

// isCPACompaction reports whether an item is a plaintext compaction item that
// this plugin produced (id begins cpa_compact_).
func isCPACompaction(item inputItem) bool {
	return item.Type == compactionType && len(item.ID) > len(compactionIDPrefix) &&
		item.ID[:len(compactionIDPrefix)] == compactionIDPrefix
}

// isCompactionTrigger reports whether an item is a V2 compaction trigger.
func isCompactionTrigger(item inputItem) bool {
	return item.Type == compactionTriggerType
}

// isCompactionItemAny reports whether an item is any compaction item (known or
// unknown marker). This is used for the summary "de-compact" step and for the
// fail-closed replay check.
func isCompactionItemAny(item inputItem) bool {
	return item.Type == compactionType
}

// detectV2Trigger reports whether the last input item is a compaction_trigger.
func detectV2Trigger(items []inputItem) bool {
	if len(items) == 0 {
		return false
	}
	return isCompactionTrigger(items[len(items)-1])
}

// detectV1Compact reports whether a request is a V1 compact request. V1 is the
// non-streaming Alt "responses/compact" endpoint.
func detectV1Compact(alt string, stream bool) bool {
	return alt == altResponsesCompact && !stream
}

// altResponsesCompact is the CPA alt value for the V1 compact endpoint.
const altResponsesCompact = "responses/compact"

// removeLastCompactionTrigger returns the input items with a trailing trigger
// removed. It is a no-op when the last item is not a trigger.
func removeLastCompactionTrigger(items []inputItem) []inputItem {
	if len(items) == 0 || !isCompactionTrigger(items[len(items)-1]) {
		return items
	}
	out := make([]inputItem, len(items)-1)
	copy(out, items[:len(items)-1])
	return out
}

// compactToUserMessage converts a plaintext compaction item into an ordinary
// user message carrying the stored plaintext summary.
func compactToUserMessage(item inputItem) map[string]any {
	return map[string]any{
		"type":    "message",
		"role":    "user",
		"content": item.EncryptedContent,
	}
}
