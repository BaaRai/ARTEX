package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Autumn-27/artex/knowledge"
	actool "github.com/Autumn-27/norma/tool"
)

// knowledgeLookup is a read-only taxonomy lookup shared by the planner and the
// workers. It has no task store dependency: the pack is embedded in the binary.
func (t *ToolSet) knowledgeLookup() actool.CoreTool {
	return readTool("knowledge_lookup",
		"Look up a stable security taxonomy entry by id or keyword. Sources: attack (MITRE ATT&CK technique), wstg (OWASP Web Security Testing Guide test), cwe (MITRE Common Weakness Enumeration). Use it to choose a test or technique while planning, or to cite an id when reporting a finding or fact. Returns id, name, category, a capped description and the canonical url; truncated=true means the full text is at the url. Read-only.",
		obj(map[string]any{
			"source": str("taxonomy to search: attack, wstg or cwe"),
			"query":  str("entry id (for example T1190, WSTG-ATHN-01, CWE-79) or keywords"),
			"limit":  intp("maximum matches, default 5, max 20"),
		}, "source", "query"),
		knowledgeLookupRun)
}

func knowledgeLookupRun(_ context.Context, in json.RawMessage) (actool.Result, error) {
	var a struct {
		Source string `json:"source"`
		Query  string `json:"query"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(in, &a); err != nil {
		return actool.Errorf("invalid input: " + err.Error()), nil
	}
	pack, err := knowledge.Default()
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	source := strings.ToLower(strings.TrimSpace(a.Source))
	matches, total, err := pack.Search(source, a.Query, a.Limit)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	return jsonResult(map[string]any{
		"source":  source,
		"query":   strings.TrimSpace(a.Query),
		"matches": matches,
		"total":   total,
	})
}
