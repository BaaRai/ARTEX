package db

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestChatBridgeApprovalLifecycle(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	const kind = "t-bridge-approvals"
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM chat_bridges WHERE kind=$1`, kind) })
	if _, err := d.SaveChatBridge(ctx, &ChatBridge{Kind: kind, Enabled: true, AllowedChats: []string{"100"}}, false); err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTask("chat bridge approvals", "", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	taskID := strconv.FormatInt(task.ID, 10)
	pending, err := d.CreateInterceptPending(0, 0, taskID, "worker-1", "bash", []byte(`{"command":"id"}`), "rule says ask")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE id=$1`, pending) })

	// Nothing to push until a chat selects the task.
	if got, err := d.ChatBridgeApprovalsToPush(ctx, kind, []string{"100"}, false, 10); err != nil || len(got) != 0 {
		t.Fatalf("before selection: %+v %v", got, err)
	}
	if err := d.SetChatBridgeTask(ctx, kind, "100", "private", task.ID); err != nil {
		t.Fatal(err)
	}
	// A group that selected the same task is pushed to only when groups can
	// act (some user is allowed), and no chat outside the allowlist is.
	if err := d.SetChatBridgeTask(ctx, kind, "-100", "supergroup", task.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		chats  []string
		groups bool
		want   int
	}{{[]string{"100", "-100"}, true, 2}, {[]string{"100", "-100"}, false, 1}, {[]string{"-100"}, false, 0}, {nil, true, 0}} {
		if got, err := d.ChatBridgeApprovalsToPush(ctx, kind, c.chats, c.groups, 10); err != nil || len(got) != c.want {
			t.Fatalf("to push for %v groups=%v = %+v %v, want %d", c.chats, c.groups, got, err, c.want)
		}
	}
	got, err := d.ChatBridgeApprovalsToPush(ctx, kind, []string{"100"}, false, 10)
	if err != nil || len(got) != 1 || got[0].PendingID != pending || got[0].ChatID != "100" || got[0].ChatType != "private" || got[0].ToolName != "bash" {
		t.Fatalf("to push = %+v %v", got, err)
	}

	id, created, err := d.CreateChatBridgeApproval(ctx, kind, "100", pending, "hash-1", "Approval needed", time.Now().Add(time.Hour))
	if err != nil || !created {
		t.Fatalf("create = %v %v", created, err)
	}
	// The same request is pushed to the same chat once.
	if _, created, err := d.CreateChatBridgeApproval(ctx, kind, "100", pending, "hash-2", "again", time.Now().Add(time.Hour)); err != nil || created {
		t.Fatalf("second create = %v %v", created, err)
	}
	if got, _ := d.ChatBridgeApprovalsToPush(ctx, kind, []string{"100"}, false, 10); len(got) != 0 {
		t.Fatalf("still to push after create: %+v", got)
	}
	// Before the message id is known, a press cannot count.
	if a, err := d.UseChatBridgeApproval(ctx, kind, "100", "", "hash-1", "u"); err != nil || a != nil {
		t.Fatalf("press before send = %+v %v", a, err)
	}
	if err := d.SetChatBridgeApprovalMessage(ctx, id, "555"); err != nil {
		t.Fatal(err)
	}

	// A press from another chat, on another message or with another nonce
	// does not count.
	for _, c := range []struct{ chat, msg, hash string }{{"200", "555", "hash-1"}, {"100", "556", "hash-1"}, {"100", "555", "hash-x"}} {
		if a, err := d.UseChatBridgeApproval(ctx, kind, c.chat, c.msg, c.hash, "u"); err != nil || a != nil {
			t.Fatalf("press %+v = %+v %v", c, a, err)
		}
	}
	// The right press counts once.
	a, err := d.UseChatBridgeApproval(ctx, kind, "100", "555", "hash-1", "telegram user 7")
	if err != nil || a == nil || a.PendingID != pending || a.MessageText != "Approval needed" {
		t.Fatalf("press = %+v %v", a, err)
	}
	if a, err := d.UseChatBridgeApproval(ctx, kind, "100", "555", "hash-1", "telegram user 7"); err != nil || a != nil {
		t.Fatalf("second press = %+v %v", a, err)
	}
	// A used push is not closed again.
	if ok, err := d.CloseChatBridgeApproval(ctx, id, "expired"); err != nil || ok {
		t.Fatalf("close after use = %v %v", ok, err)
	}
}

func TestChatBridgeApprovalsToClose(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	const kind = "t-bridge-approvals-close"
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM chat_bridges WHERE kind=$1`, kind) })
	if _, err := d.SaveChatBridge(ctx, &ChatBridge{Kind: kind, Enabled: true, AllowedChats: []string{"100"}}, false); err != nil {
		t.Fatal(err)
	}
	newPending := func() int64 {
		t.Helper()
		id, err := d.CreateInterceptPending(0, 0, "", "worker-1", "bash", []byte(`{}`), "ask")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE id=$1`, id) })
		return id
	}
	open, decided, expired := newPending(), newPending(), newPending()
	push := func(pending int64, hash string, expires time.Time) int64 {
		t.Helper()
		id, _, err := d.CreateChatBridgeApproval(ctx, kind, "100", pending, hash, "text", expires)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.SetChatBridgeApprovalMessage(ctx, id, "m"+hash); err != nil {
			t.Fatal(err)
		}
		return id
	}
	push(open, "h-open", time.Now().Add(time.Hour))
	decidedRow := push(decided, "h-decided", time.Now().Add(time.Hour))
	expiredRow := push(expired, "h-expired", time.Now().Add(-time.Minute))
	if err := d.DecideInterceptPending(decided, "allowed"); err != nil {
		t.Fatal(err)
	}

	got, err := d.ChatBridgeApprovalsToClose(ctx, kind, 10)
	if err != nil || len(got) != 2 || got[0].ID != decidedRow || got[0].PendingStatus != "allowed" || got[1].ID != expiredRow || got[1].PendingStatus != "pending" {
		t.Fatalf("to close = %+v %v", got, err)
	}
	// An expired nonce no longer counts.
	if a, err := d.UseChatBridgeApproval(ctx, kind, "100", "mh-expired", "h-expired", "u"); err != nil || a != nil {
		t.Fatalf("expired press = %+v %v", a, err)
	}
	for _, a := range got {
		if ok, err := d.CloseChatBridgeApproval(ctx, a.ID, "decided"); err != nil || !ok {
			t.Fatalf("close %d = %v %v", a.ID, ok, err)
		}
	}
	if got, _ := d.ChatBridgeApprovalsToClose(ctx, kind, 10); len(got) != 0 {
		t.Fatalf("after close: %+v", got)
	}
}
