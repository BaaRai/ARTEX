package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKnowledgeLookupRunExactID(t *testing.T) {
	res, err := knowledgeLookupRun(t.Context(), json.RawMessage(`{"source":"attack","query":"T1190"}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Flatten())
	}
	out := res.Flatten()
	for _, want := range []string{`"T1190"`, "Exploit Public-Facing Application", "attack.mitre.org"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestKnowledgeLookupRunKeyword(t *testing.T) {
	res, err := knowledgeLookupRun(t.Context(), json.RawMessage(`{"source":"wstg","query":"default credentials","limit":3}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Flatten())
	}
	if !strings.Contains(res.Flatten(), "WSTG-ATHN-01") {
		t.Errorf("output does not contain WSTG-ATHN-01: %s", res.Flatten())
	}
}

func TestKnowledgeLookupRunLimitIsClamped(t *testing.T) {
	res, err := knowledgeLookupRun(t.Context(), json.RawMessage(`{"source":"cwe","query":"data","limit":100}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Flatten())
	}
	var out struct {
		Matches []map[string]any `json:"matches"`
		Total   int              `json:"total"`
	}
	if err := json.Unmarshal([]byte(res.Flatten()), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Matches) > 20 {
		t.Fatalf("matches = %d, want at most 20", len(out.Matches))
	}
	if out.Total <= len(out.Matches) {
		t.Fatalf("total = %d, matches = %d", out.Total, len(out.Matches))
	}
}

func TestKnowledgeLookupRunErrors(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"unknown source", `{"source":"nope","query":"x"}`, "unknown knowledge source"},
		{"empty query", `{"source":"attack","query":"  "}`, "query is required"},
		{"invalid json", `{`, "invalid input"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := knowledgeLookupRun(t.Context(), json.RawMessage(c.in))
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if !res.IsError || !strings.Contains(res.Flatten(), c.want) {
				t.Fatalf("result = %q (isError=%v), want error containing %q", res.Flatten(), res.IsError, c.want)
			}
		})
	}
}

func TestKnowledgeLookupToolIsReadOnly(t *testing.T) {
	tool := (&ToolSet{}).knowledgeLookup()
	if tool.Name() != "knowledge_lookup" {
		t.Fatalf("name = %q", tool.Name())
	}
	if !tool.IsReadOnly(nil) {
		t.Fatal("tool is not read-only")
	}
}
