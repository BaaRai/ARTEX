package ghidra

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
	if !New("http://ghidra:9090/", "").Configured() {
		t.Fatal("base URL must enable the client")
	}
	if got := New("http://ghidra:9090/", "").BaseURL(); got != "http://ghidra:9090" {
		t.Fatalf("trailing slash kept: %q", got)
	}
}

func TestClient_CallWithoutURLReturnsErrNotConfigured(t *testing.T) {
	c := New("", "")
	if _, err := c.Health(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
	if _, err := c.Upload(context.Background(), "a.bin", strings.NewReader("x")); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("upload: want ErrNotConfigured, got %v", err)
	}
}

func TestClient_SendsBearerTokenAndDecodesJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
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

	got, err := New(srv.URL, "secret").Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"status":"ok"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestClient_UploadPostsMultipartFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/jobs" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("content-type = %q", ct)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("form file: %v", err)
			return
		}
		defer file.Close()
		if header.Filename != "sample.bin" {
			t.Errorf("filename = %q", header.Filename)
		}
		body, _ := io.ReadAll(file)
		if string(body) != "\x7fELF payload" {
			t.Errorf("body = %q", body)
		}
		_, _ = io.WriteString(w, `{"job_id":"j1","status":"queued"}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "").Upload(context.Background(), "sample.bin", strings.NewReader("\x7fELF payload"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"job_id":"j1"`) {
		t.Fatalf("body = %s", got)
	}
}

func TestClient_UploadNormalizesDeduplicatedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"done","cache_hit":true,"reused_job_id":"old-7"}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "").Upload(context.Background(), "same.bin", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["job_id"] != "old-7" {
		t.Fatalf("job_id not normalized: %s", got)
	}
	if m["cache_hit"] != true {
		t.Fatalf("cache_hit dropped: %s", got)
	}
}

func TestClient_UploadRejectsEmptyFilename(t *testing.T) {
	if _, err := New("http://ghidra:9090", "").Upload(context.Background(), "  ", strings.NewReader("x")); err == nil {
		t.Fatal("empty filename accepted")
	}
}

func TestClient_PathsAndQuery(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RequestURI())
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	ctx := context.Background()

	_, _ = c.Job(ctx, "job-1")
	_, _ = c.Functions(ctx, "job-1", "main", 20, 50)
	_, _ = c.Decompile(ctx, "job-1", "0x401000")
	_, _ = c.Strings(ctx, "job-1", 6, 0, 10)
	_, _ = c.SecurityFunctions(ctx, "job-1", "high", "memory", "copy", 70, 0, 25)

	want := []string{
		"/v1/jobs/job-1",
		"/v1/results/job-1/functions?limit=50&offset=20&q=main",
		"/v1/results/job-1/function/0x401000/decompile",
		"/v1/results/job-1/strings?limit=10&min_length=6",
		"/v1/results/job-1/security/functions?band=high&category=memory&limit=25&min_score=70&q=copy",
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

func TestClient_HTTPErrorCarriesStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"detail":"job not found"}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "").Summary(context.Background(), "missing")
	if err == nil {
		t.Fatal("404 treated as success")
	}
	if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "job not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestClient_ResponseSizeIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "").Health(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized body accepted: %v", err)
	}
}

func TestClient_ValidIDRejectsPathShaping(t *testing.T) {
	c := New("http://ghidra:9090", "")
	for _, id := range []string{"", "  ", "../etc/passwd", "a/b", "job?x=1", strings.Repeat("a", 129)} {
		if _, err := c.Job(context.Background(), id); err == nil {
			t.Errorf("job id %q accepted", id)
		}
	}
}

func TestClient_DecompileRequiresAddr(t *testing.T) {
	if _, err := New("http://ghidra:9090", "").Decompile(context.Background(), "job-1", " "); err == nil {
		t.Fatal("empty addr accepted")
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
	if _, err := New(srv.URL, "").Health(ctx); err == nil {
		t.Fatal("cancelled context produced a result")
	}
}
