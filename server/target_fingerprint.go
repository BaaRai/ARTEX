package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
)

var (
	htmlTitleRe             = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	htmlCanonicalLinkRe     = regexp.MustCompile(`(?is)<link[^>]+rel=["']?canonical["']?[^>]+href=["']([^"'>\s]+)["']`)
	htmlLinkHrefRe          = regexp.MustCompile(`(?is)(?:href|src|action)=["']([^"'>\s]+)["']`)
	rawHTTPLinkRe           = regexp.MustCompile(`(?i)https?://[^\s"'<>\\)\]]+`)
	fingerprintCTFConsoleRe = regexp.MustCompile(`(?i)\b(ctf|capture\s+(?:the\s+)?flag|flag\s*[1-9#]|flags\b|gablewrick|bb\s+arena|console|control\s+plane)\b`)
)

func allowFingerprintExtraProbes(goal, desc string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ARTEX_FINGERPRINT_PROBES"))) {
	case "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	}
	return fingerprintCTFConsoleRe.MatchString(goal + " " + desc)
}

func sameTargetScope(base, next *url.URL) bool {
	if base == nil || next == nil {
		return false
	}
	if !strings.EqualFold(base.Scheme, next.Scheme) {
		return false
	}
	return strings.EqualFold(normalizedHostPort(base), normalizedHostPort(next))
}

func normalizedHostPort(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	if port == "" {
		return host
	}
	return host + ":" + port
}

// TargetFingerprint captures a lightweight read-only GET/HEAD baseline profile
// of a task's goal URL at creation time.
type TargetFingerprint struct {
	URL           string          `json:"url"`
	FinalURL      string          `json:"final_url"`
	Status        int             `json:"status"`
	ContentType   string          `json:"content_type,omitempty"`
	Title         string          `json:"title,omitempty"`
	Server        string          `json:"server,omitempty"`
	Redirects     []string        `json:"redirects,omitempty"`
	Links         []string        `json:"links,omitempty"`
	ProbePaths    map[string]int  `json:"probe_paths"`
	ProbeExists   map[string]bool `json:"probe_exists"`
	CanonicalHost string          `json:"canonical_host,omitempty"`
	Warnings      []string        `json:"warnings,omitempty"`
}

func firstGoalURL(text string) string {
	for _, raw := range rawHTTPLinkRe.FindAllString(text, 4) {
		trimmed := strings.TrimRight(raw, `.,;:'")`)
		if u, err := url.Parse(trimmed); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" {
			return u.String()
		}
	}
	return ""
}

