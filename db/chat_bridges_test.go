package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestChatBridgeSaveSealedAndOffset(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	const kind = "t-bridge"
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM chat_bridges WHERE kind=$1`, kind) })

	if _, err := d.ChatBridgeByKind(ctx, kind); !errors.Is(err, ErrChatBridgeNotFound) {
		t.Fatalf("missing row: err = %v", err)
	}

	const token = "123456789:AAsecret-bot-token-value"
	b, err := d.SaveChatBridge(ctx, &ChatBridge{
		Kind:         kind,
		Enabled:      true,
		Config:       json.RawMessage(`{"bot_token":"` + token + `"}`),
		AllowedChats: []string{"100", "-1001"},
	}, false)
	if err != nil {
		t.Fatalf("SaveChatBridge: %v", err)
	}
	if !b.Enabled || b.PollOffset != 0 || b.ConfigVersion != 1 || strings.Join(b.AllowedChats, ",") != "100,-1001" || !strings.Contains(string(b.Config), token) {
		t.Fatalf("saved = %+v (config %s)", b, b.Config)
	}

	// The token never sits in clear in the table.
	var raw string
	if err := d.QueryRow(`SELECT config::text FROM chat_bridges WHERE kind=$1`, kind).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "AAsecret") || !strings.Contains(raw, sealedJSONKey) {
		t.Fatalf("chat_bridges.config is not sealed at rest: %q", raw)
	}

	// The cursor only moves forward.
	if ok, err := d.SetChatBridgeOffset(ctx, kind, b.ConfigVersion, 42); err != nil || !ok {
		t.Fatalf("set 42: %v %v", ok, err)
	}
	if ok, err := d.SetChatBridgeOffset(ctx, kind, b.ConfigVersion, 7); err != nil || ok {
		t.Fatalf("rewind to 7 must be a no-op: %v %v", ok, err)
	}
	got, err := d.ChatBridgeByKind(ctx, kind)
	if err != nil || got.PollOffset != 42 || !got.UpdatedAt.Equal(b.UpdatedAt) {
		t.Fatalf("after cursor writes = %+v, %v; want offset 42 and updated_at unchanged", got, err)
	}

	// Saving again updates in place, keeps the cursor and bumps the version.
	b2, err := d.SaveChatBridge(ctx, &ChatBridge{Kind: kind, Enabled: false, Config: json.RawMessage(`{"bot_token":"` + token + `"}`)}, false)
	if err != nil {
		t.Fatal(err)
	}
	if b2.ID != b.ID || b2.Enabled || b2.PollOffset != 42 || b2.ConfigVersion != 2 || len(b2.AllowedChats) != 0 {
		t.Fatalf("update = %+v", b2)
	}

	// A poller still holding version 1 cannot move the cursor any more.
	if ok, err := d.SetChatBridgeOffset(ctx, kind, b.ConfigVersion, 99); err != nil || ok {
		t.Fatalf("stale version write must be a no-op: %v %v", ok, err)
	}

	// A token change clears the cursor in the same save.
	b3, err := d.SaveChatBridge(ctx, &ChatBridge{Kind: kind, Config: json.RawMessage(`{"bot_token":"987654321:AAother-bot-token-value"}`)}, true)
	if err != nil || b3.PollOffset != 0 || b3.ConfigVersion != 3 {
		t.Fatalf("reset save = %+v, %v", b3, err)
	}
}
