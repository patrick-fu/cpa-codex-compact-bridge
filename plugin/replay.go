package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// compactTarget is the route a request is about to follow, as recorded by the
// Bridge Rule evaluation. The compaction state policy depends on it because
// only a route an administrator declared native-compatible may carry opaque
// compaction state onward. The values are ordered from most to least permissive
// so the state checks can rely on monotonic strictness.
type compactTarget int

const (
	// targetExplicitPassthrough matched a `passthrough` Bridge Rule: the admin
	// declares that this route's provider can interpret its own compact state.
	targetExplicitPassthrough compactTarget = iota
	// targetUnmatchedPassthrough has no Bridge Rule at all. CPA keeps the
	// request on its built-in route, but nothing declares that route
	// compact-capable, so it is stricter than an explicit rule.
	targetUnmatchedPassthrough
	// targetBridge hands the request to the Compact Bridge Facade, which can
	// only interpret its own plaintext state.
	targetBridge
)

// String names the target in test failures and diagnostics.
func (t compactTarget) String() string {
	switch t {
	case targetExplicitPassthrough:
		return "explicit passthrough"
	case targetUnmatchedPassthrough:
		return "unmatched passthrough"
	case targetBridge:
		return "bridge"
	}
	return "unknown target"
}

// mayForwardOpaqueState reports whether the target may carry opaque,
// provider-owned compaction state onward. The plugin cannot identify which
// route created that state, so only an explicit `passthrough` rule (a
// native-compatible boundary declared by the administrator) can vouch for it.
func (t compactTarget) mayForwardOpaqueState() bool {
	return t == targetExplicitPassthrough
}

// compactionRecord is what one compaction input item carries.
type compactionRecord int

const (
	// compactionOpaque is provider-owned encrypted state the facade cannot read.
	compactionOpaque compactionRecord = iota
	// compactionPlaintext is valid cpa_compact_* plaintext summary state.
	compactionPlaintext
	// compactionCorrupt is cpa_compact_* state whose summary text is unusable.
	compactionCorrupt
)

// Stable fail-closed messages for deterministic compaction state errors. These
// reach the Codex client verbatim, so they name the actionable remedy.
const (
	msgCorruptBridgeCompaction  = "bridged compaction state has no summary text; start a new session"
	msgMixedCompactionState     = "request mixes bridged and native compaction state; start a new session"
	msgNativeCompactionOnBridge = "native compaction state cannot continue on a bridged model; switch back to the model that created it or start a new session"
	// msgUnruledNativeCompaction is returned when opaque native state arrives on
	// a model with no Bridge Rule: CPA keeps the request on its built-in route,
	// but no rule declares that route able to interpret the state.
	msgUnruledNativeCompaction = "native compaction state has no matching passthrough rule; add a passthrough rule for a compact-capable model or start a new session"
)

// replayResult is the outcome of the compaction state policy on a request body.
type replayResult struct {
	// Changed is true when Body carries rewritten input items. A false result
	// means the request must be forwarded exactly as received.
	Changed bool
	// Body is the request body to delegate downstream.
	Body []byte
}

// compactionRecordOf classifies one compaction item. Only the plugin's own
// marker identifies readable state, and a marked item without summary text is
// corrupt rather than recoverable.
func compactionRecordOf(item inputItem) compactionRecord {
	if !isCPACompaction(item) {
		return compactionOpaque
	}
	if strings.TrimSpace(item.EncryptedContent) == "" {
		return compactionCorrupt
	}
	return compactionPlaintext
}

// invalidCompactionStateError builds the deterministic, client-side failure for
// compaction state that cannot be continued. Status 400 keeps the Codex client
// from spending a retry on a state error that cannot resolve itself.
func invalidCompactionStateError(message string) *pluginErr {
	return &pluginErr{Code: errCodeInvalidCompactionState, Message: message, HTTPStatus: http.StatusBadRequest}
}

