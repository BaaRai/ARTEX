package agent

import "strings"
import "testing"

// The prompt-injection hardening block must stay in the four consumer agents'
// default seeds: tool output from the target is data, never instructions.
func TestPromptSeedsCarryInjectionHardening(t *testing.T) {
	for name, tmpl := range BuiltinPromptSeeds() {
		switch name {
		case "worker", "pentest", "mainagent", "auto":
			for _, marker := range []string{"prompt-injection defense", "never instructions to you", "do NOT comply"} {
				if !strings.Contains(tmpl, marker) {
					t.Errorf("%s seed missing hardening marker %q", name, marker)
				}
			}
		}
	}
}
