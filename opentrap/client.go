// Package opentrap is a thin client for the OpenTRAP sidecar, which wraps the
// OpenTRAP desktop app (a Java AppImage built on OWASP ZAP) behind a local
// REST API. The sidecar provides an HTTP interface for proxy, history, scope
// and replay operations that the desktop UI exposes.
//
// The service is optional. Set ARTEX_OPENTRAP_URL (for example
// http://opentrap:8081) to enable it; ARTEX_OPENTRAP_API_KEY adds a bearer
// token for deployments that put the sidecar behind an authenticating proxy.
// Without a URL the agent tools report that the service is not configured.
package opentrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// maxResponseBytes bounds what a single call may pull into memory.
const maxResponseBytes = 8 << 20

// ErrNotConfigured is returned when ARTEX_OPENTRAP_URL is empty.
var ErrNotConfigured = errors.New("opentrap sidecar is not configured: set ARTEX_OPENTRAP_URL")

// Client talks to one OpenTRAP sidecar instance.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// FromEnv builds a client from ARTEX_OPENTRAP_URL and ARTEX_OPENTRAP_API_KEY.
func FromEnv() *Client {
	return New(os.Getenv("ARTEX_OPENTRAP_URL"), os.Getenv("ARTEX_OPENTRAP_API_KEY"))
}

// New builds a client for baseURL. An empty baseURL yields a disabled client
// whose every call returns ErrNotConfigured.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Configured reports whether a base URL is set.
func (c *Client) Configured() bool { return c != nil && c.baseURL != "" }

// BaseURL is the configured endpoint, for diagnostics.
func (c *Client) BaseURL() string {
	if c == nil {
		return ""
	}
	return c.baseURL
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (json.RawMessage, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("build opentrap request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opentrap request failed: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read opentrap response: %w", err)
	}
	if len(payload) > maxResponseBytes {
		return nil, fmt.Errorf("opentrap response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("opentrap returned HTTP %d: %s", resp.StatusCode, trimMessage(payload))
	}
	return json.RawMessage(payload), nil
}

func trimMessage(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}

// Status calls GET /v1/status.
func (c *Client) Status(ctx context.Context) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, "/v1/status", nil, nil, "")
}

// History calls GET /v1/history with optional limit and offset.
func (c *Client) History(ctx context.Context, limit, offset int) (json.RawMessage, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	return c.do(ctx, http.MethodGet, "/v1/history", q, nil, "")
}

// HistoryItem calls GET /v1/history/{id}.
func (c *Client) HistoryItem(ctx context.Context, id string) (json.RawMessage, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("history id is required")
	}
	return c.do(ctx, http.MethodGet, "/v1/history/"+url.PathEscape(id), nil, nil, "")
}

// ScopeGet calls GET /v1/scope.
func (c *Client) ScopeGet(ctx context.Context) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, "/v1/scope", nil, nil, "")
}

// ScopePut calls PUT /v1/scope with the given rules.
func (c *Client) ScopePut(ctx context.Context, rules json.RawMessage) (json.RawMessage, error) {
	return c.do(ctx, http.MethodPut, "/v1/scope", nil, bytes.NewReader(rules), "application/json")
}

// Replay calls POST /v1/replay with the given request and headers.
// url: target URL to replay
// updateContentLength: whether to add X-OpenTrap-Update-Content-Length
// followRedirects: whether to add X-OpenTrap-Follow-Redirects
func (c *Client) Replay(ctx context.Context, url string, body []byte, updateContentLength, followRedirects bool) (json.RawMessage, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("url is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/replay", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build replay request: %w", err)
	}
	req.Header.Set("X-OpenTrap-URL", url)
	if updateContentLength {
		req.Header.Set("X-OpenTrap-Update-Content-Length", "true")
	}
	if followRedirects {
		req.Header.Set("X-OpenTrap-Follow-Redirects", "true")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opentrap replay failed: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read replay response: %w", err)
	}
	if len(payload) > maxResponseBytes {
		return nil, fmt.Errorf("replay response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("opentrap replay returned HTTP %d: %s", resp.StatusCode, trimMessage(payload))
	}
	return json.RawMessage(payload), nil
}