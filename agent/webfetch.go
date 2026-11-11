package agent

import (
	"context"

	"github.com/Autumn-27/artex/intercept"
	actool "github.com/Autumn-27/norma/tool"
)

// withWebFetch returns tools plus the WebFetch tool of an agent session. Every
// agent kind builds WebFetch here, one tool per run, so that its configuration
// can depend on the run: every redirect hop goes through the target gate
// (intercept.RedirectCheck), and every connection through the pinned dialer
// (intercept.PinnedDialer), on the client that uses proxy and on the direct
// retry client alike. ctx is the context of the run: when it carries a target
// scope the dialer never connects without one, and when that scope's gate is
// on the direct retry is turned off for the run, since it would leave without
// the proxy. The tool is added after the session's own tools, where agentcore
// adds it, so the tool list keeps its order. caCert is the recording proxy's
// CA, trusted to verify the HTTPS it re-signs. tools is not modified.
func withWebFetch(ctx context.Context, tools []actool.CoreTool, proxy, caCert string) []actool.CoreTool {
	scope, scoped := intercept.TargetScopeFromContext(ctx)
	gated := scoped && scope.Enabled != nil && scope.Enabled()
	webFetch := actool.NewWebFetch(actool.WebFetchConfig{Proxy: proxy, CACert: caCert, CheckRedirect: intercept.RedirectCheck("WebFetch"),
		DialContext: intercept.PinnedDialer("WebFetch", scoped), DisableDirectRetry: gated})
	return append(tools[:len(tools):len(tools)], webFetch)
}
