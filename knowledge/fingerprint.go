package knowledge

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// This file answers the "what is this thing" questions a probe result raises:
// which WAF is in front, which technologies the response reveals, and what a
// bare port or CVE id means.

// ---- WAF identification ----------------------------------------------------

// WAFVerdict is one candidate WAF with the strongest evidence that produced it.
type WAFVerdict struct {
	Name       string   // e.g. "Cloudflare"
	Confidence float64  // 0..1, the highest single-signature confidence
	Evidence   []string // human-readable reasons, sorted
}

// IdentifyWAF fingerprints the WAF in front of a response.
//
// Matching is evidence-based, never definitive: a header signature matches when
// the header is present and, if it carries a pattern, when the pattern matches a
// value; a body signature matches a regex against the body. A block status code
// with no signature match yields a low-confidence "Generic WAF" verdict at the
// corpus's own status_code_confidence, so a 403 alone never asserts a vendor.
//
// Results are sorted by confidence, highest first, with ties broken by name so
// the order is stable across runs.
func (s *Set) IdentifyWAF(h http.Header, body []byte, status int) []WAFVerdict {
	if s == nil {
		return nil
	}
	type acc struct {
		conf     float64
		evidence []string
	}
	found := map[string]*acc{}
	add := func(name string, conf float64, why string) {
		a := found[name]
		if a == nil {
			a = &acc{}
			found[name] = a
		}
		if conf > a.conf {
			a.conf = conf
		}
		for _, e := range a.evidence {
			if e == why {
				return
			}
		}
		a.evidence = append(a.evidence, why)
	}

	for _, sig := range s.WAF.HeaderSignatures {
		vals := headerValues(h, sig.Header)
		if len(vals) == 0 {
			continue
		}
		if sig.Pattern == "" {
			// Presence-only signature: the header name itself is the evidence.
			add(sig.WAF, sig.Confidence, "header "+sig.Header+" present")
			continue
		}
		re, err := compiled("(?i)" + sig.Pattern)
		if err != nil {
			continue
		}
		for _, v := range vals {
			if re.MatchString(v) {
				add(sig.WAF, sig.Confidence, "header "+sig.Header+" matches /"+sig.Pattern+"/")
				break
			}
		}
	}

	if len(body) > 0 {
		for _, sig := range s.WAF.BodySignatures {
			re, err := compiled("(?i)" + sig.Pattern)
			if err != nil {
				continue
			}
			if re.Match(body) {
				add(sig.WAF, sig.Confidence, "body matches /"+sig.Pattern+"/")
			}
		}
	}

	if len(found) == 0 && s.isBlockStatus(status) {
		add("Generic WAF", s.WAF.StatusCodeConfidence, "block status code "+strconv.Itoa(status))
	}

	out := make([]WAFVerdict, 0, len(found))
	for name, a := range found {
		sort.Strings(a.evidence)
		out = append(out, WAFVerdict{Name: name, Confidence: a.conf, Evidence: a.evidence})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// isBlockStatus reports whether the corpus treats a status code as a block.
func (s *Set) isBlockStatus(status int) bool {
	for _, c := range s.WAF.BlockStatusCodes {
		if c == status {
			return true
		}
	}
	return false
}

// headerValues looks a header up case-insensitively and splits comma lists, so a
// signature naming "set-cookie" sees each cookie and one naming "x-powered-by"
// sees "PHP/8.2" rather than the whole joined value.
func headerValues(h http.Header, name string) []string {
	if h == nil {
		return nil
	}
	var out []string
	for k, vs := range h {
		if !strings.EqualFold(k, name) {
			continue
		}
		for _, v := range vs {
			for _, part := range strings.Split(v, ",") {
				if p := strings.TrimSpace(part); p != "" {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// ---- technology fingerprinting ---------------------------------------------

// TechHit is one technology the corpus knows, with the evidence that suggested
// it.
type TechHit struct {
	Name     string
	Category string
	Severity string
	Evidence string
}

// bodyKeyMinLen is the shortest technology key allowed to match on body text.
// Short keys ("php", "iis", "aws") collide with ordinary words, so a body match
// requires a key long enough to be a distinct token; headers and the URL have no
// such restriction because they are machine-shaped.
const bodyKeyMinLen = 4

// FingerprintTech derives technologies from response headers, body and URL.
//
// The corpus keys are vendor names, so matching stays conservative: a key counts
// when it appears as a whole token in the URL, or anywhere in a header name or
// value, or - for keys of at least four characters - in the body. Results are
// hints for the caller to verify, not assertions.
func (s *Set) FingerprintTech(h http.Header, body []byte, rawURL string) []TechHit {
	if s == nil {
		return nil
	}
	lowerURL := strings.ToLower(rawURL)
	lowerBody := strings.ToLower(string(body))

	out := make([]TechHit, 0, 4)
	for name, profile := range s.Tech {
		key := strings.ToLower(name)
		if key == "" {
			continue
		}
		evidence := ""
		switch {
		case containsToken(lowerURL, key):
			evidence = "url contains " + key
		case headerMentions(h, key):
			evidence = "response header mentions " + key
		case len(key) >= bodyKeyMinLen && strings.Contains(lowerBody, key):
			evidence = "body mentions " + key
		}
		if evidence == "" {
			continue
		}
		out = append(out, TechHit{
			Name:     name,
			Category: profile.Category,
			Severity: profile.Severity,
			Evidence: evidence,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// containsToken reports whether token appears in s delimited by non-word bytes,
// so "php" matches "php/8.2" but not "phpxmlrpc".
func containsToken(s, token string) bool {
	if token == "" {
		return false
	}
	for i := 0; i <= len(s)-len(token); {
		j := strings.Index(s[i:], token)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(token)
		beforeOK := start == 0 || !isWordByte(s[start-1])
		afterOK := end == len(s) || !isWordByte(s[end])
		if beforeOK && afterOK {
			return true
		}
		i = start + 1
	}
	return false
}

// headerMentions reports whether any header name or value contains token.
// Substring matching is right here because banners embed the product name
// ("Apache-Coyote/1.1") and header names are stable machine tokens
// ("X-Jenkins", "X-Powered-By").
func headerMentions(h http.Header, token string) bool {
	if h == nil {
		return false
	}
	for k, vs := range h {
		if strings.Contains(strings.ToLower(k), token) {
			return true
		}
		for _, v := range vs {
			if strings.Contains(strings.ToLower(v), token) {
				return true
			}
		}
	}
	return false
}

// TechPaths returns the well-known paths the corpus associates with a
// technology, so a caller can probe them.
func (s *Set) TechPaths(tech string) []string {
	if s == nil {
		return nil
	}
	p, ok := s.Tech[strings.ToLower(strings.TrimSpace(tech))]
	if !ok {
		return nil
	}
	return append([]string(nil), p.Paths...)
}

// ---- port correlations -----------------------------------------------------

// PortInfo is what the corpus says about one port.
type PortInfo struct {
	Port     string
	Service  string
	Severity string
	Notes    string
}

// Port returns the corpus entry for a port number.
func (s *Set) Port(port int) (PortInfo, bool) {
	if s == nil || port < 1 || port > 65535 {
		return PortInfo{}, false
	}
	key := strconv.Itoa(port)
	p, ok := s.Ports[key]
	if !ok {
		return PortInfo{}, false
	}
	return PortInfo{Port: key, Service: p.Service, Severity: p.Severity, Notes: p.Notes}, true
}

// ---- CVE correlations ------------------------------------------------------

// CVERecord is one CVE as the corpus describes it.
type CVERecord struct {
	ID          string
	Name        string
	Product     string
	Severity    string
	Description string
	Targets     []string
}

// CVE returns one record by id. The lookup is case-insensitive because agent
// output spells ids both ways.
func (s *Set) CVE(id string) (CVERecord, bool) {
	if s == nil {
		return CVERecord{}, false
	}
	want := strings.ToUpper(strings.TrimSpace(id))
	if want == "" {
		return CVERecord{}, false
	}
	for k, v := range s.CVEs {
		if strings.ToUpper(k) != want {
			continue
		}
		return CVERecord{
			ID:          k,
			Name:        v.Name,
			Product:     v.Product,
			Severity:    v.Severity,
			Description: v.Description,
			Targets:     append([]string(nil), v.Targets...),
		}, true
	}
	return CVERecord{}, false
}

// CVEsForTech returns the CVEs whose product or targets mention a technology, so
// a detected banner can surface the relevant known issues. Results are ordered
// by CVE id for stable output.
func (s *Set) CVEsForTech(tech string) []CVERecord {
	if s == nil {
		return nil
	}
	needle := strings.ToLower(strings.TrimSpace(tech))
	if needle == "" {
		return nil
	}
	var out []CVERecord
	for k, v := range s.CVEs {
		match := strings.Contains(strings.ToLower(v.Product), needle)
		if !match {
			for _, t := range v.Targets {
				if strings.Contains(strings.ToLower(t), needle) {
					match = true
					break
				}
			}
		}
		if !match {
			continue
		}
		out = append(out, CVERecord{
			ID:          k,
			Name:        v.Name,
			Product:     v.Product,
			Severity:    v.Severity,
			Description: v.Description,
			Targets:     append([]string(nil), v.Targets...),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// isWordByte reports whether b can continue an identifier-ish token, so
// containsToken treats "php/8.2" as two tokens but "x-php" as one.
func isWordByte(b byte) bool {
	return b == '_' || b == '-' || b == '.' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}
