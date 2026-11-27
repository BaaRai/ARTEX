// Package ghidra is a thin client for the ghidra-headless-rest sidecar, which
// wraps Ghidra analyzeHeadless behind a local REST API. The sidecar analyzes
// uploaded binaries; it never executes them.
//
// The service is optional. Set ARTEX_GHIDRA_URL (for example
// http://ghidra:9090) to enable it; ARTEX_GHIDRA_API_KEY adds a bearer token
// for deployments that put the sidecar behind an authenticating proxy. Without
// a URL the agent tools report that the service is not configured.
package ghidra

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// maxResponseBytes bounds what a single call may pull into memory. Decompiled
// functions are the largest legitimate payload; anything above this is a
// misconfigured endpoint, not a result the model could use.
const maxResponseBytes = 8 << 20

// ErrNotConfigured is returned when ARTEX_GHIDRA_URL is empty.
var ErrNotConfigured = errors.New("ghidra sidecar is not configured: set ARTEX_GHIDRA_URL")

// Client talks to one ghidra-headless-rest instance.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// FromEnv builds a client from ARTEX_GHIDRA_URL and ARTEX_GHIDRA_API_KEY.
func FromEnv() *Client {
	return New(os.Getenv("ARTEX_GHIDRA_URL"), os.Getenv("ARTEX_GHIDRA_API_KEY"))
}

// New builds a client for baseURL. An empty baseURL yields a disabled client
// whose every call returns ErrNotConfigured.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		// No client-wide timeout: analysis is asynchronous, but a single
		// decompile of a large function can still run for minutes. Callers
		// pass a context with the deadline they can afford.
		http: &http.Client{},
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
		return nil, fmt.Errorf("build ghidra request: %w", err)
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
		return nil, fmt.Errorf("ghidra request failed: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read ghidra response: %w", err)
	}
	if len(payload) > maxResponseBytes {
		return nil, fmt.Errorf("ghidra response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("ghidra returned HTTP %d: %s", resp.StatusCode, trimMessage(payload))
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

// Health calls GET /v1/health.
func (c *Client) Health(ctx context.Context) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, "/v1/health", nil, nil, "")
}

// Version calls GET /v1/version.
func (c *Client) Version(ctx context.Context) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, "/v1/version", nil, nil, "")
}

// Capabilities calls GET /v1/capabilities so the agent can feature-detect.
func (c *Client) Capabilities(ctx context.Context) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, "/v1/capabilities", nil, nil, "")
}

// Upload streams a binary as multipart/form-data to POST /v1/jobs and returns
// the job descriptor. The caller owns the reader and the context deadline.
func (c *Client) Upload(ctx context.Context, filename string, r io.Reader) (json.RawMessage, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(filename) == "" {
		return nil, errors.New("filename is required")
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", filename)
		if err == nil {
			_, err = io.Copy(part, r)
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	defer pr.Close()
	raw, err := c.do(ctx, http.MethodPost, "/v1/jobs", nil, pr, mw.FormDataContentType())
	if err != nil {
		return nil, err
	}
	return normalizeUpload(raw)
}

// normalizeUpload rewrites the one response shape that omits job_id. The sidecar
// deduplicates by SHA-256, so re-uploading a known binary answers
// {"status":"done","cache_hit":true,"reused_job_id":"..."} without starting a
// job; without this the caller would have to know both key names.
func normalizeUpload(raw json.RawMessage) (json.RawMessage, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, nil
	}
	if _, ok := m["job_id"]; ok {
		return raw, nil
	}
	if reused, ok := m["reused_job_id"].(string); ok && reused != "" {
		m["job_id"] = reused
		return json.Marshal(m)
	}
	return raw, nil
}

// Job calls GET /v1/jobs/{id}.
func (c *Client) Job(ctx context.Context, id string) (json.RawMessage, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id), nil, nil, "")
}

