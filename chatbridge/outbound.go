package chatbridge

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// A chat app is an external data surface: messages leave the deployment,
// sit on the chat provider's servers and on every member's phone. What the
// bridge sends is therefore fixed wording, titles, statuses, counts and links
// by default (issue #5). Free-form text, such as a main-agent answer, goes
// out only when the operator turns on excerpts for the bridge, and then only
// through SafeText. SafeText is deliberately blunt:
//
//   - Code is dropped: fenced blocks (``` and ~~~) and indented blocks.
//     Agents put requests, responses, tool output and payloads there, which is
//     exactly the evidence that stays in ARTEX.
//   - Known secrets passed by the caller (the task's stored credentials) are
//     replaced wherever they appear.
//   - Common credential shapes are masked: private key blocks, credentials in
//     URLs, Authorization and Cookie headers, bearer tokens, JWTs, well-known
//     API key formats, curl -u, session cookies and key=value pairs whose key
//     names a password, secret, token or key, quoted values included.
//
// Pattern masking cannot know every shape a credential takes. That is why
// excerpts are an opt-in and the full text stays in ARTEX.

// Redacted replaces a masked value.
const Redacted = "[redacted]"

// CodeOmitted replaces a dropped code block.
const CodeOmitted = "[code block omitted, open ARTEX to see it]"

const secretKey = `[a-z0-9_.-]*(?:password|passwd|passphrase|pwd|secret|token|api[_-]?key|apikey|access[_-]?key|private[_-]?key|client[_-]?secret|credential|sessid|session[_-]?id|auth)[a-z0-9_.-]*`

var (
	fencedCodeRe   = regexp.MustCompile("(?s)(```|~~~).*?(```|~~~|$)")
	indentedCodeRe = regexp.MustCompile(`(?m)(?:^(?: {4}|\t)[^\n]*(?:\n|$))+`)
	privateKeyRe   = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`)
	urlUserinfoRe  = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^\s/?#@]+@`)
	authHeaderRe   = regexp.MustCompile(`(?im)\b((?:proxy-)?authorization|cookie|set-cookie|x-api-key|x-auth-token)(\s*:\s*)[^\r\n]+`)
	bearerRe       = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[A-Za-z0-9._~+/=-]{8,}`)
	curlUserRe     = regexp.MustCompile(`(?i)(\s(?:-u|--user|--proxy-user)(?:\s+|=))\S+`)
	jwtRe          = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`)
	apiKeyRe       = regexp.MustCompile(`\b(?:(?:AKIA|ASIA)[0-9A-Z]{16}|(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[abposr]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,})`)
	sessionIDRe    = regexp.MustCompile(`(?i)\b((?:[a-z0-9_-]+\.)?sid|[a-z0-9_]*sess[a-z0-9_]*)(\s*=\s*)[^\s"',;&]+`)
	secretKVRe     = regexp.MustCompile(`(?i)\b(` + secretKey + `)(["']?\s*[:=]\s*)("[^"\n]*"|'[^'\n]*'|[^\s"',;&]+)`)
)

// SafeText prepares untrusted text for a chat message: it drops code, masks
// secrets and cuts the result to maxRunes runes (0 = no cut), marking a cut
// with an ellipsis.
func SafeText(text string, secrets []string, maxRunes int) string {
	out := strings.ReplaceAll(text, "\r\n", "\n")
	out = fencedCodeRe.ReplaceAllString(out, CodeOmitted)
	out = indentedCodeRe.ReplaceAllString(out, CodeOmitted+"\n")
	out = privateKeyRe.ReplaceAllString(out, Redacted)
	for _, s := range secrets {
		// Very short values (a PIN, "admin") would mask ordinary words.
		if s = strings.TrimSpace(s); utf8.RuneCountInString(s) >= 4 {
			out = strings.ReplaceAll(out, s, Redacted)
		}
	}
	out = urlUserinfoRe.ReplaceAllString(out, "${1}"+Redacted+"@")
	out = authHeaderRe.ReplaceAllString(out, "${1}${2}"+Redacted)
	out = bearerRe.ReplaceAllString(out, "${1}${2}"+Redacted)
	out = curlUserRe.ReplaceAllString(out, "${1}"+Redacted)
	out = jwtRe.ReplaceAllString(out, Redacted)
	out = apiKeyRe.ReplaceAllString(out, Redacted)
	out = sessionIDRe.ReplaceAllString(out, "${1}${2}"+Redacted)
	out = secretKVRe.ReplaceAllString(out, "${1}${2}"+Redacted)
	out = strings.TrimSpace(out)
	if maxRunes > 0 && utf8.RuneCountInString(out) > maxRunes {
		r := []rune(out)
		out = strings.TrimSpace(string(r[:maxRunes-1])) + "…"
	}
	return out
}
