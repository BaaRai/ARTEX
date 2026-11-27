package chatgptplan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The SIWC catalog is account-specific and has a different response shape
// from the API-key model catalog.
// Source: https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference
const modelCatalogURL = "https://api.openai.com/v1/models"
const maxModelCatalogBytes = 4 << 20

// Model is a ChatGPT plan model that OpenAI marks as visible in the picker.
// Slug is the value to send as the Responses API model; DisplayName is for UI.
type Model struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

// ListModels returns the selected ChatGPT account's visible models in server
// order. The access token remains on the backend and is sent only to OpenAI's
// fixed HTTPS catalog endpoint.
func ListModels(ctx context.Context, source TokenSource) ([]Model, error) {
	return listModels(ctx, source, &http.Client{Timeout: 15 * time.Second})
}

// ListModels uses this Store's saved and refreshable SIWC access token.
func (s *Store) ListModels(ctx context.Context) ([]Model, error) {
	return ListModels(ctx, s)
}

// listModels accepts a client for deterministic tests while keeping the
// production endpoint fixed. Redirects are disabled even for injected clients.
func listModels(ctx context.Context, source TokenSource, client *http.Client) ([]Model, error) {
	if source == nil {
		return nil, errors.New("ChatGPT token source is required")
	}
	if client == nil {
		return nil, errors.New("ChatGPT model catalog HTTP client is required")
	}
	token, err := source.AccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("ChatGPT sign-in: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("ChatGPT sign-in returned an empty access token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelCatalogURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	noRedirectClient := *client
	noRedirectClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request ChatGPT model catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ChatGPT model catalog returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxModelCatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read ChatGPT model catalog: %w", err)
	}
	if len(raw) > maxModelCatalogBytes {
		return nil, errors.New("ChatGPT model catalog response is too large")
	}
	var envelope struct {
		Models json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode ChatGPT model catalog: %w", err)
	}
	if models := bytes.TrimSpace(envelope.Models); len(models) == 0 || models[0] != '[' {
		return nil, errors.New("ChatGPT model catalog has no models array")
	}
	var entries []struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		Visibility  string `json:"visibility"`
	}
	if err := json.Unmarshal(envelope.Models, &entries); err != nil {
		return nil, fmt.Errorf("decode ChatGPT model catalog models: %w", err)
	}
	models := make([]Model, 0, len(entries))
	for _, entry := range entries {
		if entry.Visibility != "list" || strings.TrimSpace(entry.Slug) == "" {
			continue
		}
		name := entry.DisplayName
		if strings.TrimSpace(name) == "" {
			name = entry.Slug
		}
		models = append(models, Model{Slug: entry.Slug, DisplayName: name})
	}
	return models, nil
}
