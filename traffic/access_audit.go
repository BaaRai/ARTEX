package traffic

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"github.com/Autumn-27/artex/accessaudit"
	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

type auditFlowKey struct{}
type auditFlow struct {
	epoch         uint64
	identity      accessaudit.Identity
	execution     bool
	manualCapture bool
}

func (t *Traffic) SetAccessAudit(s *accessaudit.Store) { t.accessAudit.Store(s) }

// Capture defaults to on for standalone Traffic users. Manager applies the
// persisted setting before accepting connections. An epoch prevents off/on
// transitions from re-enabling persistence for an already-running stream.
func (t *Traffic) SetCaptureEnabled(enabled bool) {
	t.captureMu.Lock()
	defer t.captureMu.Unlock()
	for {
		old := t.captureEpoch.Load()
		if (old&1 == 0) == enabled {
			return
		}
		if t.captureEpoch.CompareAndSwap(old, old+1) {
			return
		}
	}
}

// ExecutionProxy binds an unguessable proxy credential to trusted run context.
// Release revokes it, including on connections still open after the run ends.
// This is attribution for cooperating tools, not an executor isolation boundary.
func (t *Traffic) ExecutionProxy(identity accessaudit.Identity) (string, string, func()) {
	token := rand.Text()
	t.auditLeases.Store(token, identity)
	u, _ := url.Parse(t.ProxyAddr())
	// The password also preserves the Bash pacing marker.
	u.User = url.UserPassword(token, "artex-pace")
	return u.String(), t.CACertPath(), func() { t.auditLeases.Delete(token) }
}

func (s *sink) beginAudit(f *mproxy.Flow) {
	if s.t == nil || f == nil || f.Request == nil {
		return
	}
	state := auditFlow{epoch: s.t.captureEpoch.Load()}
	auth := f.Request.Header.Get("Proxy-Authorization")
	scheme, value, ok := strings.Cut(auth, " ")
	token := ""
	if ok && strings.EqualFold(scheme, "Basic") {
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err == nil {
			token, _, _ = strings.Cut(string(decoded), ":")
		}
	}
	if f.ConnContext != nil && f.ConnContext.ClientConn != nil {
		id := f.ConnContext.ClientConn.Id
		if auth != "" {
			// A new, invalid credential must not inherit a previous run's identity.
			s.auditConnections.Delete(id)
			_, execution := s.t.auditLeases.Load(token)
			_, replay := s.t.replayCaptureLeases.Load(token)
			if execution || replay {
				s.auditConnections.Store(id, token)
			}
		} else if previous, exists := s.auditConnections.Load(id); exists {
			token = previous.(string)
		}
	}
	if identity, exists := s.t.auditLeases.Load(token); exists {
		state.identity, state.execution = identity.(accessaudit.Identity), true
	}
	_, state.manualCapture = s.t.replayCaptureLeases.Load(token)
	if raw := f.Request.Raw(); raw != nil {
		*raw = *raw.WithContext(context.WithValue(raw.Context(), auditFlowKey{}, state))
	}
	if !s.captureAllowed(f) {
		f.Stream = true
	}
	// CONNECT establishes a tunnel, not an HTTP resource access. Do not fabricate
	// a URL from its authority or count a successful tunnel as a target response.
	if state.execution && f.Request.Method != http.MethodConnect {
		s.recordAudit(f, "proxy_request", 0)
	}
}
func (s *sink) auditState(f *mproxy.Flow) auditFlow {
	if f != nil && f.Request != nil {
		if raw := f.Request.Raw(); raw != nil {
			if state, ok := raw.Context().Value(auditFlowKey{}).(auditFlow); ok {
				return state
			}
		}
	}
	return auditFlow{epoch: s.t.captureEpoch.Load()}
}
func (s *sink) captureAllowed(f *mproxy.Flow) bool {
	epoch := s.t.captureEpoch.Load()
	state := s.auditState(f)
	return (epoch&1 == 0 || state.manualCapture) && state.epoch == epoch
}
func (s *sink) recordAudit(f *mproxy.Flow, outcome string, status int) {
	if f == nil || f.Request == nil || f.Request.URL == nil || f.Request.Method == http.MethodConnect {
		return
	}
	if f.Request.URL.Scheme != "http" && f.Request.URL.Scheme != "https" {
		return
	}
	state := s.auditState(f)
	source, tool := "recording_proxy", ""
	if state.execution {
		source, tool = "execution_proxy", "Bash"
	}
	s.t.accessAudit.Load().Record(accessaudit.Event{Identity: state.identity, Source: source, Tool: tool, Outcome: outcome, Method: f.Request.Method, URL: f.Request.URL.String(), Status: status, RequestID: f.Id.String()})
}
func (s *sink) Responseheaders(f *mproxy.Flow) {
	if f == nil || f.Response == nil {
		return
	}
	if !s.captureAllowed(f) {
		f.Stream = true
	}
	s.recordAudit(f, "response_headers", f.Response.StatusCode)
}