// asInvalidCompactionState returns err when it is a deterministic state error so
// callers can keep runtime and network failures on the retryable code.
func asInvalidCompactionState(err error) *pluginErr {
	var stateErr *pluginErr
	if errors.As(err, &stateErr) && stateErr.Code == errCodeInvalidCompactionState {
		return stateErr
	}
	return nil
}

// normalizeCompactionState applies the one-way compaction portability policy to
// a request body: valid cpa_compact_* plaintext state always becomes an
// ordinary user summary on every target, opaque native state only continues
// unchanged on a target an explicit `passthrough` rule declared
// native-compatible, and mixed or corrupt state fails closed everywhere.
func normalizeCompactionState(body []byte, target compactTarget) (replayResult, error) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		return replayResult{}, fmt.Errorf("decode request body for replay: %w", err)
	}
	inputRaw, hasInput := parsed["input"]
	if !hasInput {
		// No input array: nothing to normalize.
		return replayResult{Body: body}, nil
	}
	if input := bytes.TrimSpace(inputRaw); len(input) == 0 || input[0] != '[' {
		// Responses also accepts scalar and object input. Compaction items are
		// represented only in an item array, so other shapes pass through
		// unchanged.
		return replayResult{Body: body}, nil
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(inputRaw, &rawItems); err != nil {
		return replayResult{}, fmt.Errorf("decode input items for replay: %w", err)
	}
	rewritten, changed, err := rewriteCompactionItems(parseInputItems(rawItems), target)
	if err != nil {
		return replayResult{}, err
	}
	if !changed {
		return replayResult{Body: body}, nil
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
	return replayResult{Changed: true, Body: out}, nil
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

// rewriteCompactionItems maps each item to its representation for the given
// target. It reports changed=false when every item can continue as received.
func rewriteCompactionItems(items []inputItem, target compactTarget) ([]json.RawMessage, bool, error) {
	if err := checkCompactionItems(items, target); err != nil {
		return nil, false, err
	}
	out := make([]json.RawMessage, 0, len(items))
	changed := false
	for _, item := range items {
		if isCompactionItemAny(item) && compactionRecordOf(item) == compactionPlaintext {
			msg, err := json.Marshal(compactToUserMessage(item))
			if err != nil {
				return nil, false, fmt.Errorf("encode replay user message: %w", err)
			}
			out = append(out, msg)
			changed = true
			continue
		}
		out = append(out, item.Raw)
	}
	return out, changed, nil
}

// checkCompactionItems fails closed on state the target cannot safely continue:
// corrupt bridge state, bridge state mixed with native state, and native state
// on any route that no explicit `passthrough` rule declared native-compatible.
func checkCompactionItems(items []inputItem, target compactTarget) error {
	var plaintext, opaque, corrupt int
	for _, item := range items {
		if !isCompactionItemAny(item) {
			continue
		}
		switch compactionRecordOf(item) {
		case compactionPlaintext:
			plaintext++
		case compactionCorrupt:
			corrupt++
		default:
			opaque++
		}
	}
	switch {
	case corrupt > 0:
		return invalidCompactionStateError(msgCorruptBridgeCompaction)
	case plaintext > 0 && opaque > 0:
		return invalidCompactionStateError(msgMixedCompactionState)
	case opaque > 0 && !target.mayForwardOpaqueState():
		if target == targetBridge {
			return invalidCompactionStateError(msgNativeCompactionOnBridge)
		}
		return invalidCompactionStateError(msgUnruledNativeCompaction)
	}
	return nil
}

// buildSummaryRequestInput constructs the de-compacted input for the summary
// model request. A summary turn is always bridge-owned, so opaque native state
// fails closed there. Every compaction trigger item is removed.
func buildSummaryRequestInput(items []inputItem) ([]json.RawMessage, error) {
	kept := make([]inputItem, 0, len(items))
	for _, item := range items {
		if isCompactionTrigger(item) {
			continue
		}
		kept = append(kept, item)
	}
	rewritten, _, err := rewriteCompactionItems(kept, targetBridge)
	if err != nil {
		return nil, err
	}
	return rewritten, nil
}
