package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

func sampleCompactionInputs() (*coldGraph, block, map[int64]*db.Node) {
	n1 := &db.Node{ID: 10, Kind: db.KindIntent, State: "done", Payload: json.RawMessage(`{"summary":"Scan /api/login"}`)}
	n2 := &db.Node{ID: 11, Kind: db.KindFact, State: "confirmed", Payload: json.RawMessage(`{"summary":"Login returns 401"}`)}
	g := newColdGraph(
		[]cgNode{
			{ID: 10, Kind: db.KindIntent, State: "done"},
			{ID: 11, Kind: db.KindFact, State: "confirmed"},
		},
		[]cgEdge{
			{From: 10, Rel: db.RelYields, To: 11},
		},
	)
	b := block{Members: []int64{10, 11}}
	return g, b, map[int64]*db.Node{10: n1, 11: n2}
}

// Regression for #550: compaction must not hardcode thinking={"type":"disabled"},
// which Claude Sonnet 5.5 rejects with HTTP 400 invalid_request_error.
func TestCompactorCompressDoesNotSendThinkingDisabled(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mu.Lock()
		bodies = append(bodies, m)
		mu.Unlock()
		if th, ok := m["thinking"].(map[string]any); ok && th["type"] == "disabled" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"To turn thinking off on this model, send \"thinking\": {\"type\": \"between_tools\"} instead of {\"type\": \"disabled\"}. The model does not think before responding."}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"Consolidated summary (#10,#11)."}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":6}}`))
	}))
	defer srv.Close()

	prov, err := llm.NewProvider(llm.Config{
		Format:  llm.FormatAnthropic,
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "claude-sonnet-5-5",
	})
	if err != nil {
		t.Fatal(err)
	}

	c := NewCompactor(prov, "claude-sonnet-5-5")
	g, b, nodes := sampleCompactionInputs()
	got, err := c.compress(context.Background(), g, b, nodes)
	if err != nil {
		t.Fatalf("compress failed: %v", err)
	}
	if got != "Consolidated summary (#10,#11)." {
		t.Fatalf("unexpected compressed body: %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("expected 1 request, got %d", len(bodies))
	}
	if _, hasThinking := bodies[0]["thinking"]; hasThinking {
		t.Fatalf("compaction request must not send rejected thinking parameter when profile has none: %+v", bodies[0]["thinking"])
	}
}

// Regression for #550: if a profile itself sets ThinkingType="disabled" and the
// provider returns the Anthropic 400 requesting thinking.type="between_tools",
// compaction retries once with "between_tools" and succeeds.
func TestCompactorCompressRetriesWithBetweenToolsOnAnthropic400(t *testing.T) {
	var (
		mu            sync.Mutex
		thinkingTypes []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		tt := ""
		if th, ok := m["thinking"].(map[string]any); ok {
			tt, _ = th["type"].(string)
		}
		mu.Lock()
		thinkingTypes = append(thinkingTypes, tt)
		mu.Unlock()
		if tt == "disabled" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"To turn thinking off on this model, send \"thinking\": {\"type\": \"between_tools\"} instead of {\"type\": \"disabled\"}."}}`))
			return
		}
		if tt != "between_tools" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"unexpected thinking mode"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_2","type":"message","role":"assistant","content":[{"type":"text","text":"Summary via between_tools (#10,#11)."}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":6}}`))
	}))
	defer srv.Close()

	prov, err := llm.NewProvider(llm.Config{
		Format:       llm.FormatAnthropic,
		BaseURL:      srv.URL,
		APIKey:       "test-key",
		Model:        "claude-sonnet-5-5",
		ThinkingType: "disabled",
	})
	if err != nil {
		t.Fatal(err)
	}

	c := NewCompactor(prov, "claude-sonnet-5-5")
	g, b, nodes := sampleCompactionInputs()
	got, err := c.compress(context.Background(), g, b, nodes)
	if err != nil {
		t.Fatalf("compress failed on between_tools fallback: %v", err)
	}
	if got != "Summary via between_tools (#10,#11)." {
		t.Fatalf("unexpected compressed body: %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(thinkingTypes) != 2 || thinkingTypes[0] != "disabled" || thinkingTypes[1] != "between_tools" {
		t.Fatalf("expected thinking sequence [disabled between_tools], got %v", thinkingTypes)
	}
}
