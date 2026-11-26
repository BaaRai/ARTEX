package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// The llm/test button must send the output cap under the key the profile is
// configured with. OpenAI reasoning models reject max_tokens with
// unsupported_parameter, so a profile set to max_completion_tokens used to fail
// the connection test with a 400 while conversations with it worked.
func TestLLMTestHonorsMaxTokensField(t *testing.T) {
	skipWithoutDSN(t)
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	if m.pg == nil {
		db.SkipNoPG(t)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())

	var mu sync.Mutex
	var lastBody map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		lastBody = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(fake.Close)

	pid, err := m.pg.SaveProfile(&db.LLMProfile{
		Name: "mct-field", Format: "openai", Model: "gpt-x",
		BaseURL: fake.URL, APIKey: "k1", MaxTokensField: "max_completion_tokens",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec("DELETE FROM llm_profiles WHERE id=$1", pid) })

	run := func(t *testing.T, form map[string]any) map[string]any {
		t.Helper()
		mu.Lock()
		lastBody = nil
		mu.Unlock()
		body, _ := json.Marshal(form)
		req := httptest.NewRequest(http.MethodPost, "/api/llm/test", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.testLLM(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("handler status %d: %s", rec.Code, rec.Body.String())
		}
		mu.Lock()
		defer mu.Unlock()
		if lastBody == nil {
			t.Fatalf("the test request never reached the endpoint: %s", rec.Body.String())
		}
		return lastBody
	}
	wantKey := func(t *testing.T, body map[string]any, want, absent string) {
		t.Helper()
		if _, ok := body[want]; !ok {
			t.Fatalf("request must carry %q, body keys: %v", want, bodyKeys(body))
		}
		if _, ok := body[absent]; ok {
			t.Fatalf("request must not carry %q, body keys: %v", absent, bodyKeys(body))
		}
	}

	t.Run("form value", func(t *testing.T) {
		body := run(t, map[string]any{
			"provider": "openai", "model": "gpt-x", "base_url": fake.URL,
			"api_key": "k1", "streaming": false, "max_tokens_field": "max_completion_tokens",
		})
		wantKey(t, body, "max_completion_tokens", "max_tokens")
	})

	t.Run("stored profile fallback", func(t *testing.T) {
		// List-view test button: only the profile id, the field comes from the row.
		body := run(t, map[string]any{"profile_id": pid, "api_key": "", "streaming": false})
		wantKey(t, body, "max_completion_tokens", "max_tokens")
	})

	t.Run("form override to default", func(t *testing.T) {
		// An explicit empty value in the form wins over the stored choice.
		body := run(t, map[string]any{
			"provider": "openai", "model": "gpt-x", "base_url": fake.URL,
			"api_key": "", "profile_id": pid, "streaming": false, "max_tokens_field": "",
		})
		wantKey(t, body, "max_tokens", "max_completion_tokens")
	})

	t.Run("unknown value falls back to default", func(t *testing.T) {
		body := run(t, map[string]any{
			"provider": "openai", "model": "gpt-x", "base_url": fake.URL,
			"api_key": "k1", "streaming": false, "max_tokens_field": "bogus",
		})
		wantKey(t, body, "max_tokens", "max_completion_tokens")
	})
}

func bodyKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
