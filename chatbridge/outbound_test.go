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

// The shapes from the #598 review, as agents write them in plain prose.
func TestSafeTextReviewCases(t *testing.T) {
	cases := []struct {
		name, in string
		leaks    []string
	}{
		{"quoted value with spaces", `"password": "correct horse battery"`, []string{"correct", "horse", "battery"}},
		{"session cookies", `PHPSESSID=8f2a9c1d77e04b and connect.sid=s%3Aabc123def`, []string{"8f2a9c1d77e04b", "abc123def"}},
		{"tilde fence", "Found it:\n~~~\nuser=admin&pw=Winter2026!\n~~~\ndone", []string{"Winter2026!", "user=admin"}},
		{"indented code", "Reproduce with:\n\n    curl -u admin:Winter2026! https://target.example/api\n\nThen check.", []string{"Winter2026!", "curl -u"}},
		{"curl -u in prose", "I ran curl -u admin:Winter2026! https://target.example/api", []string{"Winter2026!"}},
		{"api keys in prose", "keys sk-proj-AbCdEf1234567890XyZ and ghp_1234567890abcdefghijABCDEFGHIJ012345 work", []string{"AbCdEf1234567890XyZ", "ghp_1234567890"}},
		{"credentials in a URL", "connect to https://admin:Winter2026!@db.target.example:5432/prod now", []string{"Winter2026!", "admin:"}},
	}
	for _, c := range cases {
		out := SafeText(c.in, nil, 0)
		for _, leak := range c.leaks {
			if strings.Contains(out, leak) {
				t.Errorf("%s: %q leaked in %q", c.name, leak, out)
			}
		}
	}
	// Ordinary prose is left alone.
	for _, keep := range []string{"The inside of the session looked fine.", "Run the assessment again tomorrow.", "We used 1200 tokens today.", "Visit https://target.example/login next."} {
		if out := SafeText(keep, nil, 0); out != keep {
			t.Errorf("ordinary text changed: %q -> %q", keep, out)
		}
	}
}
