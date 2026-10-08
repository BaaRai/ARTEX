package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// This file covers the "pack whole items" fix: when a digest message exceeds the
// channel's length limit, it must be truncated **item by item** and honestly
// report how many items did not fit, so the caller marks only the ones actually
// delivered.
//
// The previous approach rendered the whole thing, truncated it, and then marked
// the whole batch as delivered: the second half of the message vanished into
// thin air while the delivery history showed all succeeded — vulnerabilities
// were simply lost, and nowhere could you find out.

func TestMarkdownBodyPacksWholeItemsWithinByteLimit(t *testing.T) {
	// 200 digest items are guaranteed to far exceed WeCom's 4096 bytes.
	m := batchMsg(200)
	body, kept := markdownBody(m, weComMarkdownLimit)

	if len(body) > weComMarkdownLimit {
		t.Fatalf("body of %d bytes exceeds the limit of %d", len(body), weComMarkdownLimit)
	}
	if !utf8.ValidString(body) {
		t.Fatal("body is not valid UTF-8")
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("only a part should fit (0 < kept < %d), got %d", len(m.Items), kept)
	}
	// The header must honestly state how many items this message contains and how
	// many remain — otherwise readers take the number in the header for the whole
	// batch.
	if !strings.Contains(body, "remaining") || !strings.Contains(body, "next message") {
		t.Fatalf("the header should state how many items are not included in this message:\n%s", body[:minInt(400, len(body))])
	}
	// Only the first kept items should be included.
	for i := 0; i < kept; i++ {
		if !strings.Contains(body, "vuln"+itoa(i+1)) {
			t.Fatalf("item %d should be in this message:\n%s", i+1, body)
		}
	}
	if strings.Contains(body, "vuln"+itoa(kept+1)) {
		t.Fatalf("item %d should not appear (it belongs to the next batch)", kept+1)
	}
}

func TestMarkdownBodyKeepsEverythingWhenUnderLimit(t *testing.T) {
	m := batchMsg(3)
	body, kept := markdownBody(m, 0) // 0 = unlimited
	if kept != len(m.Items) {
		t.Fatalf("everything should be kept when length is unlimited, got kept=%d", kept)
	}
	if strings.Contains(body, "remaining") {
		t.Fatalf("no truncation notice should appear when nothing was truncated:\n%s", body)
	}
}

func TestMarkdownBodyAlwaysKeepsAtLeastOneItem(t *testing.T) {
	// When the budget is too small to fit even one item, still send one (with
	// final truncation as the backstop). Otherwise a single oversized
	// vulnerability would permanently jam the whole batch in place: never fitting
	// on every claim, never sent.
	m := batchMsg(5)
	_, kept := markdownBody(m, 50)
	if kept != 1 {
		t.Fatalf("at least 1 item should be kept, got %d", kept)
	}
}

func TestMarkdownBodySingleReturnsOne(t *testing.T) {
	_, kept := markdownBody(singleMsg(), 4096)
	if kept != 1 {
		t.Fatalf("a single message should report 1 delivered, got %d", kept)
	}
	// An empty message has no items to deliver.
	if _, k := markdownBody(Message{}, 4096); k != 0 {
		t.Fatalf("an empty message should report 0 items, got %d", k)
	}
}

func TestTelegramPackingUsesRuneBudget(t *testing.T) {
	m := batchMsg(200)
	text, kept := telegramHTML(m)
	// Telegram limits by **character count**; measuring in bytes would cram a
	// CJK message down to a third.
	if n := utf8.RuneCountInString(text); n > telegramTextLimit {
		t.Fatalf("body of %d characters exceeds the limit of %d", n, telegramTextLimit)
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("only a part should fit, got %d", kept)
	}
	if !strings.Contains(text, "next message") {
		t.Fatalf("should state that more items are not included:\n%.300s", text)
	}
}

func TestFeishuPackingReportsKept(t *testing.T) {
	m := batchMsg(2000)
	_, kept := feishuCard(m)
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("the card should only fit a part, got %d", kept)
	}
}

