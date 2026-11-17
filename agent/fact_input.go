package agent

import "encoding/json"

func factItemSchema() map[string]any {
	schema := obj(map[string]any{
		"summary":          str("Required on every fact: a non-empty one-sentence summary of this conclusion. Batch items do not inherit summary from the top level."),
		"intent_id":        idp("The intent that produced this fact; only this field inherits from the top level in a batch."),
		"detail":           str("Details of this fact, including all related observations."),
		"evidence":         str("One line of key evidence: the command and the most proving output lines. Put longer output in detail."),
		"confidence":       str("observed (seen directly in output) | inferred (inferred from phenomena). Label negative conclusions honestly."),
		"source":           map[string]any{"type": "string", "enum": factSources, "description": "where the content came from: target_response | tool_output | third_party_intel | operator_input | inference (default when omitted)"},
		"asset_ids":        map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1}, "description": "Asset IDs explicitly involved in this fact. Use asset_ids as the canonical field; tested_asset_ids is accepted as an alias. If both are provided, they must name the same set. Do not infer IDs from the intent."},
		"tested_asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1}, "description": "Compatibility alias for asset_ids, not an additional list. When both are provided their sets must match. Only include assets actually involved in this conclusion."},
	}, "summary")
	schema["properties"].(map[string]any)["summary"].(map[string]any)["minLength"] = 1
	return schema
}

func recordFactSchema() map[string]any {
	props := factItemSchema()["properties"].(map[string]any)
	props["facts"] = map[string]any{
		"type": "array", "minItems": 1, "items": factItemSchema(),
		"description": "A non-empty batch of complete facts. Every item needs its own summary. Only intent_id inherits from the top level. Returned ids match item order; retry only failed indices.",
	}
	schema := obj(props)
	schema["anyOf"] = []any{
		map[string]any{"required": []string{"summary"}},
		map[string]any{"required": []string{"facts"}},
	}
	return schema
}

// RepairRecordFactSchema restores the single/batch input contract in stored or
// cached schemas without changing operator descriptions, defaults or extra fields.
func RepairRecordFactSchema(stored map[string]any) map[string]any {
	raw, _ := json.Marshal(stored)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{}
	}
	mergeFactContract(out, recordFactSchema())
	// A batch requires summaries on its items, never a redundant top-level one.
	delete(out, "required")
	return out
}

func mergeFactContract(dst, src map[string]any) {
	for key, value := range src {
		if key == "description" || key == "default" {
			if _, ok := dst[key]; ok {
				continue
			}
		}
		if child, ok := value.(map[string]any); ok {
			target, _ := dst[key].(map[string]any)
			if target == nil {
				target = map[string]any{}
				dst[key] = target
			}
			mergeFactContract(target, child)
		} else {
			dst[key] = value
		}
	}
}
