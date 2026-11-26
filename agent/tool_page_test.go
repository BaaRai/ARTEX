package agent

import "testing"

func TestClampToolLimit(t *testing.T) {
	if got := clampToolLimit(0, 10); got != 10 {
		t.Fatalf("default = %d, want 10", got)
	}
	if got := clampToolLimit(-4, 50); got != 50 {
		t.Fatalf("negative = %d, want 50", got)
	}
	if got := clampToolLimit(20, 10); got != 20 {
		t.Fatalf("in range = %d, want 20", got)
	}
	if got := clampToolLimit(1<<30, 10); got != toolPageMax {
		t.Fatalf("huge = %d, want %d", got, toolPageMax)
	}
	if got := clampToolOffset(-1); got != 0 {
		t.Fatalf("negative offset = %d", got)
	}
	if got := clampToolOffset(toolOffsetMax + 1); got != toolOffsetMax {
		t.Fatalf("huge offset = %d", got)
	}
	if got := clampToolPage(0); got != 1 {
		t.Fatalf("page = %d, want 1", got)
	}
	if got := clampToolPage(toolPageIndexMax + 5); got != toolPageIndexMax {
		t.Fatalf("huge page = %d", got)
	}
}
