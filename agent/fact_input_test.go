package agent

import (
	"encoding/json"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func TestRecordFactSummaryContract(t *testing.T) {
	tools := NewToolSet(guardTaskStore(t), "worker")
	tool := tools.recordFact()
	for _, input := range []string{
		`{}`, `null`, `[]`,
		`{"summary":""}`, `{"summary":"   "}`,
		`{"summary":"must not substitute","facts":[]}`,
		`{"summary":"must not substitute","facts":null}`,
		`{"summary":"must not inherit","facts":[{"detail":"missing summary"}]}`,
		`{"facts":[{"summary":"must not save"},{"summary":42}]}`,
	} {
		out := callGuardTool(t, tool, input)
		if !out.IsError {
			t.Errorf("accepted %s: %s", input, out.Flatten())
		}
	}
	if tools.Writes().Facts != 0 {
		t.Fatal("invalid inputs wrote facts")
	}
	single := callGuardTool(t, tool, `{"summary":"valid single"}`)
	if single.IsError {
		t.Fatal(single.Flatten())
	}
	batch := callGuardTool(t, tool, `{"facts":[{"summary":"valid batch"},{"detail":"missing summary"}]}`)
	var result struct {
		IDs    []int64           `json:"ids"`
		Errors map[string]string `json:"errors"`
		Retry  string            `json:"retry_instruction"`
	}
	if err := json.Unmarshal([]byte(batch.Flatten()), &result); err != nil {
		t.Fatal(batch.Flatten(), err)
	}
	if !batch.IsError || len(result.IDs) != 2 || result.IDs[0] <= 0 || result.IDs[1] != 0 ||
		!strings.Contains(result.Errors["1"], "summary") || result.Retry == "" || tools.Writes().Facts != 2 {
		t.Fatalf("partial failure is not actionable: %s", batch.Flatten())
	}
}

func TestRecordFactStoredContractRepair(t *testing.T) {
	old := map[string]any{
		"type": "object", "required": []any{"summary"},
		"properties": map[string]any{
			"summary":      map[string]any{"type": "string", "description": "operator summary"},
			"confidence":   map[string]any{"type": "string", "default": "inferred"},
			"custom_field": map[string]any{"type": "string"},
			"facts":        map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		},
	}
	base := NewToolSet(nil, "").recordFact()
	repaired := DecorateTool(base, "operator tool", old)
	schema := repaired.InputSchema()
	if _, required := schema["required"]; required {
		t.Fatal("batch still requires a top-level summary")
	}
	if len(schema["anyOf"].([]any)) != 2 {
		t.Fatal("single/batch alternatives missing")
	}
	props := schema["properties"].(map[string]any)
	item := props["facts"].(map[string]any)["items"].(map[string]any)
	required := item["required"].([]string)
	if len(required) != 1 || required[0] != "summary" || props["facts"].(map[string]any)["minItems"] != 1 {
		t.Fatal("batch must require a nonempty list with a summary per item")
	}
	if props["summary"].(map[string]any)["minLength"] != 1 ||
		item["properties"].(map[string]any)["summary"].(map[string]any)["minLength"] != 1 {
		t.Fatal("empty summaries permitted by schema")
	}
	if repaired.Description() != "operator tool" ||
		props["summary"].(map[string]any)["description"] != "operator summary" ||
		props["confidence"].(map[string]any)["default"] != "inferred" || props["custom_field"] == nil {
		t.Fatal("operator annotations lost")
	}
	if _, ok := old["properties"].(map[string]any)["tested_asset_ids"]; ok {
		t.Fatal("shared old schema mutated")
	}
	first, _ := json.Marshal(schema)
	second, _ := json.Marshal(DecorateTool(base, "", schema).InputSchema())
	if string(first) != string(second) {
		t.Fatal("repair is not idempotent")
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(first, &roundTrip); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`{"summary":"single"}`, `{"facts":[{"summary":"batch"}]}`} {
		if err := actool.ValidateInput(roundTrip, json.RawMessage(input)); err != nil {
			t.Fatalf("SDK rejects valid %s after catalog round trip: %v", input, err)
		}
	}
}
