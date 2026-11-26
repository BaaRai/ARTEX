package chatbridge

import (
	"context"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func testRouter() *Router {
	return &Router{
		BotUserName: "artex_bot",
		Now:         func() time.Time { return testNow },
		Commands: []Command{{
			Name: "echo", Usage: "<text>", Summary: "repeat the text",
			Run: func(_ context.Context, req Request) string { return "echo:" + req.Args + ":" + req.UserName },
		}},
	}
}

func msg(chat, text string) Update {
	return Update{ID: 1, ChatID: chat, UserID: "7", UserName: "op", Text: text, Date: testNow.Add(-time.Second)}
}

func TestRouteAllowedCommand(t *testing.T) {
	r := testRouter()
	d, reply := r.Route(context.Background(), KindTelegram, []string{"100"}, msg("100", "/echo  hello world "))
	if d != DecisionHandled || reply != "echo:hello world:op" {
		t.Fatalf("got %v %q", d, reply)
	}
}

func TestRouteCommandAddressedToThisOrOtherBot(t *testing.T) {
	r := testRouter()
	if d, reply := r.Route(context.Background(), KindTelegram, []string{"100"}, msg("100", "/ECHO@Artex_Bot x")); d != DecisionHandled || !strings.HasPrefix(reply, "echo:x") {
		t.Fatalf("own bot: got %v %q", d, reply)
	}
	if d, reply := r.Route(context.Background(), KindTelegram, []string{"100"}, msg("100", "/echo@other_bot x")); d != DecisionIgnored || reply != "" {
		t.Fatalf("other bot: got %v %q", d, reply)
	}
}

func TestRouteUnknownChatRefusedOnceThenMuted(t *testing.T) {
	r := testRouter()
	d, reply := r.Route(context.Background(), KindTelegram, []string{"100"}, msg("555", "/echo hi"))
	if d != DecisionRefused || !strings.Contains(reply, "555") {
		t.Fatalf("first contact: got %v %q", d, reply)
	}
	if strings.Contains(reply, "echo") {
		t.Fatalf("refusal must not run the command: %q", reply)
	}
	for i := 0; i < 3; i++ {
		if d, reply := r.Route(context.Background(), KindTelegram, []string{"100"}, msg("555", "hello?")); d != DecisionMuted || reply != "" {
			t.Fatalf("repeat %d: got %v %q", i, d, reply)
		}
	}
}

func TestRouteEmptyAllowlistRefusesEveryone(t *testing.T) {
	r := testRouter()
	if d, _ := r.Route(context.Background(), KindTelegram, nil, msg("100", "/help")); d != DecisionRefused {
		t.Fatalf("got %v, want refused", d)
	}
}

func TestRouteRefusalMemoryIsBounded(t *testing.T) {
	r := testRouter()
	for i := 0; i < maxRefusedChats; i++ {
		r.Route(context.Background(), KindTelegram, nil, msg("c"+itoa(i), "hi"))
	}
	// Past the cap the memory starts over: a new chat (it may be the
	// operator's own) still gets its refusal with the chat ID.
	if d, reply := r.Route(context.Background(), KindTelegram, nil, msg("brand-new", "hi")); d != DecisionRefused || !strings.Contains(reply, "brand-new") {
		t.Fatalf("over the cap: got %v %q", d, reply)
	}
	if len(r.refused) > maxRefusedChats {
		t.Fatalf("refused set grew to %d", len(r.refused))
	}
	if d, _ := r.Route(context.Background(), KindTelegram, nil, msg("brand-new", "again")); d != DecisionMuted {
		t.Fatalf("after the reset: got %v, want muted", d)
	}
}

func TestRouterKeepRefusals(t *testing.T) {
	prev := testRouter()
	prev.Route(context.Background(), KindTelegram, nil, msg("555", "hi"))
	next := testRouter()
	next.KeepRefusals(prev)
	if d, reply := next.Route(context.Background(), KindTelegram, nil, msg("555", "hi")); d != DecisionMuted || reply != "" {
		t.Fatalf("carried refusal: got %v %q", d, reply)
	}
	if d, _ := next.Route(context.Background(), KindTelegram, nil, msg("556", "hi")); d != DecisionRefused {
		t.Fatalf("new chat: got %v", d)
	}
	if d, _ := prev.Route(context.Background(), KindTelegram, nil, msg("556", "hi")); d != DecisionRefused {
		t.Fatalf("the previous router must not share the new memory: got %v", d)
	}
}

func TestRouteDropsStaleAndBotAndEmpty(t *testing.T) {
	r := testRouter()
	old := msg("100", "/echo late")
	old.Date = testNow.Add(-MaxUpdateAge - time.Second)
	if d, reply := r.Route(context.Background(), KindTelegram, []string{"100"}, old); d != DecisionStale || reply != "" {
		t.Fatalf("stale: got %v %q", d, reply)
	}
	// A stale message from an unknown chat is dropped before the refusal, so
	// a backlog never produces a burst of refusals.
	old.ChatID = "999"
	if d, _ := r.Route(context.Background(), KindTelegram, []string{"100"}, old); d != DecisionStale {
		t.Fatalf("stale unknown chat: got %v", d)
	}
	b := msg("100", "/echo x")
	b.FromBot = true
	if d, _ := r.Route(context.Background(), KindTelegram, []string{"100"}, b); d != DecisionIgnored {
		t.Fatalf("bot sender: got %v", d)
	}
	if d, _ := r.Route(context.Background(), KindTelegram, []string{"100"}, msg("100", "   ")); d != DecisionIgnored {
		t.Fatalf("empty text: got %v", d)
	}
}

func TestRouteHelpAndUnknownAndPlainText(t *testing.T) {
	r := testRouter()
	_, help := r.Route(context.Background(), KindTelegram, []string{"100"}, msg("100", "/start"))
	if !strings.Contains(help, "/echo <text> - repeat the text") || !strings.Contains(help, "/help") {
		t.Fatalf("help = %q", help)
	}
	if _, reply := r.Route(context.Background(), KindTelegram, []string{"100"}, msg("100", "/nope")); !strings.Contains(reply, "Unknown command /nope") {
		t.Fatalf("unknown = %q", reply)
	}
	if _, reply := r.Route(context.Background(), KindTelegram, []string{"100"}, msg("100", "status please")); !strings.Contains(reply, "/help") {
		t.Fatalf("plain text without handler = %q", reply)
	}
	r.Text = func(_ context.Context, req Request) string { return "text:" + req.Args }
	if _, reply := r.Route(context.Background(), KindTelegram, []string{"100"}, msg("100", "status please")); reply != "text:status please" {
		t.Fatalf("plain text handler = %q", reply)
	}
	if _, reply := r.Route(context.Background(), KindTelegram, []string{"100"}, msg("100", "/")); reply != "text:/" {
		t.Fatalf("bare slash = %q", reply)
	}
}

func TestNormalizeAllowedChats(t *testing.T) {
	tr, _ := Get(KindTelegram)
	got, err := NormalizeAllowedChats(tr, []string{" 123 ", "", "-1001234567890", "123"})
	if err != nil || strings.Join(got, ",") != "123,-1001234567890" {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := NormalizeAllowedChats(tr, []string{"@channel"}); err == nil {
		t.Fatal("a username must be refused: updates carry numeric ids")
	}
	many := make([]string, MaxAllowedChats+1)
	for i := range many {
		many[i] = itoa(i + 1)
	}
	if _, err := NormalizeAllowedChats(tr, many); err == nil {
		t.Fatal("over the cap must be refused")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}
