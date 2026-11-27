package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Autumn-27/artex/opentrap"
	actool "github.com/Autumn-27/norma/tool"
)

const (
	opentrapMaxToolBytes = 96 << 10
	opentrapTimeout      = 90 * time.Second
)

func (t *ToolSet) opentrapClient() *opentrap.Client { return opentrap.FromEnv() }

func opentrapUnavailable(err error) (actool.Result, error) {
	if errors.Is(err, opentrap.ErrNotConfigured) {
		return actool.Errorf("OpenTRAP is not configured: set ARTEX_OPENTRAP_URL to the opentrap sidecar (for example http://opentrap:8081) and restart ARTEX"), nil
	}
	return actool.Errorf("OpenTRAP request failed: " + err.Error()), nil
}

func opentrapClipJSON(raw json.RawMessage) string {
	if len(raw) <= opentrapMaxToolBytes {
		return string(raw)
	}
	return string(raw[:opentrapMaxToolBytes]) + "\n... result truncated at " + strconv.Itoa(opentrapMaxToolBytes) + " bytes; narrow the query with offset/limit"
}

func (t *ToolSet) opentrapStatus() actool.CoreTool {
	return readTool(
		"opentrap_status",
		"Check the OpenTRAP intercepting-proxy sidecar: status, version and capabilities. Call it once before the first opentrap_* tool in a task; if it reports that the service is not configured, tell the operator instead of retrying.",
		obj(map[string]any{}),
		func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
			c := t.opentrapClient()
			if !c.Configured() {
				return opentrapUnavailable(opentrap.ErrNotConfigured)
			}
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			status, err := c.Status(ctx)
			if err != nil {
				return opentrapUnavailable(err)
			}
			out := map[string]any{"url": c.BaseURL(), "status": json.RawMessage(status)}
			return jsonResult(out)
		})
}

func (t *ToolSet) opentrapHistory() actool.CoreTool {
	return readTool(
		"opentrap_history",
		"List the OpenTRAP HTTP history (requests and responses captured by the proxy). Use offset/limit to page through the results.",
		obj(map[string]any{
			"limit":  intp("page size, at most 200"),
			"offset": intp("pagination offset"),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Limit  int `json:"limit"`
				Offset int `json:"offset"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			c := t.opentrapClient()
			if !c.Configured() {
				return opentrapUnavailable(opentrap.ErrNotConfigured)
			}
			ctx, cancel := context.WithTimeout(ctx, opentrapTimeout)
			defer cancel()
			res, err := c.History(ctx, opentrapClampPage(a.Limit), a.Offset)
			if err != nil {
				return opentrapUnavailable(err)
			}
			return actool.Text(opentrapClipJSON(res)), nil
		})
}

func (t *ToolSet) opentrapHistoryItem() actool.CoreTool {
	return readTool(
		"opentrap_history_item",
		"Get one OpenTRAP history entry by id, with full request and response.",
		obj(map[string]any{
			"id": str("history entry id"),
		}, "id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			c := t.opentrapClient()
			if !c.Configured() {
				return opentrapUnavailable(opentrap.ErrNotConfigured)
			}
			ctx, cancel := context.WithTimeout(ctx, opentrapTimeout)
			defer cancel()
			res, err := c.HistoryItem(ctx, a.ID)
			if err != nil {
				return opentrapUnavailable(err)
			}
			return actool.Text(opentrapClipJSON(res)), nil
		})
}

func (t *ToolSet) opentrapScopeGet() actool.CoreTool {
	return readTool(
		"opentrap_scope_get",
		"Get the current OpenTRAP scope rules (included and excluded URL prefixes/regexes).",
		obj(map[string]any{}),
		func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
			c := t.opentrapClient()
			if !c.Configured() {
				return opentrapUnavailable(opentrap.ErrNotConfigured)
			}
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			res, err := c.ScopeGet(ctx)
			if err != nil {
				return opentrapUnavailable(err)
			}
			return actool.Text(opentrapClipJSON(res)), nil
		})
}

func (t *ToolSet) opentrapScopePut() actool.CoreTool {
	return writeTool(
		"opentrap_scope_put",
		"Replace the OpenTRAP scope rules. Provide the full rules object as returned by opentrap_scope_get (include and exclude arrays).",
		obj(map[string]any{
			"rules": str("JSON string of the scope rules object"),
		}, "rules"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Rules string `json:"rules"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			c := t.opentrapClient()
			if !c.Configured() {
				return opentrapUnavailable(opentrap.ErrNotConfigured)
			}
			ctx, cancel := context.WithTimeout(ctx, opentrapTimeout)
			defer cancel()
			res, err := c.ScopePut(ctx, json.RawMessage(a.Rules))
			if err != nil {
				return opentrapUnavailable(err)
			}
			return actool.Text(opentrapClipJSON(res)), nil
		})
}

func (t *ToolSet) opentrapReplay() actool.CoreTool {
	return writeTool(
		"opentrap_replay",
		"Replay a request through the OpenTRAP proxy (like Burp Repeater). The request is sent via the sidecar and recorded in its history. Provide the target URL, optional body, and control headers for content-length update and redirect following.",
		obj(map[string]any{
			"url":                    str("target URL to replay"),
			"body":                   str("optional request body as JSON string"),
			"update_content_length":  str("boolean: add X-OpenTrap-Update-Content-Length header"),
			"follow_redirects":       str("boolean: add X-OpenTrap-Follow-Redirects header"),
		}, "url"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				URL                    string `json:"url"`
				Body                   string `json:"body"`
				UpdateContentLength    bool   `json:"update_content_length"`
				FollowRedirects        bool   `json:"follow_redirects"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			c := t.opentrapClient()
			if !c.Configured() {
				return opentrapUnavailable(opentrap.ErrNotConfigured)
			}
			ctx, cancel := context.WithTimeout(ctx, opentrapTimeout)
			defer cancel()
			res, err := c.Replay(ctx, a.URL, []byte(a.Body), a.UpdateContentLength, a.FollowRedirects)
			if err != nil {
				return opentrapUnavailable(err)
			}
			return actool.Text(opentrapClipJSON(res)), nil
		})
}

func opentrapClampPage(limit int) int {
	switch {
	case limit <= 0:
		return 50
	case limit > 200:
		return 200
	}
	return limit
}