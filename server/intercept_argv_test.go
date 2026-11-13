package server

import "testing"

func TestValidateArgvRuleReq(t *testing.T) {
	base := interceptRuleReq{Name: "r", MatchTarget: "tool_input", MatchType: "argv", Pattern: `["git","push"]`, Action: "allow"}
	if err := validateInterceptRuleReq(base); err != nil {
		t.Fatalf("valid argv rule refused: %v", err)
	}
	for name, mut := range map[string]func(*interceptRuleReq){
		"tool_name target": func(r *interceptRuleReq) { r.MatchTarget = "tool_name" },
		"empty pattern":    func(r *interceptRuleReq) { r.Pattern = "" },
		"empty token":      func(r *interceptRuleReq) { r.Pattern = `["git",""]` },
		"flag token":       func(r *interceptRuleReq) { r.Pattern = `["git","-C"]` },
		"metacharacter":    func(r *interceptRuleReq) { r.Pattern = `["nmap;id"]` },
		"nine tokens":      func(r *interceptRuleReq) { r.Pattern = `["a","b","c","d","e","f","g","h","i"]` },
		"not an array":     func(r *interceptRuleReq) { r.Pattern = `git push` },
	} {
		req := base
		mut(&req)
		if err := validateInterceptRuleReq(req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
