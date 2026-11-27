package intercept

import (
	"strings"
	"testing"
	"time"
)

// matchArgvWithin matches cmd against a single ["nmap"] argv rule and fails
// the test if the match takes longer than limit, rather than hanging the test
// binary on a regression.
func matchArgvWithin(t *testing.T, limit time.Duration, action, cmd string) bool {
	t.Helper()
	done := make(chan bool, 1)
	go func() {
		_, ok := matchSingleArgvRule(action, `["nmap"]`, "Bash", bashInput(cmd))
		done <- ok
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(limit):
		t.Fatalf("%s rule did not return within %v for %.60q...", action, limit, cmd)
		return false
	}
}

// Each unknown option on a wrapper is tried as taking no value and as taking
// one. Before the walks were memoized, the work grew like Fib(n) in the number
// of unknown options (about 40 took seconds, 80 never returned), and nested
// wrappers multiplied it again through the candidate loop.
func TestArgvCompoundManyUnknownWrapperOptions(t *testing.T) {
	const n = 80
	opts := func(prog, opt string) string { return prog + strings.Repeat(" "+opt, n) }
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"nohup short", opts("nohup", "-Z") + " id", false},
		{"nohup long", opts("nohup", "--x") + " id", false},
		{"sudo short", opts("sudo", "-Z") + " id", false},
		{"env short", opts("env", "-Z") + " id", false},
		{"env long", opts("env", "--x") + " id", false},
		{"xargs ambiguous", opts("xargs", "-l") + " id", false},
		{"nested nohup", strings.Repeat("nohup -Z ", n) + "id", false},
		{"nested env", strings.Repeat("env -Z ", n) + "id", false},
		{"nested mixed", strings.Repeat("sudo -Z nohup -Z env -Z ", n/3) + "id", false},

		// The wrapped command is still found behind the same options.
		{"nohup short nmap", opts("nohup", "-Z") + " nmap -p 80 x", true},
		{"env long nmap", opts("env", "--x") + " nmap -p 80 x", true},
		{"nested nohup nmap", strings.Repeat("nohup -Z ", n) + "nmap -p 80 x", true},
		{"nested mixed nmap", strings.Repeat("sudo -Z nohup -Z env -Z ", n/3) + "nmap -p 80 x", true},
	}
	for _, tc := range cases {
		for _, action := range []string{"deny", "ask"} {
			if got := matchArgvWithin(t, 5*time.Second, action, tc.cmd); got != tc.want {
				t.Errorf("%s: %s rule matched=%v, want %v", tc.name, action, got, tc.want)
			}
		}
	}
}
