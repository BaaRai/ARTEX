package intercept

import (
	"encoding/json"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func bashInput(cmd string) []byte {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

func matchSingleArgvRule(action, pattern, toolName string, input []byte) (Decision, bool) {
	ic := New(nil)
	ic.SetRulesForTest([]db.InterceptRule{{
		ID:          1,
		Name:        action + " " + pattern,
		Enabled:     true,
		Priority:    100,
		MatchTarget: "tool_input",
		MatchType:   "argv",
		Pattern:     pattern,
		Action:      action,
	}})
	return ic.Match(toolName, input)
}

func TestArgvDenyAndAskCompoundAndWrapped(t *testing.T) {
	nmapShouldMatch := []struct {
		name string
		cmd  string
	}{
		{"plain", "nmap -p 80 x"},
		{"command wrapper", "command nmap -p 80 x"},
		{"command double dash", "command -- nmap -p 80 x"},
		{"command -p", "command -p nmap -p 80 x"},
		{"path qualified", "/usr/bin/nmap -p 80 x"},
		{"sudo", "sudo nmap -p 80 x"},
		{"sudo with flags and assign", "sudo -u root -n FOO=1 nmap -p 80 x"},
		{"doas", "doas -u root nmap -p 80 x"},
		{"env with assign", "env FOO=1 nmap -p 80 x"},
		{"env with flags", "env -i -u HOME -C /tmp -S arg FOO=1 nmap -p 80 x"},
		{"env with expansion in assign", "env FOO=$(id) nmap -p 80 x"},
		{"timeout", "timeout 30 nmap -p 80 x"},
		{"timeout with flags", "timeout -k 5 -s KILL 30 nmap -p 80 x"},
		{"nohup", "nohup nmap -p 80 x"},
		{"setsid", "setsid -f nmap -p 80 x"},
		{"stdbuf", "stdbuf -oL -eL nmap -p 80 x"},
		{"nice -n", "nice -n 10 nmap -p 80 x"},
		{"nice numeric flag", "nice -10 nmap -p 80 x"},
		{"ionice", "ionice -c 3 nmap -p 80 x"},
		{"taskset", "taskset -c 0-1 nmap -p 80 x"},
		{"chrt", "chrt -f 50 nmap -p 80 x"},
		{"watch", "watch -n 5 nmap -p 80 x"},
		{"flock", "flock -n /tmp/lock nmap -p 80 x"},
		{"strace", "strace -f -o /tmp/trace nmap -p 80 x"},
		{"ltrace", "ltrace -f -o /tmp/ltrace nmap -p 80 x"},
		{"xargs", "echo 10.0.0.1 | xargs -n 1 nmap -p 80"},
		{"xargs -I", "xargs -I{} nmap -p 80 {}"},
		{"exec", "exec -- nmap -p 80 x"},
		{"exec -a", "exec -a fake nmap -p 80 x"},
		{"semicolon list", "nmap -p 80 x; true"},
		{"and list", "true && nmap -p 80 x"},
		{"or list", "false || nmap -p 80 x"},
		{"pipeline", "nmap -p 80 x | tee out"},
		{"subshell", "(nmap -p 80 x)"},
		{"redirect", "nmap -p 80 x > out"},
		{"background", "nmap -p 80 x &"},
		{"prefix assign", "FOO=1 nmap -p 80 x"},
		{"expansion in later arg", "nmap $(id)"},
		{"param expansion in later arg", "nmap $TARGET"},
		{"command subst inside echo", "echo $(nmap -p 80 x)"},
		{"bash -c single quoted", "bash -c 'nmap -p 80 x'"},
		{"sh -c double quoted", `sh -c "nmap -p 80 x"`},
		{"zsh -c", "zsh -c 'nmap -p 80 x'"},
		{"dash -c", "dash -c 'nmap -p 80 x'"},
		{"ksh -c", "ksh -c 'nmap -p 80 x'"},
		{"fish -c", "fish -c 'nmap -p 80 x'"},
		{"path qualified bash -c", "/bin/bash -c 'nmap -p 80 x'"},
		{"eval quoted", "eval 'nmap -p 80 x'"},
		{"eval separate args", "eval nmap -p 80 x"},
		{"eval with trailing subst", "eval nmap $(id)"},
		{"stacked wrappers", "sudo env FOO=1 timeout 30 nohup command /usr/bin/nmap -p 80 x"},
		{"nested payload depth 2", `bash -c "sh -c 'nmap -p 80 x'"`},
		{"nested payload depth 3", `bash -c "sh -c \"eval 'nmap -p 80 x'\""`},
		{"bad nested payload does not block later call", `bash -c "if then"; nmap -p 80 x`},
	}

	for _, action := range []string{"deny", "ask"} {
		for _, tc := range nmapShouldMatch {
			t.Run(action+"/match/"+tc.name, func(t *testing.T) {
				dec, ok := matchSingleArgvRule(action, `["nmap"]`, "Bash", bashInput(tc.cmd))
				if !ok || dec.Action != action {
					t.Errorf("%s [\"nmap\"] on %q: got (%+v, %v), want match with action %q", action, tc.cmd, dec, ok, action)
				}
			})
		}
	}

	nmapShouldNotMatch := []struct {
		name string
		cmd  string
	}{
		{"argument to echo", "echo nmap"},
		{"prefix of other binary", "nmapx"},
		{"argument in pipeline", "echo x | grep nmap"},
		{"command -v lookup", "command -v nmap"},
		{"command -V lookup", "command -V nmap"},
		{"sudo -l list", "sudo -l nmap"},
		{"sudo -e edit", "sudo -e nmap"},
		{"sudo unknown flag", "sudo --unknown-flag nmap -p 80 x"},
		{"env unknown flag", "env -Z nmap -p 80 x"},
		{"env no command", "env FOO=1"},
		{"ionice pid mode", "ionice -p 1234"},
		{"taskset pid mode", "taskset -p 1234"},
		{"chrt pid mode", "chrt -p 1234"},
		{"xargs ambiguous -l", "xargs -l nmap"},
		{"shell with non-c option", "bash -x -c 'nmap -p 80 x'"},
		{"shell script file", "bash nmap"},
		{"eval opaque leading arg", "eval $CMD nmap"},
		{"opaque command token", "$CMD -p 80 x"},
		{"nested payload depth 4 exceeds limit", `bash -c "sh -c \"bash -c 'eval \\\"nmap -p 80 x\\\"'\""`},
		{"unparsable single quote", "nmap 'unterminated"},
		{"unparsable subst", "nmap $("},
		{"empty command", ""},
	}

	for _, action := range []string{"deny", "ask"} {
		for _, tc := range nmapShouldNotMatch {
			t.Run(action+"/nomatch/"+tc.name, func(t *testing.T) {
				if dec, ok := matchSingleArgvRule(action, `["nmap"]`, "Bash", bashInput(tc.cmd)); ok {
					t.Errorf("%s [\"nmap\"] on %q unexpectedly matched: %+v", action, tc.cmd, dec)
				}
			})
		}
	}

	// Non-Bash tool never matches argv rules.
	if dec, ok := matchSingleArgvRule("deny", `["nmap"]`, "shell_open", bashInput("nmap -p 80 x")); ok {
		t.Errorf("non-Bash tool matched deny rule: %+v", dec)
	}
}

func TestArgvDenyMultiTokenAndPathRules(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		cmd     string
		want    bool
	}{
		{"git config path qualified with flag", `["git","config"]`, "/usr/bin/git config --list", true},
		{"git config via command", `["git","config"]`, "command git config --list", true},
		{"git config via sudo", `["git","config"]`, "sudo git config user.name foo", true},
		{"git config opaque subcommand", `["git","config"]`, "git $SUB --list", false},
		{"git status does not match git config", `["git","config"]`, "git status", false},
		{"exact path rule matches exact path", `["/usr/bin/nmap"]`, "/usr/bin/nmap -p 80 x", true},
		{"exact path rule does not match bare name", `["/usr/bin/nmap"]`, "nmap -p 80 x", false},
		{"exact path rule does not match other path", `["/usr/bin/nmap"]`, "/opt/bin/nmap -p 80 x", false},
		{"builtin wrapper matches echo", `["echo"]`, "builtin -- echo hi", true},
		{"xargs with no command does not invent echo", `["echo"]`, "xargs -n 1", false},
		{"wrapper itself can still be denied", `["sudo"]`, "sudo nmap -p 80 x", true},
		{"shell itself can still be denied", `["bash"]`, "bash -c 'echo hi'", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got := matchSingleArgvRule("deny", tc.pattern, "Bash", bashInput(tc.cmd))
			if got != tc.want {
				t.Errorf("deny %s on %q = %v, want %v", tc.pattern, tc.cmd, got, tc.want)
			}
		})
	}
}

