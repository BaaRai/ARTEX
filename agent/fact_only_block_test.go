package agent

import (
	"strings"
	"testing"
)

// Блок «доказал — не зарегистрировал» обязан попадать в ситуационный блок планировщика:
// без него счётчик есть, а планировщик его не видит. Второй случай — тесты и вызовы без
// factOnly (nil) не должны падать и не должны печатать блок.
func TestPlannerSituationalCarriesFactOnlyBlock(t *testing.T) {
	with := plannerSituational(nil, triggerLookup{
		factOnly: func() string {
			return "\n\n[Proved but not registered: 2 finished intent(s) produced facts and NO finding.]\n"
		},
	}, nil, newDataEnvelope())
	if !strings.Contains(with, "Proved but not registered") {
		t.Fatalf("situational block lost the fact-only nudge:\n%s", with)
	}

	without := plannerSituational(nil, triggerLookup{}, nil, newDataEnvelope())
	if strings.Contains(without, "Proved but not registered") {
		t.Fatalf("empty lookup should not print the block:\n%s", without)
	}
}
