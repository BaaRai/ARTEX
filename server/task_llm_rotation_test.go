package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
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

// Use two real HTTP provider adapters and a barrier: provider B must finish and
// another intent must start while provider A's first request is still blocked.
func TestRotationConcurrentProvidersAndPrecedence(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started := make(chan int, 3)
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	ids := []int64{}
	for i := 0; i < 2; i++ {
		i := i
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started <- i
			select {
			case <-release[i]:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		}))
		defer endpoint.Close()
		id, err := m.pg.SaveProfile(&db.LLMProfile{Name: fmt.Sprintf("rotation-http-%d-%d", time.Now().UnixNano(), i), Format: "openai", BaseURL: endpoint.URL + "/v1", Model: "test", APIKey: "test-key"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		defer m.pg.DeleteProfile(id)
	}
	// Release blocked handlers even when an assertion fails.
	defer func() {
		for _, ch := range release {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	}()
	on := true
	task, err := m.CreateTaskWithOptions("rotation HTTP", "test", db.TaskCreateOptions{LLMProfileIDs: ids, LLMRotate: &on})
	if err != nil {
		t.Fatal(err)
	}
	defer m.DeleteTask(task.ID, DeleteTaskOptions{})
	s := &Server{m: m, provByProfile: map[int64]*provEntry{}}
	runtime := &taskLLMRuntime{s: s, taskID: task.ID, agentKey: "worker"}
	done := make(chan error, 3)
	dispatch := func(want int) {
		t.Helper()
		head, _ := task.nextRotatedProfile()
		go func() {
			_, _, _, err := runtime.Complete(withLLMProfilePin(ctx, head), llm.CompletionRequest{})
			done <- err
		}()
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("provider=%d want=%d", got, want)
			}
		case <-ctx.Done():
			t.Fatal("provider request did not start concurrently")
		}
	}
	dispatch(0)
	dispatch(1)
	close(release[1])
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	dispatch(0)
	close(release[0])
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	// An exhausted pin must fall back rather than retry the failed provider forever.
	n, _ := parseTaskID(task.ID)
	if _, err := m.pg.MarkTaskLLMProfileQuotaExhausted(n, ids[1], "insufficient_quota"); err != nil {
		t.Fatal(err)
	}
	sel, err := runtime.currentFor(withLLMProfilePin(ctx, ids[1]))
	if err != nil || sel.profileID != ids[0] {
		t.Fatalf("exhausted pin selected: %+v, %v", sel, err)
	}
	if err := m.pg.SetAgentLLMProfile("worker", &ids[1]); err != nil {
		t.Fatal(err)
	}
	defer m.pg.SetAgentLLMProfile("worker", nil)
	sel, err = runtime.currentFor(withLLMProfilePin(ctx, ids[0]))
	if err != nil || sel.profileID != 0 {
		t.Fatalf("fixed binding did not outrank rotation: %+v, %v", sel, err)
	}
	off := false
	if _, err := m.ReplaceTaskLLMProfiles(task.ID, []int64{ids[1], ids[0]}, 0, &off); err != nil {
		t.Fatal(err)
	}
	if task.llmStateSnapshot().Rotate || taskDTO(task, "idle").LLMRotate {
		t.Fatal("live task or API response retained disabled rotation")
	}
	if _, err := m.ReplaceTaskLLMProfiles(task.ID, ids, 0, &on); err != nil {
		t.Fatal(err)
	}
	pt, err := m.pg.GetTask(n)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := taskFromPG(pt, task.Store, m.interceptor)
	if !reloaded.llmStateSnapshot().Rotate {
		t.Fatal("task reload lost enabled rotation")
	}
}

func TestRotationConcurrentDispatchAndUpdates(t *testing.T) {
	task := &Task{LLMProfileIDs: []int64{1, 2}, LLMRotate: true}
	const workers = 100
	counts := make(chan int64, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, ok := task.nextRotatedProfile()
			if !ok {
				t.Error("missing assignment")
				return
			}
			counts <- id
		}()
	}
	wg.Wait()
	close(counts)
	got := map[int64]int{}
	for id := range counts {
		got[id]++
	}
	if got[1] != workers/2 || got[2] != workers/2 {
		t.Fatalf("unbalanced assignments: %v", got)
	}
	// Replace the chain while dispatching, exercising the shared list lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < workers; i++ {
			task.setLLMState(nil, nil, []int64{3, 4}, int64(i+1), "ready", "", i%2 == 0)
		}
	}()
	for i := 0; i < workers; i++ {
		task.nextRotatedProfile()
		task.llmStateSnapshot()
	}
	wg.Wait()
}

func TestNextRotatedProfileRoundRobin(t *testing.T) {
	tk := &Task{LLMProfileIDs: []int64{3, 1, 2}, LLMRotate: true}
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
	tk.setLLMState(nil, nil, []int64{3, 1, 2}, 1, "ready", "", false)
	if _, ok := tk.nextRotatedProfile(); ok {
		t.Fatal("disabled rotation must not assign a pin")
	}
}
