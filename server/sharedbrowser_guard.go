package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
)

// sharedBrowserToolNote is appended to the description of every browser MCP tool
// while the shared browser is on, so the model knows the operator can see and
// take over the page, and what to do at a CAPTCHA.
const sharedBrowserToolNote = "\nThis browser is shared with the human operator, who watches it live and can take over. " +
	"At a CAPTCHA, a \"verify you are human\" page, an email/SMS code or a 2FA prompt: do not try to solve or bypass it; " +
	"call ask_operator (attach a screenshot) and the operator completes it in this same browser, then continue after their reply."

// isSharedBrowserMCP reports whether an MCP row is the browser MCP the shared
// browser takes over: the seeded "browser" server running Playwright MCP.
func isSharedBrowserMCP(m *db.MCPServer) bool {
	if m == nil || m.Name != browserMCPName || m.Transport != "stdio" {
		return false
	}
	line := m.Command + " " + strings.Join(jsonStrSlice(m.Args), " ")
	return strings.Contains(line, "playwright-mcp") || strings.Contains(line, "@playwright/mcp")
}

// browserMCPProxy reads the proxy that syncBrowserMCPProxy put on the row, and
// whether it is the traffic-capture proxy (its CA is set, so TLS is re-signed).
func browserMCPProxy(args []string, env map[string]string) (proxy string, insecure bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--proxy-server" && i+1 < len(args) {
			proxy = args[i+1]
			i++
		} else if v, ok := strings.CutPrefix(a, "--proxy-server="); ok {
			proxy = v
		}
	}
	return proxy, proxy != "" && env["NODE_EXTRA_CA_CERTS"] != ""
}

// sharedBrowserMCPArgs rewrites the browser MCP's arguments to drive the shared
// browser: launch options that no longer apply (headless, isolated profile, proxy,
// a browser binary or profile of its own) are dropped, and --cdp-endpoint is set.
// The stored row is left alone, so turning the shared browser off restores it.
func sharedBrowserMCPArgs(args []string, endpoint string) []string {
	withValue := map[string]bool{
		"--proxy-server": true, "--proxy-bypass": true, "--user-data-dir": true, "--cdp-endpoint": true,
		"--executable-path": true, "--storage-state": true, "--device": true, "--user-agent": true,
	}
	flags := map[string]bool{"--headless": true, "--isolated": true, "--headed": true}
	out := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, _, hasEq := strings.Cut(a, "=")
		if flags[name] {
			continue
		}
		if withValue[name] {
			if !hasEq {
				i++ // its value is the next argument
			}
			continue
		}
		out = append(out, a)
	}
	return append(out, "--cdp-endpoint", endpoint)
}

// browserRunHolder names the agent run for the browser lease: the worker intent
// when there is one (the key ask_operator uses too), else the task and agent, the
// chat session, or a per-run id.
func browserRunHolder(ri agent.RunInfo, agentKey string) (key, label string) {
	switch {
	case ri.TaskID != 0 && ri.IntentID != 0:
		return fmt.Sprintf("t%d/i%d", ri.TaskID, ri.IntentID), fmt.Sprintf("task %d · intent %d (%s)", ri.TaskID, ri.IntentID, agentKey)
	case ri.TaskID != 0:
		return fmt.Sprintf("t%d/%s", ri.TaskID, agentKey), fmt.Sprintf("task %d (%s)", ri.TaskID, agentKey)
	case ri.SessionID != "":
		return "chat/" + ri.SessionID, fmt.Sprintf("chat (%s)", agentKey)
	}
	return "run/" + uuid.NewString(), agentKey
}

// sharedBrowserTool wraps one browser MCP tool with the shared-browser gate:
// operator control, the one-run-at-a-time lease, and the CAPTCHA handoff.
type sharedBrowserTool struct {
	actool.CoreTool
	b      *sharedBrowser
	holder string
	label  string
}

func (t *sharedBrowserTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	if refusal := t.b.AgentGate(ctx, t.holder, t.label, t.Name()); refusal != "" {
		return actool.Errorf(refusal), nil
	}
	return t.CoreTool.Call(ctx, in, tc)
}

// AcceptsRawInput forwards the wrapped tool's opt-out of the harness schema check.
func (t *sharedBrowserTool) AcceptsRawInput() bool {
	r, ok := t.CoreTool.(interface{ AcceptsRawInput() bool })
	return ok && r.AcceptsRawInput()
}

// prepareSharedBrowserMCP starts (or reuses) the shared browser and returns a copy
// of the MCP row pointed at it. ok is false when the browser could not start: the
// caller then connects the row unchanged (a private headless browser, as before).
func prepareSharedBrowserMCP(ctx context.Context, b *sharedBrowser, m *db.MCPServer) (*db.MCPServer, bool) {
	args := jsonStrSlice(m.Args)
	proxy, insecure := browserMCPProxy(args, jsonStrMap(m.Env))
	endpoint, err := b.Ensure(ctx, proxy, insecure)
	if err != nil {
		logSharedBrowserFallback(err)
		return m, false
	}
	cp := *m
	cp.Args = encodeJSON(sharedBrowserMCPArgs(args, endpoint))
	return &cp, true
}

// logSharedBrowserFallback logs a failed shared-browser start once per distinct
// error, so a missing Chromium does not repeat on every agent run.
func logSharedBrowserFallback(err error) {
	logExecOnce("[browser] shared browser unavailable, agents fall back to a private headless browser: %v", err)
}
