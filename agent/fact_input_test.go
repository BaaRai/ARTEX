package agent

import (
	"encoding/json"
	"regexp"
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
		"anyOf": []any{map[string]any{"required": []string{"summary"}}},
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
	if _, exists := schema["anyOf"]; exists {
		t.Fatal("root anyOf reaches the provider")
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
		props["confidence"].(map[string]any)["default"] != "inferred" || props["custom_field"] != nil {
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

func TestRecordFactStrictFields(t *testing.T) {
	tools := NewToolSet(guardTaskStore(t), "worker")
	for _, tc := range []struct{ input, field string }{
		{`{"summary":"valid","typo":1}`, "typo"},
		{`{"Summary":"case alias"}`, "Summary"},
		{`{"summary":"valid","Facts":[]}`, "Facts"},
		{`{"facts":[{"summary":"first"},{"summary":"second","typo":1}]}`, "typo"},
		{`{"facts":[{"summary":"first"},{"Summary":"case alias"}]}`, "Summary"},
		{`{"facts":[{"summary":"first"},{"summary":"second","ASSET_IDS":[]}]}`, "ASSET_IDS"},
		{`{"facts":[{"summary":"nested","facts":[]}]}`, "facts"},
		{`{"summary":"ignored","facts":[{"summary":"batch"}]}`, "summary"},
		{`{"detail":"ignored","facts":[{"summary":"batch"}]}`, "detail"},
		{`{"evidence":"ignored","facts":[{"summary":"batch"}]}`, "evidence"},
		{`{"source":"inference","facts":[{"summary":"batch"}]}`, "source"},
		{`{"confidence":"inferred","facts":[{"summary":"batch"}]}`, "confidence"},
		{`{"asset_ids":[],"facts":[{"summary":"batch"}]}`, "asset_ids"},
		{`{"tested_asset_ids":[],"facts":[{"summary":"batch"}]}`, "tested_asset_ids"},
	} {
		out := callGuardTool(t, tools.recordFact(), tc.input)
		if !out.IsError || !strings.Contains(out.Flatten(), tc.field) {
			t.Errorf("input %s: want named rejection for %s, got %s", tc.input, tc.field, out.Flatten())
		}
	}
	if tools.Writes().Facts != 0 {
		t.Fatal("malformed fields caused partial writes")
	}
	for _, input := range []string{
		`{"facts":[null]}`, `{"facts":[[]]}`, `{"summary":"valid"} {"summary":"extra object"}`,
	} {
		if _, _, err := decodeRecordFactInput(json.RawMessage(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
	if _, batch, err := decodeRecordFactInput(json.RawMessage(`{"intent_id":1,"facts":[{"summary":"valid","intent_id":2}]}`)); err != nil || !batch {
		t.Fatalf("valid batch: %v %v", batch, err)
	}
}

func TestRecordFactSchemaMatchesStrings(t *testing.T) {
	props := recordFactSchema()["properties"].(map[string]any)
	summary := regexp.MustCompile(props["summary"].(map[string]any)["pattern"].(string))
	for _, value := range []string{"", " ", "\t\r\n", "\u00a0\u0085", "\u2003\u3000", "conclusion", "  observed  "} {
		if got, want := summary.MatchString(value), strings.TrimSpace(value) != ""; got != want {
			t.Errorf("summary %q: schema=%v runtime=%v", value, got, want)
		}
	}
	id := regexp.MustCompile(factAssetIDSchema()["pattern"].(string))
	for _, value := range []string{"1", "001", "+1", " +001 ", "\u200312\u3000", "", "0", "-1", "1.5", "a"} {
		raw, _ := json.Marshal(value)
		if got, want := id.MatchString(value), pid(raw) > 0; got != want {
			t.Errorf("asset ID %q: schema=%v runtime=%v", value, got, want)
		}
	}
	for _, input := range []string{
		`{"summary":"single","asset_ids":["1",2]}`,
		`{"facts":[{"summary":"batch","tested_asset_ids":["1",2]}]}`,
	} {
		if err := actool.ValidateInput(recordFactSchema(), json.RawMessage(input)); err != nil {
			t.Errorf("numeric-string compatibility rejected: %v", err)
		}
	}
}
