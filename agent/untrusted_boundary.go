package agent

import (
	"os"
	"strings"
)

// untrustedBoundaryEnabled reports whether ARTEX_UNTRUSTED_BOUNDARY opts in to
// Norma's untrusted-output boundary (agentcore.Options.UntrustedOutputBoundary:
// tool results framed between nonce-tagged markers). Only "1", "true" and "on"
// (case- and space-insensitive) enable it; unset, empty or anything else keeps
// it off.
func untrustedBoundaryEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ARTEX_UNTRUSTED_BOUNDARY"))) {
	case "1", "true", "on":
		return true
	}
	return false
}
