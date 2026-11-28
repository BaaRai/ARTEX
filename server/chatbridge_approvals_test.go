package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/chatbridge"
	"github.com/Autumn-27/artex/db"
)

func TestChatBridgeApprovals(t *testing.T) {
	s, task, fake := bridgeCommandServer(t)
	ctx := context.Background()
	pg := s.m.PG()
	tid, _ := strconv.ParseInt(task.ID, 10, 64)
	if _, err := pg.UpsertTaskCredential(tid, db.TaskCredentialInput{Domain: "app.example", Username: "admin", Password: "Demo-Secret-4242", Source: "user"}); err != nil {
		t.Fatal(err)
	}
	// Chat 100 (private) and group -100 select the task. The group has no
	// allowed user, so nobody there could press: it gets no push.
	if _, err := pg.SaveChatBridge(ctx, &db.ChatBridge{Kind: fakeBridgeKind, Enabled: true, Config: json.RawMessage(`{"bot_token":"x"}`), AllowedChats: []string{"100", "-100"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := pg.SetChatBridgeTask(ctx, fakeBridgeKind, "100", chatbridge.ChatTypePrivate, tid); err != nil {
		t.Fatal(err)
	}
	if err := pg.SetChatBridgeTask(ctx, fakeBridgeKind, "-100", "supergroup", tid); err != nil {
		t.Fatal(err)
	}
	newPending := func() int64 {
		t.Helper()
		id, err := pg.CreateInterceptPending(0, 0, task.ID, "worker-1", "bash", []byte(`{"command":"cat /etc/shadow","password":"Demo-Secret-4242"}`), "rule says ask")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pg.Exec(`DELETE FROM intercept_pending WHERE id=$1`, id) })
		return id
	}

	first := newPending()
	s.chatBridgeApprovalTick(ctx, fakeBridgeKind)
	if len(fake.buttons) != 1 || fake.buttons[0].chatID != "100" {
		t.Fatalf("pushes = %+v, want one to chat 100", fake.buttons)
	}
	msg := fake.buttons[0]
	if !strings.Contains(msg.text, "Approval needed · task #"+task.ID) || !strings.Contains(msg.text, "Tool: bash · worker-1") || !strings.Contains(msg.text, "Risk: ") {
		t.Fatalf("push text = %q", msg.text)
	}
	// Excerpts are off: the request and the reason stay in ARTEX.
	if strings.Contains(msg.text, "shadow") || strings.Contains(msg.text, "rule says") || strings.Contains(msg.text, "Demo-Secret") {
		t.Fatalf("push text leaked the request: %q", msg.text)
	}
	if len(msg.rows) != 1 || len(msg.rows[0]) != 2 || msg.rows[0][0].Text != "Allow" || msg.rows[0][1].Text != "Block" {
		t.Fatalf("buttons = %+v", msg.rows)
	}
	allowData, blockData := msg.rows[0][0].Data, msg.rows[0][1].Data
	if len(allowData) > 64 || !strings.HasPrefix(allowData, "ap:") || allowData[:len(allowData)-1] != blockData[:len(blockData)-1] {
		t.Fatalf("callback data = %q / %q", allowData, blockData)
	}
	// The nonce is not stored, only its hash.
	var stored string
	if err := pg.QueryRow(`SELECT nonce_hash FROM chat_bridge_approvals WHERE pending_id=$1`, first).Scan(&stored); err != nil || strings.Contains(allowData, stored) || strings.Contains(stored, strings.Split(allowData, ":")[1]) {
		t.Fatalf("stored nonce = %q, %v", stored, err)
	}
	// The next tick sends nothing new.
	s.chatBridgeApprovalTick(ctx, fakeBridgeKind)
	if len(fake.buttons) != 1 {
		t.Fatalf("pushed twice: %+v", fake.buttons)
	}

	// Allow, pressed through the poller like a real update.
	press := func(data, chat, messageID string) {
		t.Helper()
		fake.updates = []chatbridge.Update{{ID: time.Now().UnixNano(), ChatID: chat, ChatType: chatbridge.ChatTypePrivate, UserID: "7", UserName: "op", CallbackID: "cb", CallbackData: data, MessageID: messageID}}
		s.chatBridges.step(ctx, fakeBridgeKind)
	}
	press(allowData, "100", msg.messageID)
	if p, _ := pg.GetInterceptPending(first); p == nil || p.Status != "allowed" {
		t.Fatalf("pending after Allow = %+v", p)
	}
	if last := fake.toasts[len(fake.toasts)-1]; !strings.Contains(last, "Allowed by @op") {
		t.Fatalf("toast = %q", last)
	}
	if edit := fake.edits[msg.messageID]; !strings.HasPrefix(edit, msg.text) || !strings.Contains(edit, "Allowed by @op") {
		t.Fatalf("edit = %q", edit)
	}
	// The nonce works once: Block on the same message does nothing.
	press(blockData, "100", msg.messageID)
	if last := fake.toasts[len(fake.toasts)-1]; !strings.Contains(last, "no longer works") {
		t.Fatalf("second press toast = %q", last)
	}
	if p, _ := pg.GetInterceptPending(first); p.Status != "allowed" {
		t.Fatalf("second press changed the decision: %+v", p)
	}

	// A second request: its buttons are bound to their chat and message.
	second := newPending()
	s.chatBridgeApprovalTick(ctx, fakeBridgeKind)
	if len(fake.buttons) != 2 {
		t.Fatalf("second push = %+v", fake.buttons)
	}
	msg2 := fake.buttons[1]
	reply := s.bridgeApprovalPress(ctx, chatbridge.Request{Kind: fakeBridgeKind, ChatID: "-100", ChatType: "supergroup", UserID: "7", Args: msg2.rows[0][0].Data, MessageID: msg2.messageID})
	if !strings.Contains(reply.Toast, "no longer works") {
		t.Fatalf("press from another chat = %+v", reply)
	}
	reply = s.bridgeApprovalPress(ctx, chatbridge.Request{Kind: fakeBridgeKind, ChatID: "100", ChatType: chatbridge.ChatTypePrivate, UserID: "7", Args: msg2.rows[0][0].Data, MessageID: msg.messageID})
	if !strings.Contains(reply.Toast, "no longer works") {
		t.Fatalf("press on another message = %+v", reply)
	}
	if p, _ := pg.GetInterceptPending(second); p.Status != "pending" {
		t.Fatalf("a refused press decided the request: %+v", p)
	}
	// Decided in the web UI: the next tick removes the buttons.
	if err := s.m.interceptor.Decide(second, false, "admin", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	s.chatBridgeApprovalTick(ctx, fakeBridgeKind)
	if edit := fake.edits[msg2.messageID]; !strings.Contains(edit, "Decided in ARTEX: blocked") {
		t.Fatalf("edit after a web decision = %q", edit)
	}
	press(msg2.rows[0][0].Data, "100", msg2.messageID)
	if last := fake.toasts[len(fake.toasts)-1]; !strings.Contains(last, "no longer works") {
		t.Fatalf("press after a web decision = %q", last)
	}

	// With excerpts on, the request is shown, filtered.
	setBridgeShareText(t, s, true)
	newPending()
	s.chatBridgeApprovalTick(ctx, fakeBridgeKind)
	text := fake.buttons[len(fake.buttons)-1].text
	if !strings.Contains(text, "Request: ") || !strings.Contains(text, "cat /etc/shadow") || strings.Contains(text, "Demo-Secret-4242") || !strings.Contains(text, "Reason: rule says ask") {
		t.Fatalf("push with excerpts = %q", text)
	}
}

func TestParseApprovalData(t *testing.T) {
	nonce, err := newApprovalNonce()
	if err != nil {
		t.Fatal(err)
	}
	for _, allow := range []bool{true, false} {
		n, a, ok := parseApprovalData(approvalData(nonce, allow))
		if !ok || n != nonce || a != allow {
			t.Fatalf("round trip %v = %q %v %v", allow, n, a, ok)
		}
	}
	for _, bad := range []string{"", "ap", "ap:" + nonce, "ap:" + nonce + ":x", "xx:" + nonce + ":a", "ap:short:a", "ap:" + nonce + ":a:extra"} {
		if _, _, ok := parseApprovalData(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