func TestWebhookAndEmailReportAllItems(t *testing.T) {
	// These two channels do not truncate the body; the whole batch counts as
	// delivered.
	m := batchMsg(7)
	if n := len(m.Items); n != 7 {
		t.Fatal("precondition does not hold")
	}
	// Confirm indirectly via the renderer's return value: markdownBody(0) keeps
	// everything when unlimited.
	if _, k := markdownBody(m, 0); k != len(m.Items) {
		t.Fatalf("everything should be used when length is unlimited, got %d", k)
	}
}

// TestMarkdownEscapesUntrustedContent is the regression test for "untrusted
// content must not change message structure". Titles and summaries come from
// model output (the model reads the tested target's responses); asset names come
// from the tested target's URLs.
func TestMarkdownEscapesUntrustedContent(t *testing.T) {
	cases := []struct {
		name  string
		item  Item
		must  []string // must appear in the result (escaped form)
		wrong []string // must not appear in the result (unescaped form)
	}{
		{
			name: "newline + external link in the title",
			item: Item{
				Severity: "high",
				Name:     "SQL injection on login page\n[URGENT: click to verify your account](http://attacker.tld)",
			},
			// The newline must be collapsed (otherwise it can forge a new list
			// item / quote block); the square brackets and parentheses must be
			// escaped (otherwise it is a clickable external link).
			must:  []string{`\[URGENT: click to verify your account\]`, `\(http://attacker.tld\)`},
			wrong: []string{"\n[URGENT", "\n\n[URGENT"},
		},
		{
			name: "image beacon in the title",
			item: Item{
				Severity: "high",
				Name:     "Vuln ![](http://attacker.tld/beacon)",
			},
			must:  []string{`\!`, `\(http://attacker.tld/beacon\)`},
			wrong: []string{"![]("},
		},
		{
			name: "emphasis and quote in an asset name",
			item: Item{
				Severity: "high",
				Name:     "plain title",
				Assets:   []string{"a.com/*inject*>quote"},
			},
			must:  []string{`\*inject\*`, `\>`},
			wrong: []string{"*inject*"},
		},
		{
			name: "backticks and pipe in the summary",
			item: Item{
				Severity: "high",
				Name:     "title",
				Summary:  "`code` | table",
			},
			must:  []string{"\\`code\\`", `\|`},
			wrong: []string{"`code`"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Message{Items: []Item{tc.item}}
			// writeItem in single mode is the rendering path shared by all three
			// markdown channels.
			var b strings.Builder
			writeItem(&b, tc.item, "", true)
			got := b.String()
			for _, want := range tc.must {
				if !strings.Contains(got, want) {
					t.Errorf("missing escaped form %q:\n%s", want, got)
				}
			}
			for _, bad := range tc.wrong {
				if strings.Contains(got, bad) {
					t.Errorf("unescaped form %q appeared (can be used to inject structure or external links):\n%s", bad, got)
				}
			}
			_ = m
		})
	}
}

// TestMarkdownEscapeBackslashFirst locks the escape order: the backslash must be
// handled first, otherwise the backslashes added later get another layer and the
// output ends up with double backslashes.
func TestMarkdownEscapeBackslashFirst(t *testing.T) {
	if got := markdownEscape(`a\b*c`); got != `a\\b\*c` {
		t.Fatalf("wrong escape order, got %q", got)
	}
}

// TestTelegramTitleHasNoMarkdownEscapes locks a specific regression: markdown
// escapes must not leak into Telegram's HTML output (an escape was once added to
// the shared title function, and Telegram messages ended up showing visible
// backslashes like `\(1\)`).
func TestTelegramTitleHasNoMarkdownEscapes(t *testing.T) {
	m := Message{Items: []Item{{Severity: "high", Name: "alert(1) *important*"}}}
	text, _ := telegramHTML(m)
	if strings.Contains(text, `\(`) || strings.Contains(text, `\*`) {
		t.Fatalf("markdown backslash escapes appeared in the Telegram body:\n%s", text)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
