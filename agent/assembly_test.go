package agent

import (
	"context"
	"sync"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

// Server wiring installs the hooks while restored tasks may already be assembling
// tools on other goroutines. Readers and writers run concurrently here, so under
// -race any unsynchronised hook access is reported regardless of scheduling.
func TestAugmentToolsHooksConcurrentSwap(t *testing.T) {
	oldAugment, oldResolve, oldPolicy := ToolAugment(), ToolResolve(), FindingTrafficBindingEnabled()
	t.Cleanup(func() {
		SetToolAugment(oldAugment)
		SetToolResolve(oldResolve)
		SetFindingTrafficBindingEnabled(oldPolicy)
	})
	augment := func(context.Context, string) ([]actool.CoreTool, DeferredInfo, func()) {
		return nil, DeferredInfo{}, nil
	}
	resolve := func(_ context.Context, _ string, in []actool.CoreTool) []actool.CoreTool { return in }
	policy := func() bool { return false }

	const rounds = 200
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range rounds {
				out, _, cleanup := AugmentTools(context.Background(), "worker", nil)
				cleanup()
				if len(out) != 0 {
					t.Errorf("AugmentTools(nil base) = %d tools, want 0", len(out))
					return
				}
			}
		})
	}
	wg.Go(func() {
		for range rounds {
			SetToolAugment(augment)
			SetToolResolve(resolve)
			SetFindingTrafficBindingEnabled(policy)
			SetToolAugment(nil)
			SetToolResolve(nil)
			SetFindingTrafficBindingEnabled(nil)
		}
	})
	wg.Wait()
}
