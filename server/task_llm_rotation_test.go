package server

import (
	"context"
	"testing"
)

func TestLLMProfilePinRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := llmProfilePinFrom(ctx); ok {
		t.Fatal("empty context must carry no pin")
	}
	pinned := withLLMProfilePin(ctx, 42)
	got, ok := llmProfilePinFrom(pinned)
	if !ok || got != 42 {
		t.Fatalf("pin roundtrip got=%d ok=%v", got, ok)
	}
}

func TestNextRotatedProfileRoundRobin(t *testing.T) {
	tk := &Task{LLMProfileIDs: []int64{3, 1, 2}}
	want := []int64{3, 1, 2, 3, 1}
	for i, w := range want {
		got, ok := tk.nextRotatedProfile()
		if !ok || got != w {
			t.Fatalf("rotation %d: got=%d ok=%v want=%d", i, got, ok, w)
		}
	}
	if _, ok := (&Task{}).nextRotatedProfile(); ok {
		t.Fatal("empty chain must not rotate")
	}
}
