package main

import (
	"net/http"
	"testing"
)

func TestRewriteAuth(t *testing.T) {
	h := http.Header{}
	h.Set("X-Api-Key", "sk-old")
	h.Set("Authorization", "Bearer old")

	rewriteAuth(h, "tok")

	if got := h.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
	}
	if got := h.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key = %q, want absent", got)
	}
}

func TestMergeBeta_Empty(t *testing.T) {
	h := http.Header{}

	mergeBeta(h)

	if got := h.Get("Anthropic-Beta"); got != oauthBeta {
		t.Errorf("Anthropic-Beta = %q, want %q", got, oauthBeta)
	}
}

func TestMergeBeta_PreservesExisting(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Beta", "prompt-caching-2024-07-31")

	mergeBeta(h)

	got := h.Get("Anthropic-Beta")
	if got != "prompt-caching-2024-07-31,"+oauthBeta {
		t.Errorf("Anthropic-Beta = %q, want both values comma-separated", got)
	}
}

func TestMergeBeta_Idempotent(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Beta", oauthBeta)

	mergeBeta(h)

	if got := h.Get("Anthropic-Beta"); got != oauthBeta {
		t.Errorf("Anthropic-Beta = %q, want unchanged single value %q", got, oauthBeta)
	}
}

func TestInjectIdentity_Missing(t *testing.T) {
	body := map[string]any{}

	changed := injectIdentity(body)

	if !changed {
		t.Errorf("changed = false, want true")
	}
	if got := body["system"]; got != ClaudeCodeIdentity {
		t.Errorf("system = %v, want %q", got, ClaudeCodeIdentity)
	}
}

func TestInjectIdentity_String(t *testing.T) {
	body := map[string]any{"system": "X"}

	changed := injectIdentity(body)

	if !changed {
		t.Errorf("changed = false, want true")
	}
	want := ClaudeCodeIdentity + "\n\nX"
	if got := body["system"]; got != want {
		t.Errorf("system = %v, want %q", got, want)
	}
}

func TestInjectIdentity_StringIdempotent(t *testing.T) {
	body := map[string]any{"system": ClaudeCodeIdentity + "\n\nX"}

	changed := injectIdentity(body)

	if changed {
		t.Errorf("changed = true, want false")
	}
	want := ClaudeCodeIdentity + "\n\nX"
	if got := body["system"]; got != want {
		t.Errorf("system = %v, want unchanged %q", got, want)
	}
}

func TestInjectIdentity_Array(t *testing.T) {
	body := map[string]any{
		"system": []any{
			map[string]any{"type": "text", "text": "X"},
		},
	}

	changed := injectIdentity(body)

	if !changed {
		t.Errorf("changed = false, want true")
	}
	arr, ok := body["system"].([]any)
	if !ok {
		t.Fatalf("system is not []any: %T", body["system"])
	}
	if len(arr) != 2 {
		t.Fatalf("len(arr) = %d, want 2", len(arr))
	}
	first, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("arr[0] is not map[string]any: %T", arr[0])
	}
	if first["type"] != "text" || first["text"] != ClaudeCodeIdentity {
		t.Errorf("arr[0] = %v, want identity text block", first)
	}
}

func TestInjectIdentity_ArrayIdempotent(t *testing.T) {
	body := map[string]any{
		"system": []any{
			map[string]any{"type": "text", "text": ClaudeCodeIdentity},
		},
	}

	changed := injectIdentity(body)

	if changed {
		t.Errorf("changed = true, want false")
	}
	arr, ok := body["system"].([]any)
	if !ok {
		t.Fatalf("system is not []any: %T", body["system"])
	}
	if len(arr) != 1 {
		t.Errorf("len(arr) = %d, want 1", len(arr))
	}
}

func TestInjectIdentity_OtherType(t *testing.T) {
	body := map[string]any{"system": 42}

	changed := injectIdentity(body)

	if changed {
		t.Errorf("changed = true, want false")
	}
	if got := body["system"]; got != 42 {
		t.Errorf("system = %v, want unchanged 42", got)
	}
}
