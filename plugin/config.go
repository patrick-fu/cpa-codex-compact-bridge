package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Action is the user-configured disposition for a matching Bridge Rule.
type Action string

const (
	ActionBridge      Action = "bridge"
	ActionPassthrough Action = "passthrough"
)

// Rule is one ordered Bridge Rule entry.
type Rule struct {
	Match        string `yaml:"match"`
	Action       Action `yaml:"action"`
	SummaryModel string `yaml:"summary_model"`
}

// Config is the plugin configuration block (plugins.configs.<pluginID>).
type Config struct {
	Enabled       bool   `yaml:"enabled"`
	Priority      int    `yaml:"priority"`
	Rules         []Rule `yaml:"rules"`
	OnNoMatch     Action `yaml:"on_no_match"`
	CompactPrompt string `yaml:"compact_prompt"`
}

// loadConfig parses the YAML configuration bytes into a Config. It validates
// rule actions and coerces on_no_match to passthrough when unset.
func loadConfig(raw []byte) (Config, error) {
	cfg := Config{OnNoMatch: ActionPassthrough}
	if len(raw) == 0 {
		return cfg, nil
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode plugin config: %w", err)
	}
	if cfg.OnNoMatch == "" {
		cfg.OnNoMatch = ActionPassthrough
	}
	for i, rule := range cfg.Rules {
		switch rule.Action {
		case ActionBridge, ActionPassthrough:
		default:
			return Config{}, fmt.Errorf("rule %d has invalid action %q (want bridge or passthrough)", i, rule.Action)
		}
		if strings.TrimSpace(rule.Match) == "" {
			return Config{}, fmt.Errorf("rule %d has empty match glob", i)
		}
	}
	return cfg, nil
}

// matchDecision reports whether the model matches a bridge rule and returns
// the effective summary model (bridge) or passthrough decision. The first
// matching rule wins; on_no_match is the terminal fallback.
type matchDecision struct {
	Handled      bool
	Bridged      bool
	SummaryModel string
}

// decideRoute evaluates the ordered rules against the model. Glob matching is
// case-sensitive (filepath.Match semantics). The first match wins.
func decideRoute(cfg Config, model string) matchDecision {
	decision := matchDecision{}
	for _, rule := range cfg.Rules {
		if globMatch(rule.Match, model) {
			decision.Handled = true
			if rule.Action == ActionBridge {
				decision.Bridged = true
				decision.SummaryModel = strings.TrimSpace(rule.SummaryModel)
			}
			return decision
		}
	}
	// on_no_match: first release accepts only passthrough, which means the host
	// keeps the request on its built-in route. The router returns Handled=false.
	return decision
}

// compactionTargetFor translates a routing decision into the target used by
// the Compaction State Policy. Handled without Bridged means an explicit
// `passthrough` Bridge Rule matched, which is the administrator's statement
// that the route is native-compatible; no rule matching at all is only
// CPA's default route and cannot make that claim.
func compactionTargetFor(decision matchDecision) compactTarget {
	switch {
	case decision.Handled && decision.Bridged:
		return targetBridge
	case decision.Handled:
		return targetExplicitPassthrough
	default:
		return targetUnmatchedPassthrough
	}
}

// globMatch wraps filepath.Match as an ordered, case-sensitive glob.
func globMatch(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	matched, err := filepath.Match(pattern, name)
	return err == nil && matched
}
