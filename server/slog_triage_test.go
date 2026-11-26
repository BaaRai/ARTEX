package server

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

type triageSlogCapture struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (h *triageSlogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *triageSlogCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.recs = append(h.recs, r.Clone())
	h.mu.Unlock()
	return nil
}
func (h *triageSlogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *triageSlogCapture) WithGroup(string) slog.Handler      { return h }

func TestTriageRefusedBaseURLLogsSlogWarn(t *testing.T) {
	cap := &triageSlogCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := &Server{}
	if c := s.triageHTTPClient("http://127.0.0.1:9999"); c != nil {
		t.Fatalf("expected nil client for loopback base_url, got %v", c)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	var sawWarn bool
	for _, r := range cap.recs {
		if r.Level == slog.LevelWarn && strings.Contains(r.Message, "[triage]") {
			sawWarn = true
		}
	}
	if !sawWarn {
		t.Fatalf("expected slog.LevelWarn [triage] record; got %d records", len(cap.recs))
	}
}
