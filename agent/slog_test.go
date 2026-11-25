package agent

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/llmrec"
)

func TestSlogPreservesTagsAndLevels(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Default().Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	noaWarn("sess-1")("fallback triggered")
	_, capt := llmrec.NewCapture(context.Background())
	logTestConnection(Config{Format: FormatBedrockGateway}, capt)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "WARN [noa]") || !strings.Contains(lines[0], "session=sess-1") {
		t.Fatalf("unexpected noa log line: %q", lines[0])
	}
	if !strings.Contains(lines[1], "WARN [llm-test]") {
		t.Fatalf("unexpected llm-test log line: %q", lines[1])
	}
}
