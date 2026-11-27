package agent

import (
	"strings"
	"testing"

	"github.com/Autumn-27/artex/chatgptplan"
)

func TestSubscriptionProfileFormatsAreKeyless(t *testing.T) {
	for _, tc := range []struct {
		format, model string
	}{
		{format: "chatgpt-plan", model: "gpt-6.1-sol"},
		{format: "claude-code", model: "sonnet"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			cfg := ConfigFrom(tc.format, "", "", "", "")
			if cfg.Provider() != tc.format || cfg.Model != tc.model || cfg.NeedsAPIKey() {
				t.Fatalf("subscription config = %+v", cfg)
			}
			cfg.APIKey = "unexpected-secret"
			if _, err := cfg.NewProvider(); err == nil || !strings.Contains(err.Error(), "do not accept an API key") {
				t.Fatalf("subscription profile accepted an API key: %v", err)
			}
			cfg.APIKey = ""
			cfg.BaseURL = "https://custom.example"
			if _, err := cfg.NewProvider(); err == nil || !strings.Contains(err.Error(), "base URL") {
				t.Fatalf("subscription profile accepted a custom endpoint: %v", err)
			}
		})
	}
}

func TestChatGPTPlanFactoryUsesSharedStore(t *testing.T) {
	cfg := ConfigFrom("chatgpt-plan", "gpt-6.1-sol", "", "", "")
	if _, err := cfg.NewProvider(); err == nil {
		t.Fatal("ChatGPT plan profile accepted a missing credential store")
	}
	cfg.ChatGPTStore = chatgptplan.NewStore(t.TempDir() + "/chatgpt-plan.json")
	if _, err := cfg.NewProvider(); err != nil {
		t.Fatalf("ChatGPT plan profile could not build with a shared credential store: %v", err)
	}
}

func TestSubscriptionFromEnvDoesNotInheritAPIKeys(t *testing.T) {
	t.Setenv("ARTEX_LLM_BASE_URL", "")
	t.Setenv("ARTEX_LLM_MODEL", "")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	t.Setenv("OPENAI_API_KEY", "openai-test-key")
	for _, format := range []string{"chatgpt-plan", "claude-code"} {
		t.Setenv("ARTEX_LLM_PROVIDER", format)
		cfg, ok := FromEnv()
		if !ok || cfg.Provider() != format || cfg.APIKey != "" || cfg.NeedsAPIKey() {
			t.Fatalf("%s inherited API credentials: ok=%v cfg=%+v", format, ok, cfg)
		}
	}
}
