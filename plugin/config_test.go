package main

import (
	"testing"
)

func TestLoadConfigDefault(t *testing.T) {
	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OnNoMatch != ActionPassthrough {
		t.Fatalf("OnNoMatch = %q, want passthrough", cfg.OnNoMatch)
	}
}

func TestLoadConfigRules(t *testing.T) {
	raw := []byte(`
rules:
  - match: "glm-*"
    action: bridge
    summary_model: "glm-5.2"
  - match: "gpt-*-codex*"
    action: passthrough
on_no_match: passthrough
`)
	cfg, err := loadConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Rules) != 2 {
		t.Fatalf("len(Rules) = %d, want 2", len(cfg.Rules))
	}
	if cfg.Rules[0].Match != "glm-*" || cfg.Rules[0].Action != ActionBridge {
		t.Fatalf("rule0 = %+v", cfg.Rules[0])
	}
	if cfg.Rules[0].SummaryModel != "glm-5.2" {
		t.Fatalf("rule0 summary = %q", cfg.Rules[0].SummaryModel)
	}
}

func TestLoadConfigInvalidAction(t *testing.T) {
	raw := []byte(`
rules:
  - match: "glm-*"
    action: bogus
`)
	_, err := loadConfig(raw)
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestLoadConfigEmptyMatch(t *testing.T) {
	raw := []byte(`
rules:
  - match: ""
    action: bridge
`)
	_, err := loadConfig(raw)
	if err == nil {
		t.Fatal("expected error for empty match")
	}
}

func TestDecideRouteFirstMatchWins(t *testing.T) {
	cfg := Config{
		Rules: []Rule{
			{Match: "glm-*", Action: ActionBridge, SummaryModel: "glm-5.2"},
			{Match: "gpt-*-codex*", Action: ActionPassthrough},
		},
		OnNoMatch: ActionPassthrough,
	}
	// bridge rule wins
	d := decideRoute(cfg, "glm-4.7")
	if !d.Handled || !d.Bridged || d.SummaryModel != "glm-5.2" {
		t.Fatalf("glm-4.7 decision = %+v", d)
	}
	// passthrough rule
	d = decideRoute(cfg, "gpt-5.5-codex")
	if !d.Handled || d.Bridged {
		t.Fatalf("gpt-5.5-codex decision = %+v", d)
	}
	// first match wins: glm-* also matches glm-codex before the passthrough rule
	d = decideRoute(cfg, "glm-codex")
	if !d.Handled || !d.Bridged {
		t.Fatalf("glm-codex decision = %+v", d)
	}
}

func TestDecideRouteNoMatchPassthrough(t *testing.T) {
	cfg := Config{
		Rules:     []Rule{{Match: "glm-*", Action: ActionBridge}},
		OnNoMatch: ActionPassthrough,
	}
	d := decideRoute(cfg, "claude-opus")
	if d.Handled {
		t.Fatalf("claude-opus should not be handled, got %+v", d)
	}
}

func TestDecideRouteCaseSensitive(t *testing.T) {
	cfg := Config{
		Rules: []Rule{{Match: "GLM-*", Action: ActionBridge}},
	}
	// case-sensitive glob: GLM-* does not match glm-4.7
	d := decideRoute(cfg, "glm-4.7")
	if d.Handled {
		t.Fatalf("case-sensitive glob should not match, got %+v", d)
	}
	d = decideRoute(cfg, "GLM-4.7")
	if !d.Handled || !d.Bridged {
		t.Fatalf("GLM-4.7 decision = %+v", d)
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"glm-*", "glm-4.7", true},
		{"glm-*", "glm-5.2", true},
		{"gpt-*-codex*", "gpt-5.5-codex", true},
		{"gpt-*-codex*", "gpt-5.5", false},
		{"*", "anything", true},
		{"exact", "exact", true},
		{"exact", "other", false},
		{"", "x", false},
	}
	for _, tc := range cases {
		got := globMatch(tc.pattern, tc.name)
		if got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}
