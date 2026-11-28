package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/accessaudit"
)

func (s *Server) getAccessAudit(w http.ResponseWriter, r *http.Request) {
	if s.m.audit == nil {
		writeErr(w, 503, "access audit unavailable")
		return
	}
	v := r.URL.Query()
	now := time.Now().UTC()
	q := accessaudit.Query{From: now.Add(-24 * time.Hour), To: now, URL: v.Get("url"), Host: v.Get("host"), TaskID: v.Get("task"), Source: v.Get("source"), Limit: 100}
	for _, part := range []struct {
		key string
		dst *time.Time
	}{{"from", &q.From}, {"to", &q.To}} {
		if raw := v.Get(part.key); raw != "" {
			parsed, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				writeErr(w, 400, "from/to must be RFC3339 timestamps with timezone")
				return
			}
			*part.dst = parsed
		}
	}
	if raw := v.Get("before"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			writeErr(w, 400, "invalid before cursor")
			return
		}
		q.Before = n
	}
	if raw := v.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			writeErr(w, 400, "limit must be 1..200")
			return
		}
		q.Limit = n
	}
	// Pasted full URLs follow the same redaction as observations. Substrings are
	// literal, case-sensitive path searches; SQL wildcard characters stay literal.
	if strings.Contains(q.URL, "://") {
		safe, _, err := accessaudit.RedactURL(q.URL)
		if err != nil {
			writeErr(w, 400, "invalid URL")
			return
		}
		q.URL = safe
	}
	if !q.From.Before(q.To) || q.To.Sub(q.From) > 31*24*time.Hour || len(q.URL) > accessaudit.MaxURL || len(q.Host) > 255 || len(q.TaskID) > 64 || len(q.Source) > 64 {
		writeErr(w, 400, "invalid filters or time range (maximum 31 days)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	page, err := s.m.audit.Search(ctx, q)
	if err != nil {
		writeErr(w, 503, "access audit query unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"events": page.Events, "next": page.Next, "from": q.From, "to": q.To, "coverage": "partial", "retention_days": accessaudit.RetentionDays, "max_events": accessaudit.MaxEvents, "write_failures_since_start": s.m.audit.Failures(), "process_started_at": s.m.audit.Started()})
}
