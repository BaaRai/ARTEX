package agent

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/llmrec"
)

type recordCaptureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	return nil
}

func (h *recordCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordCaptureHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordCaptureHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

func TestSlogPreservesTagsAndLevels(t *testing.T) {
	handler := &recordCaptureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	noaWarn("sess-1")("fallback triggered")
	_, capt := llmrec.NewCapture(context.Background())
	logTestConnection(Config{Format: FormatBedrockGateway}, capt)

	var sawNoa, sawLLMTest bool
	for _, r := range handler.snapshot() {
		if r.Level == slog.LevelWarn && strings.HasPrefix(r.Message, "[noa]") {
			r.Attrs(func(a slog.Attr) bool {
				if a.Key == "session" && a.Value.String() == "sess-1" {
					sawNoa = true
					return false
				}
				return true
			})
		}
		if r.Level == slog.LevelWarn && strings.HasPrefix(r.Message, "[llm-test]") {
			sawLLMTest = true
		}
	}
	if !sawNoa {
		t.Fatalf("expected WARN [noa] record with session=sess-1; got %d records", len(handler.snapshot()))
	}
	if !sawLLMTest {
		t.Fatalf("expected WARN [llm-test] record; got %d records", len(handler.snapshot()))
	}
}
