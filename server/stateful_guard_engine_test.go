package server

import (
	"context"
	"testing"
	"time"

	"github.com/Autumn-27/artex/intercept"
)

func TestEngineStatefulGuardFlagAndPlannerContext(t *testing.T) {
	cases := []struct {
		name      string
		env       string
		wantGuard bool
	}{
		{name: "default_on", env: "", wantGuard: true},
		{name: "explicit_on", env: "on", wantGuard: true},
		{name: "explicit_off", env: "off", wantGuard: false},
		{name: "explicit_false", env: "false", wantGuard: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(intercept.StatefulGuardEnv, tc.env)
			e := &Engine{}
			tk := &Task{ID: "77"}
			sg := e.statefulGuardForTask()
			pctx := e.plannerGateContext(context.Background(), tk, time.Second)
			got := intercept.StatefulGuardFromContext(pctx)
			if tc.wantGuard {
				if sg == nil || got != sg {
					t.Fatalf("expected non-nil stateful guard attached to plannerGateContext, got sg=%v ctx=%v", sg, got)
				}
			} else {
				if sg != nil || got != nil {
					t.Fatalf("expected nil stateful guard when %s=%q, got sg=%v ctx=%v", intercept.StatefulGuardEnv, tc.env, sg, got)
				}
			}
		})
	}
}
