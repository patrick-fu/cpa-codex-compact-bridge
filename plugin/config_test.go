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
	if cfg.MaxSummaryTokens != defaultMaxSummaryTokens || cfg.MaxSummaryBytes != defaultMaxSummaryBytes || !cfg.AppendToolGuard || cfg.ForwardServiceTier || len(cfg.SummaryImageModels) != 0 {
		t.Fatalf("summary defaults = %+v", cfg)
	}
}

func TestLoadConfigRules(t *testing.T) {
	raw := []byte(`
compact_prompt: "Preserve exact task state."
rules:
  - match: "glm-*"
    action: bridge
    summary_model: "glm-5.2"
  - match: "gpt-*"
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
	if cfg.CompactPrompt != "Preserve exact task state." || !cfg.compactPromptSet || !cfg.AppendToolGuard {
		t.Fatalf("partial config lost defaults: %+v", cfg)
	}
}

func TestLoadConfigTracksExplicitBlankCompactPrompt(t *testing.T) {
	cfg, err := loadConfig([]byte("compact_prompt: ''\n"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.compactPromptSet || cfg.CompactPrompt != "" {
		t.Fatalf("explicit blank prompt = %+v", cfg)
	}
}

func TestLoadConfigSummarySettings(t *testing.T) {
	cfg, err := loadConfig([]byte(`
max_summary_tokens: 4096
max_summary_bytes: 65536
append_tool_guard: false
forward_service_tier: true
summary_image_models: ["vision-*"]
`))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MaxSummaryTokens != 4096 || cfg.MaxSummaryBytes != 65536 || cfg.AppendToolGuard || !cfg.ForwardServiceTier || len(cfg.SummaryImageModels) != 1 || cfg.SummaryImageModels[0] != "vision-*" {
		t.Fatalf("summary settings = %+v", cfg)
	}
}

func TestLoadConfigRejectsInvalidSummarySettings(t *testing.T) {
	cases := []string{
		"max_summary_tokens: 100001\n",
		"max_summary_bytes: 0\n",
		"max_summary_bytes: -1\n",
		"summary_image_models: [\"\"]\n",
		"summary_image_models: [\"[\"]\n",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if _, err := loadConfig([]byte(raw)); err == nil {
				t.Fatalf("loadConfig(%q) succeeded", raw)
			}
		})
	}
	for _, raw := range []string{"max_summary_tokens: 0\n", "max_summary_tokens: -1\n"} {
		t.Run(raw, func(t *testing.T) {
			cfg, err := loadConfig([]byte(raw))
			if err != nil || cfg.MaxSummaryTokens != defaultMaxSummaryTokens {
				t.Fatalf("loadConfig(%q) = %+v, %v", raw, cfg, err)
			}
		})
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
			{Match: "gpt-*", Action: ActionPassthrough},
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
		{"gpt-*", "gpt-5.5-codex", true},
		{"gpt-*", "gpt-5.5", true},
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
