package opentrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_Configured(t *testing.T) {
	if New("", "").Configured() {
		t.Fatal("empty base URL must be unconfigured")
	}
	if !New("http://opentrap:8081/", "").Configured() {
		t.Fatal("base URL must enable the client")
	}
	if got := New("http://opentrap:8081/", "").BaseURL(); got != "http://opentrap:8081" {
		t.Fatalf("trailing slash kept: %q", got)
	}
}

func TestClient_CallWithoutURLReturnsErrNotConfigured(t *testing.T) {
	c := New("", "")
	if _, err := c.Status(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
	if _, err := c.History(context.Background(), 0, 0); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("history: want ErrNotConfigured, got %v", err)
	}
	if _, err := c.Replay(context.Background(), "http://example.com", nil, false, false); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("replay: want ErrNotConfigured, got %v", err)
	}
}

func TestClient_SendsBearerTokenAndDecodesJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("accept = %q", got)
		}
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "secret").Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"status":"ok"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestClient_HistoryWithQueryParams(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RequestURI())
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	ctx := context.Background()

	_, _ = c.History(ctx, 50, 10)
	_, _ = c.HistoryItem(ctx, "hist-123")
	_, _ = c.ScopeGet(ctx)

	want := []string{
		"/v1/history?limit=50&offset=10",
		"/v1/history/hist-123",
		"/v1/scope",
	}
	if len(got) != len(want) {
		t.Fatalf("requests = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestClient_ScopePutSendsJSON(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v1/scope" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "").ScopePut(context.Background(), json.RawMessage(`{"include":["example.com"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, "example.com") {
		t.Fatalf("body not sent: %s", gotBody)
	}
}

func TestClient_ReplaySendsRequiredHeaders(t *testing.T) {
	var gotHeaders http.Header
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/replay" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		gotHeaders = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"status":200}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "secret").Replay(context.Background(), "http://target.example.com/path", []byte(`{"key":"value"}`), true, true)
	if err != nil {
		t.Fatal(err)
	}
	if gotHeaders.Get("X-OpenTrap-URL") != "http://target.example.com/path" {
		t.Errorf("X-OpenTrap-URL = %q", gotHeaders.Get("X-OpenTrap-URL"))
	}
	if gotHeaders.Get("X-OpenTrap-Update-Content-Length") != "true" {
		t.Errorf("X-OpenTrap-Update-Content-Length = %q", gotHeaders.Get("X-OpenTrap-Update-Content-Length"))
	}
	if gotHeaders.Get("X-OpenTrap-Follow-Redirects") != "true" {
		t.Errorf("X-OpenTrap-Follow-Redirects = %q", gotHeaders.Get("X-OpenTrap-Follow-Redirects"))
	}
	if gotHeaders.Get("Authorization") != "Bearer secret" {
		t.Errorf("Authorization = %q", gotHeaders.Get("Authorization"))
	}
	if gotBody != `{"key":"value"}` {
		t.Errorf("body = %q", gotBody)
	}
}

func TestClient_ReplayRejectsEmptyURL(t *testing.T) {
	if _, err := New("http://opentrap:8081", "").Replay(context.Background(), "  ", nil, false, false); err == nil {
		t.Fatal("empty url accepted")
	}
}

func TestClient_HTTPErrorCarriesStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"detail":"not found"}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "").HistoryItem(context.Background(), "missing")
	if err == nil {
		t.Fatal("404 treated as success")
	}
	if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestClient_ResponseSizeIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "").Status(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized body accepted: %v", err)
	}
}

func TestClient_HistoryItemRejectsEmptyID(t *testing.T) {
	if _, err := New("http://opentrap:8081", "").HistoryItem(context.Background(), " "); err == nil {
		t.Fatal("empty id accepted")
	}
}

func TestClient_ContextCancellationStopsRequest(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer srv.Close()
	defer close(block)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(srv.URL, "").Status(ctx); err == nil {
		t.Fatal("cancelled context produced a result")
	}
}