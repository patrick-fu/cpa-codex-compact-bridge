package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// replayResult is the outcome of replay normalization on an ordinary request.
type replayResult struct {
	// Normalized is true when the request was successfully normalized and is
	// safe to delegate to CPA's ordinary provider route.
	Normalized bool
	// Body is the rewritten request body (input compact items replaced).
	Body []byte
}

// normalizeForReplay inspects an ordinary (non-compact) request body for
// compaction items. Known cpa_compact_ items are rewritten into ordinary user
// messages. Unknown compaction items cause fail-closed (Normalized=false).
func normalizeForReplay(body []byte) (replayResult, error) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		return replayResult{}, fmt.Errorf("decode request body for replay: %w", err)
	}
	inputRaw, hasInput := parsed["input"]
	if !hasInput {
		// No input array: nothing to normalize.
		return replayResult{Normalized: true, Body: body}, nil
	}
	if input := bytes.TrimSpace(inputRaw); len(input) == 0 || input[0] != '[' {
		// Responses also accepts scalar input. Compaction items are represented
		// only in an item array, so non-array input must pass through unchanged.
		return replayResult{Normalized: true, Body: body}, nil
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(inputRaw, &rawItems); err != nil {
		return replayResult{}, fmt.Errorf("decode input items for replay: %w", err)
	}
	items := parseInputItems(rawItems)
	if !hasCompactionItems(items) {
		// Fast path: no compaction items at all.
		return replayResult{Normalized: true, Body: body}, nil
	}
	rewritten, err := rewriteInputItems(items)
	if err != nil {
		return replayResult{}, err
	}
	encoded, err := json.Marshal(rewritten)
	if err != nil {
		return replayResult{}, fmt.Errorf("encode replay input: %w", err)
	}
	parsed["input"] = encoded
	out, err := json.Marshal(parsed)
	if err != nil {
		return replayResult{}, fmt.Errorf("encode replay body: %w", err)
	}
	return replayResult{Normalized: true, Body: out}, nil
}

// hasCompactionItems reports whether any item is a compaction item.
func hasCompactionItems(items []inputItem) bool {
	for _, item := range items {
		if isCompactionItemAny(item) {
			return true
		}
	}
	return false
}

// rewriteInputItems maps each input item to its replay representation. Known
// cpa_compact_ items become user messages; unknown compaction items fail.
func rewriteInputItems(items []inputItem) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		if !isCompactionItemAny(item) {
			out = append(out, item.Raw)
			continue
		}
		if isCPACompaction(item) {
			if strings.TrimSpace(item.EncryptedContent) == "" {
				return nil, fmt.Errorf("cpa_compact_ item %q has empty encrypted_content", item.ID)
			}
			msg, err := json.Marshal(compactToUserMessage(item))
			if err != nil {
				return nil, fmt.Errorf("encode replay user message: %w", err)
			}
			out = append(out, msg)
			continue
		}
		// Unknown compaction item: fail closed.
		return nil, fmt.Errorf("unknown compaction item %q: refusing to forward opaque compact state to upstream", item.ID)
	}
	return out, nil
}

// buildSummaryRequestInput constructs the de-compacted input for the summary
// model request. A marked plaintext compaction is restored as its user summary;
// an unknown compaction fails closed. The final trigger (if present) is removed.
func buildSummaryRequestInput(items []inputItem) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		if isCompactionTrigger(item) {
			continue
		}
		if isCompactionItemAny(item) {
			if !isCPACompaction(item) || strings.TrimSpace(item.EncryptedContent) == "" {
				return nil, fmt.Errorf("unknown compaction item %q: refusing to summarize opaque compact state", item.ID)
			}
			msg, err := json.Marshal(compactToUserMessage(item))
			if err != nil {
				return nil, fmt.Errorf("encode compaction summary: %w", err)
			}
			out = append(out, msg)
			continue
		}
		out = append(out, item.Raw)
	}
	return out, nil
}
