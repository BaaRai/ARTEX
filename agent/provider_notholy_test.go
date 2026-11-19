package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// ConfigFrom("notholy", ...) bakes the NotHoly gateway base URL so a profile
// needs only a key, requires an API key (unlike keyless Ollama), and does NOT
// invent a model — the model comes from the NotHoly catalog (/v1/models).
func TestNotHolyConfigFromDefaults(t *testing.T) {
	c := ConfigFrom("notholy", "", "", "", "")
	if c.Format != FormatNotHoly {
		t.Fatalf("format=%v, want FormatNotHoly", c.Format)
	}
	if c.Provider() != "notholy" {
		t.Fatalf("Provider()=%q", c.Provider())
	}
	if c.BaseURL != DefaultNotHolyBaseURL {
		t.Fatalf("BaseURL=%q, want default %q", c.BaseURL, DefaultNotHolyBaseURL)
	}
	if !c.NeedsAPIKey() {
		t.Fatal("notholy must require an API key")
	}
	if c.Model != "" {
		t.Fatalf("model must stay empty (catalog-driven), got %q", c.Model)
	}
	// A full endpoint URL is tolerated (the provider appends /chat/completions).
	e := ConfigFrom("notholy", "m", "https://api.notholy.com/v1/chat/completions", "k", "")
	if e.BaseURL != "https://api.notholy.com/v1" {
		t.Fatalf("full endpoint URL not normalized: %q", e.BaseURL)
	}
	// A custom key is preserved; a custom base (self-hosted NotHoly) is preserved.
	k := ConfigFrom("notholy", "m", "https://nh.internal/v1", "nh-secret", "")
	if k.APIKey != "nh-secret" || k.BaseURL != "https://nh.internal/v1" {
		t.Fatalf("custom key/base lost: key=%q base=%q", k.APIKey, k.BaseURL)
	}
}

// The provider built from a NotHoly config speaks OpenAI Chat Completions on the
// wire with the real Bearer key, and never falls back to OpenAI's public
// endpoint — it targets the configured (baked) NotHoly base URL.
func TestNotHolyProviderWiresOpenAIProtocol(t *testing.T) {
	var gotPath, gotAuth, gotModel atomic.Value
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		gotAuth.Store(r.Header.Get("Authorization"))
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel.Store(body.Model)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","model":"notholy-specter","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(fake.Close)

	cfg := ConfigFrom("notholy", "notholy-specter", fake.URL, "nh-secret", "")
	cfg.Stream = false // non-streaming: the fake answers one plain JSON body
	prov, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := llm.CompletionRequest{
		System:   []string{"You are a connection test. Output OK."},
		Messages: []llm.Message{llm.UserText("ping")},
	}
	if _, _, _, err = prov.Complete(ctx, req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if p, _ := gotPath.Load().(string); p != "/chat/completions" {
		t.Fatalf("path=%q, want /chat/completions", p)
	}
	if a, _ := gotAuth.Load().(string); a != "Bearer nh-secret" {
		t.Fatalf("Authorization=%q, want the configured NotHoly key", a)
	}
	if m, _ := gotModel.Load().(string); m != "notholy-specter" {
		t.Fatalf("model on wire=%q", m)
	}
}

// FromEnv resolves NotHoly: explicitly, and inferred when only NOTHOLY_API_KEY
// is set. Without a key it is unusable (ok=false).
func TestNotHolyFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ARTEX_LLM_BASE_URL", "")
	t.Setenv("ARTEX_LLM_MODEL", "")

	// Explicit provider + key.
	t.Setenv("ARTEX_LLM_PROVIDER", "notholy")
	t.Setenv("NOTHOLY_API_KEY", "nh-secret")
	c, ok := FromEnv()
	if !ok {
		t.Fatal("notholy env config must be usable with a key")
	}
	if c.Format != FormatNotHoly || c.APIKey != "nh-secret" || c.BaseURL != DefaultNotHolyBaseURL {
		t.Fatalf("format=%v key=%q base=%q", c.Format, c.APIKey, c.BaseURL)
	}

	// Inferred from the key alone (no provider set).
	t.Setenv("ARTEX_LLM_PROVIDER", "")
	c, ok = FromEnv()
	if !ok || c.Format != FormatNotHoly {
		t.Fatalf("notholy must be inferred from NOTHOLY_API_KEY: ok=%v format=%v", ok, c.Format)
	}

	// No key → unusable.
	t.Setenv("ARTEX_LLM_PROVIDER", "notholy")
	t.Setenv("NOTHOLY_API_KEY", "")
	if _, ok := FromEnv(); ok {
		t.Fatal("notholy without a key must be unusable")
	}
}
