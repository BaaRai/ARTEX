package agent

import (
	"github.com/Autumn-27/artex/intercept"
	actool "github.com/Autumn-27/norma/tool"
)

// withWebFetch returns tools plus the WebFetch tool of an agent session. Every
// agent kind builds WebFetch here instead of through agentcore's EnableWebFetch,
// which has no option for a redirect policy: every redirect hop goes through the
// target gate (intercept.RedirectCheck), on the client that uses proxy and on
// the direct retry client alike. The tool is added after the session's own
// tools, where agentcore adds it, so the tool list keeps its order. caCert is the
// recording proxy's CA, trusted to verify the HTTPS it re-signs. tools is not
// modified.
func withWebFetch(tools []actool.CoreTool, proxy, caCert string) []actool.CoreTool {
	webFetch := actool.NewWebFetch(actool.WebFetchConfig{Proxy: proxy, CACert: caCert, CheckRedirect: intercept.RedirectCheck("WebFetch")})
	return append(tools[:len(tools):len(tools)], webFetch)
}
