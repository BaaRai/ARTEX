package chatbridge

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// A chat app is an external data surface: messages leave the deployment,
// sit on the chat provider's servers and on every member's phone. What the
// bridge relays from ARTEX is therefore a summary with a link back, never
// evidence or a credential (issue #5). SafeText is the last filter on text
// that ARTEX did not write itself, such as a main-agent answer. It is
// deliberately blunt:
//
//   - Fenced code blocks are dropped. Agents put requests, responses, tool
//     output and payloads in them, which is exactly the evidence that stays in
//     ARTEX.
//   - Known secrets passed by the caller (the task's stored credentials) are
//     replaced wherever they appear.
//   - Common credential shapes are masked: private key blocks, Authorization
//     and Cookie headers, bearer tokens, JWTs, AWS access key ids and
//     key=value pairs whose key names a password, secret, token or API key.
//
// Pattern masking is best effort; the full answer stays readable in ARTEX.

// Redacted replaces a masked value.
const Redacted = "[redacted]"

// CodeOmitted replaces a dropped code block.
const CodeOmitted = "[code block omitted, open ARTEX to see it]"

var (
	fencedCodeRe = regexp.MustCompile("(?s)```.*?(```|$)")
	privateKeyRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`)
	authHeaderRe = regexp.MustCompile(`(?im)\b((?:proxy-)?authorization|cookie|set-cookie|x-api-key)(\s*:\s*)[^\r\n]+`)
	bearerRe     = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[A-Za-z0-9._~+/=-]{8,}`)
	jwtRe        = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`)
	awsKeyRe     = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)
	secretKVRe   = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:password|passwd|pwd|secret|token|api[_-]?key|apikey|access[_-]?key|private[_-]?key|session[_-]?id)[a-z0-9_.-]*)(["']?\s*[:=]\s*["']?)[^\s"',;&]+`)
)

// SafeText prepares untrusted text for a chat message: it drops code blocks,
// masks secrets and cuts the result to maxRunes runes (0 = no cut), marking a
// cut with an ellipsis.
func SafeText(text string, secrets []string, maxRunes int) string {
	out := strings.ReplaceAll(text, "\r\n", "\n")
	out = fencedCodeRe.ReplaceAllString(out, CodeOmitted)
	out = privateKeyRe.ReplaceAllString(out, Redacted)
	for _, s := range secrets {
		// Very short values (a PIN, "admin") would mask ordinary words.
		if s = strings.TrimSpace(s); utf8.RuneCountInString(s) >= 4 {
			out = strings.ReplaceAll(out, s, Redacted)
		}
	}
	out = authHeaderRe.ReplaceAllString(out, "${1}${2}"+Redacted)
	out = bearerRe.ReplaceAllString(out, "${1}${2}"+Redacted)
	out = jwtRe.ReplaceAllString(out, Redacted)
	out = awsKeyRe.ReplaceAllString(out, Redacted)
	out = secretKVRe.ReplaceAllString(out, "${1}${2}"+Redacted)
	out = strings.TrimSpace(out)
	if maxRunes > 0 && utf8.RuneCountInString(out) > maxRunes {
		r := []rune(out)
		out = strings.TrimSpace(string(r[:maxRunes-1])) + "…"
	}
	return out
}
