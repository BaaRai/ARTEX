package traffic

import (
	"github.com/Autumn-27/artex/accessaudit"
	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

func (t *Traffic) SetAccessAudit(s *accessaudit.Store) { t.accessAudit.Store(s) }
func (s *sink) Responseheaders(f *mproxy.Flow) {
	if f == nil || f.Request == nil || f.Request.URL == nil || f.Response == nil {
		return
	}
	s.t.accessAudit.Load().Record(accessaudit.Event{Source: "recording_proxy", Outcome: "response_headers", Method: f.Request.Method, URL: f.Request.URL.String(), Status: f.Response.StatusCode})
}
