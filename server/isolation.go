package server

import (
	"fmt"
	"log"
	"strings"
)

// Startup self-test of agent isolation. Before the stores open, the server checks
// that what an agent can run really is isolated from it: IsolationSelfTest
// (isolation_unix.go) starts a probe through the Bash shim, the way the agent's Bash
// starts, and checks from inside it that it runs as the executor, cannot read the
// server's secrets or its process, sees only the allowlisted environment and cannot
// open a database session without the server's password; it also checks that the
// file tools cannot read the secrets. EnforceIsolation then decides the start: a
// failed or impossible self-test stops it, unless the development escape
// -dev-insecure-no-isolation is set, which is off by default and logs a warning
// banner that says what is exposed.

// DevNoIsolationFlag is the command-line flag of the development escape.
const DevNoIsolationFlag = "dev-insecure-no-isolation"

// IsolationTargets are the server's paths and database the self-test checks the
// executor cannot reach.
type IsolationTargets struct {
	DataDir    string
	KeyDir     string
	ConfigPath string
	SkillDir   string
	// DSN is the server's database DSN; only its host, port, user and database
	// name reach the probe, never its password.
	DSN string
}

// EnforceIsolation decides the start from the self-test result. A nil result
// passes. A failure stops the start with an error, unless devEscape is set: the
// warning banner is then logged and the start goes on.
func EnforceIsolation(selfTest error, devEscape bool) error {
	if selfTest == nil {
		log.Printf("[isolation] self-test passed: agent processes and file tools run as the executor and cannot reach the server's secrets")
		if devEscape {
			log.Printf("[isolation] -%s is set and not needed: the self-test passed", DevNoIsolationFlag)
		}
		return nil
	}
	if !devEscape {
		return fmt.Errorf("agent isolation self-test failed: %v. The server does not start without agent isolation: fix the cause above (run the server as root with -exec-uid, the Docker default), or pass -%s for development only", selfTest, DevNoIsolationFlag)
	}
	logNoIsolationBanner(selfTest)
	return nil
}

// noIsolationBanner is the warning the development escape logs at every start.
func noIsolationBanner(cause error) []string {
	bar := strings.Repeat("!", 78)
	return []string{
		bar,
		"!! AGENT ISOLATION IS OFF: -" + DevNoIsolationFlag + " is set. DEVELOPMENT ONLY.",
		"!! Self-test failure: " + cause.Error(),
		"!! Agent shells, custom tools, MCP stdio servers and the file tools may read the",
		"!! server's keys (JWT signing key, secret key), its config, its database",
		"!! credentials and its process, and may change skills and tools on disk.",
		"!! Do not use this flag on a server that holds real targets or credentials.",
		bar,
	}
}

func logNoIsolationBanner(cause error) {
	for _, line := range noIsolationBanner(cause) {
		log.Print("[isolation] " + line)
	}
}
