package intercept

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestArgvAllowDenylist(t *testing.T) {
	for _, prog := range []string{
		"python", "python3", "python3.11", "/usr/bin/python3.11", "php8.2", "ruby3.2", "perl5", "pip3",
		"env", "/usr/bin/env", "bash", "/bin/sh", "find", "rm", "mkfs", "mkfs.ext4", "/sbin/mkfs.xfs",
		"docker", "kubectl", "npm", "sudo", "xargs", "Rscript", ".",
	} {
		if !ArgvAllowDenied(prog) {
			t.Errorf("%q is not denied", prog)
		}
	}
	for _, prog := range []string{"nmap", "git", "/usr/bin/git", "curl", "ls", "gh", "x", "7z", "pythonic"} {
		if ArgvAllowDenied(prog) {
			t.Errorf("%q is denied, want allowable", prog)
		}
	}
}

func TestAlwaysAllowPrefix(t *testing.T) {
	bash := func(cmd string) []byte {
		b, _ := json.Marshal(map[string]string{"command": cmd})
		return b
	}
	if got, err := AlwaysAllowPrefix("Bash", bash("git push --force origin main")); err != nil || !slices.Equal(got, []string{"git", "push"}) {
		t.Fatalf("git push: %q %v", got, err)
	}
	if got, err := AlwaysAllowPrefix("Bash", bash("nmap -p 80 10.0.0.1")); err != nil || !slices.Equal(got, []string{"nmap"}) {
		t.Fatalf("nmap: %q %v", got, err)
	}
	for name, c := range map[string]struct {
		tool  string
		input []byte
	}{
		"not Bash":       {"shell_open", bash("git push")},
		"compound":       {"Bash", bash("git push; id")},
		"substitution":   {"Bash", bash("git push $(id)")},
		"redirection":    {"Bash", bash("git push > out")},
		"empty":          {"Bash", bash("")},
		"no command":     {"Bash", []byte(`{}`)},
		"not json":       {"Bash", []byte(`not json`)},
		"python":         {"Bash", bash("python3.11 -c 'import os'")},
		"path to python": {"Bash", bash("/usr/bin/python3 x.py")},
		"find":           {"Bash", bash("find . -exec sh -c id ;")},
		"rm":             {"Bash", bash("rm -rf /")},
		"docker":         {"Bash", bash("docker run --privileged x")},
	} {
		if got, err := AlwaysAllowPrefix(c.tool, c.input); err == nil {
			t.Errorf("%s: prefix %q, want a refusal", name, got)
		}
	}
}

// The denylist neutralises argv allow rules at matching time (a rule created
// before the denylist, or written straight into the database), and only them:
// an argv deny or ask rule for the same program still matches.
func TestArgvDenylistAtMatching(t *testing.T) {
	ask := db.InterceptRule{ID: 99, Name: "ask every Bash call", Enabled: true, Priority: -2000, MatchTarget: "tool_name",
		MatchType: "string", Pattern: "Bash", Action: "ask"}
	argv := func(id int64, pattern, action string) db.InterceptRule {
		return db.InterceptRule{ID: id, Name: "argv " + action + " " + pattern, Enabled: true, Priority: -1000,
			MatchTarget: "tool_input", MatchType: "argv", Pattern: pattern, Action: action}
	}
	ic := New(nil)
	ic.SetRulesForTest([]db.InterceptRule{
		argv(1, `["python"]`, "allow"), argv(2, `["python3.11"]`, "allow"), argv(3, `["find"]`, "allow"),
		argv(4, `["/usr/bin/env"]`, "allow"), argv(5, `["nmap"]`, "allow"), ask,
	})
	for _, cmd := range []string{"python x.py", "python3.11 -c 'import os'", "find . -name x", "/usr/bin/env"} {
		input, _ := json.Marshal(map[string]string{"command": cmd})
		dec, ok := ic.Match("Bash", input)
		if !ok || dec.Action != "ask" || dec.RuleID != 99 {
			t.Errorf("%q: %+v %v, want the fallback ask rule", cmd, dec, ok)
		}
	}
	if dec, ok := ic.Match("Bash", []byte(`{"command":"nmap -p 80 x"}`)); !ok || dec.Action != "allow" || dec.RuleID != 5 {
		t.Errorf("nmap: %+v %v, want the argv allow", dec, ok)
	}

	ic.SetRulesForTest([]db.InterceptRule{argv(1, `["python"]`, "allow"), argv(6, `["python"]`, "deny"), argv(7, `["find"]`, "ask")})
	if dec, ok := ic.Match("Bash", []byte(`{"command":"python x.py"}`)); !ok || dec.Action != "deny" || dec.RuleID != 6 {
		t.Errorf("python: %+v %v, want the argv deny", dec, ok)
	}
	if dec, ok := ic.Match("Bash", []byte(`{"command":"find . -delete"}`)); !ok || dec.Action != "ask" || dec.RuleID != 7 {
		t.Errorf("find: %+v %v, want the argv ask", dec, ok)
	}
}