func TestArgvAllowRemainsStrict(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"nmap -p 80 x", true},
		{"command nmap x", false},
		{"nmap x; true", false},
		{"bash -c 'nmap x'", false},
		{"/usr/bin/nmap x", false},
		{"sudo nmap x", false},
		{"env FOO=1 nmap x", false},
		{"nmap $(id)", false},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			_, got := matchSingleArgvRule("allow", `["nmap"]`, "Bash", bashInput(tc.cmd))
			if got != tc.want {
				t.Errorf("allow [\"nmap\"] on %q = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestArgvCompoundMalformedInputDoesNotPanic(t *testing.T) {
	inputs := [][]byte{
		nil,
		[]byte(``),
		[]byte(`not json`),
		[]byte(`{}`),
		bashInput(`nmap 'unterminated`),
		bashInput(`nmap $(`),
		bashInput(`if then fi; (( (`),
		bashInput(`bash -c "unterminated`),
		bashInput(`eval "((("`),
	}
	for _, action := range []string{"deny", "ask", "allow"} {
		for _, in := range inputs {
			if dec, ok := matchSingleArgvRule(action, `["nmap"]`, "Bash", in); ok {
				t.Errorf("%s matched malformed input %q: %+v", action, string(in), dec)
			}
		}
	}
}

func TestArgvPriorityPreservedWithCompoundDeny(t *testing.T) {
	ic := New(nil)
	ic.SetRulesForTest([]db.InterceptRule{
		{ID: 1, Name: "allow simple nmap", Enabled: true, Priority: 100, MatchTarget: "tool_input", MatchType: "argv", Pattern: `["nmap"]`, Action: "allow"},
		{ID: 2, Name: "deny compound nmap", Enabled: true, Priority: 50, MatchTarget: "tool_input", MatchType: "argv", Pattern: `["nmap"]`, Action: "deny"},
	})

	// Simple command hits the higher-priority allow rule.
	dec, ok := ic.Match("Bash", bashInput("nmap -p 80 x"))
	if !ok || dec.Action != "allow" || dec.RuleID != 1 {
		t.Fatalf("simple nmap: got (%+v, %v), want allow rule 1", dec, ok)
	}
	// Compound command fails the strict allow rule and falls through to the deny rule.
	dec, ok = ic.Match("Bash", bashInput("nmap -p 80 x; true"))
	if !ok || dec.Action != "deny" || dec.RuleID != 2 {
		t.Fatalf("compound nmap: got (%+v, %v), want deny rule 2", dec, ok)
	}
}