// Jobs calls GET /v1/jobs.
func (c *Client) Jobs(ctx context.Context, limit int) (json.RawMessage, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return c.do(ctx, http.MethodGet, "/v1/jobs", q, nil, "")
}

// Cancel calls POST /v1/jobs/{id}/cancel.
func (c *Client) Cancel(ctx context.Context, id string) (json.RawMessage, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/cancel", nil, bytes.NewReader(nil), "application/json")
}

// Summary calls GET /v1/results/{id}/summary.
func (c *Client) Summary(ctx context.Context, id string) (json.RawMessage, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodGet, "/v1/results/"+url.PathEscape(id)+"/summary", nil, nil, "")
}

// Functions calls GET /v1/results/{id}/functions with an optional name search.
func (c *Client) Functions(ctx context.Context, id, search string, offset, limit int) (json.RawMessage, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	q := pageQuery(offset, limit)
	if strings.TrimSpace(search) != "" {
		q.Set("q", search)
	}
	return c.do(ctx, http.MethodGet, "/v1/results/"+url.PathEscape(id)+"/functions", q, nil, "")
}

// Decompile calls GET /v1/results/{id}/function/{addr}/decompile.
func (c *Client) Decompile(ctx context.Context, id, addr string) (json.RawMessage, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	if strings.TrimSpace(addr) == "" {
		return nil, errors.New("addr is required")
	}
	return c.do(ctx, http.MethodGet, "/v1/results/"+url.PathEscape(id)+"/function/"+url.PathEscape(addr)+"/decompile", nil, nil, "")
}

// Xrefs calls GET /v1/results/{id}/xrefs/{addr}.
func (c *Client) Xrefs(ctx context.Context, id, addr string) (json.RawMessage, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	if strings.TrimSpace(addr) == "" {
		return nil, errors.New("addr is required")
	}
	return c.do(ctx, http.MethodGet, "/v1/results/"+url.PathEscape(id)+"/xrefs/"+url.PathEscape(addr), nil, nil, "")
}

// Strings calls GET /v1/results/{id}/strings.
func (c *Client) Strings(ctx context.Context, id string, minLength, offset, limit int) (json.RawMessage, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	q := pageQuery(offset, limit)
	if minLength > 0 {
		q.Set("min_length", strconv.Itoa(minLength))
	}
	return c.do(ctx, http.MethodGet, "/v1/results/"+url.PathEscape(id)+"/strings", q, nil, "")
}

// Imports calls GET /v1/results/{id}/imports.
func (c *Client) Imports(ctx context.Context, id string, offset, limit int) (json.RawMessage, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodGet, "/v1/results/"+url.PathEscape(id)+"/imports", pageQuery(offset, limit), nil, "")
}

// SecurityFunctions calls GET /v1/results/{id}/security/functions, the
// deterministic attack-surface ranking. It is a review prior, not a verdict.
func (c *Client) SecurityFunctions(ctx context.Context, id, band, category, search string, minScore, offset, limit int) (json.RawMessage, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	q := pageQuery(offset, limit)
	if band != "" {
		q.Set("band", band)
	}
	if category != "" {
		q.Set("category", category)
	}
	if strings.TrimSpace(search) != "" {
		q.Set("q", search)
	}
	if minScore > 0 {
		q.Set("min_score", strconv.Itoa(minScore))
	}
	return c.do(ctx, http.MethodGet, "/v1/results/"+url.PathEscape(id)+"/security/functions", q, nil, "")
}

func pageQuery(offset, limit int) url.Values {
	q := url.Values{}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return q
}

// validID accepts the job identifiers the sidecar issues: short opaque tokens
// without path separators. Rejecting anything else keeps a model-supplied
// string from reshaping the request path.
func validID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("job id is required")
	}
	if len(id) > 128 {
		return errors.New("job id is too long")
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return errors.New("job id contains invalid characters")
		}
	}
	return nil
}

// UploadTimeout is the deadline a tool should give a single upload.
const UploadTimeout = 5 * time.Minute