// FingerprintTarget fetches goalURL (GET/HEAD only, respecting task scope),
// probes /login, /signup, /app, /docs, checks for JSON-vs-console and canonical
// vhost mismatches, and records a target_fingerprint fact on t.Store.
func (s *Server) FingerprintTarget(ctx context.Context, t *Task, goalURL string) (*TargetFingerprint, int64, error) {
	if s == nil || t == nil || t.Store == nil {
		return nil, 0, nil
	}
	rawURL := strings.TrimSpace(goalURL)
	if rawURL == "" {
		rawURL = firstGoalURL(t.Goal + " " + t.Description)
	}
	if rawURL == "" {
		return nil, 0, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, 0, nil
	}

	// Deduplicate if already fingerprinted for this task.
	if existing, _ := t.Store.ListByKind(db.KindFact, 100); len(existing) > 0 {
		for _, n := range existing {
			if strings.Contains(string(n.Payload), `"`+db.FactSubtypeTargetFingerprint+`"`) {
				return nil, n.ID, nil
			}
		}
	}

	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	reqCtx = intercept.WithTargetScope(reqCtx, t.targetScope(intercept.DefaultTargetWaitBound))
	if res := intercept.GateRenderedURL(reqCtx, "target_fingerprint", rawURL); res.Code != "" {
		return nil, 0, fmt.Errorf("target_fingerprint gated (%s): %s", res.Code, res.Reason)
	}

	rootHost := strings.ToLower(u.Hostname())
	cleanRoot := *u
	cleanRoot.User = nil
	sanitizedURL := cleanRoot.String()

	var redirects []string
	var crossHostRedirect string
	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.URL.User = nil
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			if !sameTargetScope(&cleanRoot, req.URL) {
				if req.URL.Hostname() != "" {
					crossHostRedirect = strings.ToLower(req.URL.Host)
				}
				redirects = append(redirects, req.URL.String())
				return http.ErrUseLastResponse
			}
			if len(via) >= 4 {
				return http.ErrUseLastResponse
			}
			redirects = append(redirects, req.URL.String())
			if res := intercept.GateRenderedURL(reqCtx, "target_fingerprint", req.URL.String()); res.Code != "" {
				return fmt.Errorf("redirect gated: %s", res.Reason)
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, sanitizedURL, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	bodyStr := string(bodyBytes)
	title := ""
	if m := htmlTitleRe.FindStringSubmatch(bodyStr); len(m) == 2 {
		title = strings.Join(strings.Fields(strings.TrimSpace(m[1])), " ")
	}

	finalURL := sanitizedURL
	if resp.Request != nil && resp.Request.URL != nil {
		fu := *resp.Request.URL
		fu.User = nil
		finalURL = fu.String()
	}

	seenLinks := map[string]bool{}
	var links []string
	addLink := func(l string) {
		l = strings.TrimSpace(l)
		if l == "" || seenLinks[l] || len(links) >= 30 {
			return
		}
		seenLinks[l] = true
		links = append(links, l)
	}
	canonicalHost := crossHostRedirect
	if m := htmlCanonicalLinkRe.FindStringSubmatch(bodyStr); len(m) == 2 {
		addLink(m[1])
		if cu, cerr := url.Parse(m[1]); cerr == nil && cu.Hostname() != "" {
			ch := strings.ToLower(cu.Hostname())
			if ch != rootHost {
				canonicalHost = ch
			}
		}
	}
	for _, m := range htmlLinkHrefRe.FindAllStringSubmatch(bodyStr, 30) {
		if len(m) == 2 {
			addLink(m[1])
			if cu, cerr := url.Parse(m[1]); cerr == nil && (cu.Scheme == "http" || cu.Scheme == "https") && cu.Hostname() != "" {
				ch := strings.ToLower(cu.Hostname())
				if canonicalHost == "" && ch != rootHost {
					canonicalHost = ch
				}
			}
		}
	}
	for _, raw := range rawHTTPLinkRe.FindAllString(bodyStr, 20) {
		addLink(raw)
		if canonicalHost == "" {
			if cu, cerr := url.Parse(raw); cerr == nil && cu.Hostname() != "" && strings.ToLower(cu.Hostname()) != rootHost {
				canonicalHost = strings.ToLower(cu.Hostname())
			}
		}
	}
	if canonicalHost == "" && resp.Request != nil && resp.Request.URL != nil {
		if fh := strings.ToLower(resp.Request.URL.Hostname()); fh != "" && fh != rootHost {
			canonicalHost = fh
		}
	}

	probePaths := map[string]int{}
	probeExists := map[string]bool{}
	if allowFingerprintExtraProbes(t.Goal, t.Description) {
		originBase := cleanRoot.Scheme + "://" + cleanRoot.Host
		for _, p := range []string{"/login", "/signup", "/app", "/docs"} {
			st := probeHeadThenGet(reqCtx, client, originBase+p)
			probePaths[p] = st
			probeExists[p] = st > 0 && st != http.StatusNotFound && st != http.StatusGone
		}
	}

	trimmedBody := strings.TrimSpace(bodyStr)
	isJSON := strings.Contains(strings.ToLower(ct), "json") || strings.HasPrefix(trimmedBody, "{") || strings.HasPrefix(trimmedBody, "[")
	goalLower := strings.ToLower(t.Goal + " " + t.Description)
	anyConsolePath := false
	for _, exists := range probeExists {
		if exists {
			anyConsolePath = true
			break
		}
	}
	var warnings []string
	if isJSON && fingerprintCTFConsoleRe.MatchString(goalLower) {
		if len(probeExists) > 0 && !anyConsolePath {
			warnings = append(warnings, fmt.Sprintf("root %s returned JSON (%d) and /login, /signup, /app, /docs are all missing: possible secondary/service vhost; verify the main instance URL", sanitizedURL, resp.StatusCode))
		} else if strings.Contains(goalLower, "console") {
			warnings = append(warnings, fmt.Sprintf("root %s returned JSON (%d) while the task goal mentions console — verify whether the web console is on another host or path", sanitizedURL, resp.StatusCode))
		}
	}
	if canonicalHost != "" {
		warnings = append(warnings, fmt.Sprintf("page references canonical/alternate host %s (task target host is %s) — verify vhost alignment", canonicalHost, rootHost))
	}

	fp := &TargetFingerprint{
		URL:           sanitizedURL,
		FinalURL:      finalURL,
		Status:        resp.StatusCode,
		ContentType:   ct,
		Title:         title,
		Server:        resp.Header.Get("Server"),
		Redirects:     redirects,
		Links:         links,
		ProbePaths:    probePaths,
		ProbeExists:   probeExists,
		CanonicalHost: canonicalHost,
		Warnings:      warnings,
	}

	probeDesc := "probes=root-only"
	if len(probePaths) > 0 {
		probeDesc = fmt.Sprintf("probes=/login:%d /signup:%d /app:%d /docs:%d",
			probePaths["/login"], probePaths["/signup"], probePaths["/app"], probePaths["/docs"])
	}
	summary := fmt.Sprintf("Target fingerprint %s: HTTP %d (%s), title=%q, server=%q, %s",
		sanitizedURL, fp.Status, fp.ContentType, fp.Title, fp.Server, probeDesc)
	if len(warnings) > 0 {
		summary += "; WARNING: " + strings.Join(warnings, " | ")
	}
	evBytes, _ := json.Marshal(fp)
	payload := map[string]any{
		"subtype":     db.FactSubtypeTargetFingerprint,
		"fingerprint": fp,
		"warnings":    warnings,
		"summary":     summary,
		"source":      "tool_output",
		"confidence":  "observed",
		"evidence":    string(evBytes),
	}
	factID, err := t.Store.AddNode(db.KindFact, payload, 0, "confirmed", "system", nil)
	if err != nil {
		return fp, 0, err
	}
	if origin, _ := t.Store.OriginFactID(); origin > 0 {
		_ = t.Store.Link(origin, db.RelYields, factID)
	}
	if len(warnings) > 0 && s.engine != nil {
		s.engine.emitActivity(t, db.Activity{
			Worker:  "system",
			Kind:    "text",
			IsError: false,
			Summary: "Target fingerprint warning: " + strings.Join(warnings, "; "),
			Detail:  string(evBytes),
		})
	}
	return fp, factID, nil
}

func probeHeadThenGet(ctx context.Context, client *http.Client, target string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return 0
	}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			return resp.StatusCode
		}
	}
	reqGet, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0
	}
	respGet, err := client.Do(reqGet)
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(respGet.Body, 4096))
	respGet.Body.Close()
	return respGet.StatusCode
}

func (s *Server) fingerprintTaskAsync(t *Task, seedText string) {
	if s == nil || t == nil || s.ctx.Err() != nil {
		return
	}
	urlStr := firstGoalURL(seedText)
	if urlStr == "" {
		urlStr = firstGoalURL(t.Goal + " " + t.Description)
	}
	if urlStr == "" {
		return
	}
	go func() {
		if _, _, err := s.FingerprintTarget(s.ctx, t, urlStr); err != nil {
			log.Printf("[fingerprint] task %s (%s): %v", t.ID, urlStr, err)
		}
	}()
}
