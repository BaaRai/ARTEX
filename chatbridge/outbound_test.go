package chatbridge

import (
	"strings"
	"testing"
)

func TestSafeTextDropsCodeAndMasksSecrets(t *testing.T) {
	in := strings.Join([]string{
		"Found SQL injection on /login.",
		"```http",
		"POST /login HTTP/1.1",
		"Cookie: sid=abc123def456",
		"```",
		"Logged in as admin with password=Hunter2Hunter2 and the stored one s3cr3t-Value.",
		"Authorization: Bearer abcdefghijklmnop",
		"token: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.c2lnbmF0dXJlX3ZhbHVl",
		"aws AKIAABCDEFGHIJKLMNOP",
		"-----BEGIN RSA PRIVATE KEY-----",
		"MIIEowIBAAKCAQEA",
		"-----END RSA PRIVATE KEY-----",
		"Next: try the admin panel.",
	}, "\n")
	out := SafeText(in, []string{"s3cr3t-Value", "pin"}, 0)
	for _, leak := range []string{"abc123def456", "POST /login", "Hunter2Hunter2", "s3cr3t-Value", "abcdefghijklmnop", "eyJhbGci", "AKIAABCDEFGHIJKLMNOP", "MIIEowIBAAKCAQEA"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in:\n%s", leak, out)
		}
	}
	for _, keep := range []string{"Found SQL injection on /login.", CodeOmitted, "Logged in as admin with password=" + Redacted, "Authorization: " + Redacted, "Next: try the admin panel."} {
		if !strings.Contains(out, keep) {
			t.Errorf("missing %q in:\n%s", keep, out)
		}
	}
	// A short known secret does not mask ordinary words.
	if !strings.Contains(SafeText("ping the host", []string{"pin"}, 0), "ping") {
		t.Error("a 3-letter secret masked an ordinary word")
	}
}

func TestSafeTextUnclosedFenceAndCut(t *testing.T) {
	if out := SafeText("Summary first.\n```\nraw response body", nil, 0); strings.Contains(out, "raw response") {
		t.Fatalf("an unclosed code block leaked: %q", out)
	}
	out := SafeText(strings.Repeat("a", 50), nil, 10)
	if n := len([]rune(out)); n != 10 || !strings.HasSuffix(out, "…") {
		t.Fatalf("cut = %q (%d runes)", out, n)
	}
}
