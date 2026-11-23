package main

import (
	"net/http"
	"strings"
)

const ClaudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude."

const oauthBeta = "oauth-2025-04-20"

// rewriteAuth replaces any existing API-key/Authorization headers with a
// Bearer token derived from the OAuth access token.
func rewriteAuth(h http.Header, token string) {
	h.Del("X-Api-Key")
	h.Del("Authorization")
	h.Set("Authorization", "Bearer "+token)
}

// mergeBeta ensures oauthBeta is present in the Anthropic-Beta header,
// preserving any existing values and never duplicating entries.
func mergeBeta(h http.Header) {
	existing := h.Get("Anthropic-Beta")

	var parts []string
	for _, p := range strings.Split(existing, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}

	found := false
	for _, p := range parts {
		if p == oauthBeta {
			found = true
			break
		}
	}
	if !found {
		parts = append(parts, oauthBeta)
	}

	h.Set("Anthropic-Beta", strings.Join(parts, ","))
}

// injectIdentity ensures body["system"] begins with ClaudeCodeIdentity.
// It returns true iff it changed body.
func injectIdentity(body map[string]any) bool {
	v, ok := body["system"]
	if !ok {
		body["system"] = ClaudeCodeIdentity
		return true
	}

	switch s := v.(type) {
	case string:
		if strings.HasPrefix(s, ClaudeCodeIdentity) {
			return false
		}
		body["system"] = ClaudeCodeIdentity + "\n\n" + s
		return true
	case []any:
		if len(s) > 0 {
			if first, ok := s[0].(map[string]any); ok {
				if first["type"] == "text" {
					if text, ok := first["text"].(string); ok && strings.HasPrefix(text, ClaudeCodeIdentity) {
						return false
					}
				}
			}
		}
		identityBlock := map[string]any{"type": "text", "text": ClaudeCodeIdentity}
		newArr := make([]any, 0, len(s)+1)
		newArr = append(newArr, identityBlock)
		newArr = append(newArr, s...)
		body["system"] = newArr
		return true
	default:
		return false
	}
}
