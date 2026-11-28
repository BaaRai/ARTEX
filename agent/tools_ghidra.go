package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/ghidra"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// Ghidra tools. The sidecar (ghidra-headless-rest) is optional: ARTEX_GHIDRA_URL
// enables it. Analysis is asynchronous — ghidra_analyze uploads a binary from
// the run directory and returns a job id, then ghidra_job polls it and the
// read tools query the finished job.
//
// Static analysis only. The sidecar never executes the uploaded file, and
// nothing here does either.

const (
	ghidraMaxToolBytes = 96 << 10
	ghidraPollTimeout  = 90 * time.Second
)

func (t *ToolSet) ghidraClient() *ghidra.Client { return ghidra.FromEnv() }

func ghidraUnavailable(err error) (actool.Result, error) {
	if errors.Is(err, ghidra.ErrNotConfigured) {
		return actool.Errorf("Ghidra is not configured: set ARTEX_GHIDRA_URL to the ghidra-headless-rest sidecar (for example http://ghidra:9090) and restart ARTEX"), nil
	}
	return actool.Errorf("Ghidra request failed: " + err.Error()), nil
}

// clipToolJSON keeps a large result inside a useful prompt budget without
// hiding that it was cut: the model is told how to narrow the query instead of
// silently reading a truncated document as complete.
func clipToolJSON(raw json.RawMessage) string {
	if len(raw) <= ghidraMaxToolBytes {
		return string(raw)
	}
	return string(raw[:ghidraMaxToolBytes]) + "\n... result truncated at " + strconv.Itoa(ghidraMaxToolBytes) + " bytes; narrow the query with offset/limit or a search term"
}

func (t *ToolSet) ghidraStatus() actool.CoreTool {
	return readTool(
		"ghidra_status",
		"Check the Ghidra reverse-engineering sidecar: health, version and advertised capabilities. Call it once before the first ghidra_* tool in a task; if it reports that the service is not configured, tell the operator instead of retrying.",
		obj(map[string]any{}),
		func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
			c := t.ghidraClient()
			if !c.Configured() {
				return ghidraUnavailable(ghidra.ErrNotConfigured)
			}
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			health, err := c.Health(ctx)
			if err != nil {
				return ghidraUnavailable(err)
			}
			out := map[string]any{"url": c.BaseURL(), "health": json.RawMessage(health)}
			if caps, err := c.Capabilities(ctx); err == nil {
				out["capabilities"] = json.RawMessage(caps)
			}
			return jsonResult(out)
		})
}

// writeToolTC is writeTool for a handler that needs the ToolContext, here the
// run's working directory: the uploaded file is opened with the same confinement
// as the file tools, so a model-supplied path cannot reach another run's tree.
func writeToolTC(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision { return acperm.Allowed() },
		Run:         run,
	})
}

func (t *ToolSet) ghidraAnalyze() actool.CoreTool {
	return writeToolTC(
		"ghidra_analyze",
		"Upload a binary from your working directory to the Ghidra sidecar and start static analysis. Returns {job_id, status}. The file is analyzed, never executed. Use it on artifacts you already downloaded (APK/JAR native libraries, desktop binaries, firmware components); it is not a remote-analysis service and cannot read a path outside your run directory.",
		obj(map[string]any{
			"path": str("path of the binary, relative to your working directory"),
		}, "path"),
		func(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			c := t.ghidraClient()
			if !c.Configured() {
				return ghidraUnavailable(ghidra.ErrNotConfigured)
			}
			root := ""
			if tc != nil {
				root = tc.WorkingDir
			}
			f, err := openScanOutputInBase(t.workBase, root, a.Path)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			defer f.Close()
			ctx, cancel := context.WithTimeout(ctx, ghidra.UploadTimeout)
			defer cancel()
			res, err := c.Upload(ctx, baseName(a.Path), f)
			if err != nil {
				return ghidraUnavailable(err)
			}
			return actool.Text(clipToolJSON(res)), nil
		})
}

// baseName keeps the uploaded file name while refusing to send a whole path.
func baseName(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	if p == "" {
		return "artifact.bin"
	}
	return p
}

func (t *ToolSet) ghidraJob() actool.CoreTool {
	return readTool(
		"ghidra_job",
		"Poll one Ghidra analysis job: status (queued|running|done|failed|cancelled|interrupted), progress and metadata. Analysis of a large binary takes minutes; poll with this instead of re-uploading. When it returns done, query the results with ghidra_functions, ghidra_decompile, ghidra_strings or ghidra_attack_surface.",
		obj(map[string]any{
			"job_id": str("job id returned by ghidra_analyze"),
		}, "job_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				JobID string `json:"job_id"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			c := t.ghidraClient()
			ctx, cancel := context.WithTimeout(ctx, ghidraPollTimeout)
			defer cancel()
			res, err := c.Job(ctx, a.JobID)
			if err != nil {
				return ghidraUnavailable(err)
			}
			return actool.Text(clipToolJSON(res)), nil
		})
}

func (t *ToolSet) ghidraFunctions() actool.CoreTool {
	return readTool(
		"ghidra_functions",
		"List functions of a finished Ghidra job, optionally filtered by name, display name or address. Use it to locate the entry points worth decompiling instead of pulling the whole program.",
		obj(map[string]any{
			"job_id": str("job id of a finished analysis"),
			"search": str("optional name, display name or address filter"),
			"offset": intp("pagination offset"),
			"limit":  intp("page size, at most 200"),
		}, "job_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				JobID  string `json:"job_id"`
				Search string `json:"search"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			res, err := t.ghidraClient().Functions(ctx, a.JobID, a.Search, a.Offset, clampPage(a.Limit))
			if err != nil {
				return ghidraUnavailable(err)
			}
			return actool.Text(clipToolJSON(res)), nil
		})
}

func (t *ToolSet) ghidraDecompile() actool.CoreTool {
	return readTool(
		"ghidra_decompile",
		"Decompile one function of a finished Ghidra job to C-like pseudocode. Addresses may be padded or unpadded (0x401000 and 0x00401000 resolve to the same function). Decompilation is evidence for your reasoning, not proof of a vulnerability.",
		obj(map[string]any{
			"job_id": str("job id of a finished analysis"),
			"addr":   str("function address, hex, for example 0x401000"),
		}, "job_id", "addr"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				JobID string `json:"job_id"`
				Addr  string `json:"addr"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			defer cancel()
			res, err := t.ghidraClient().Decompile(ctx, a.JobID, a.Addr)
			if err != nil {
				return ghidraUnavailable(err)
			}
			return actool.Text(clipToolJSON(res)), nil
		})
}

func (t *ToolSet) ghidraStrings() actool.CoreTool {
	return readTool(
		"ghidra_strings",
		"List printable strings extracted from a finished Ghidra job. Use it for embedded endpoints, keys, debug flags and test accounts; filter by minimum length rather than dumping everything.",
		obj(map[string]any{
			"job_id":     str("job id of a finished analysis"),
			"min_length": intp("minimum string length, default 4"),
			"offset":     intp("pagination offset"),
			"limit":      intp("page size, at most 200"),
		}, "job_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				JobID     string `json:"job_id"`
				MinLength int    `json:"min_length"`
				Offset    int    `json:"offset"`
				Limit     int    `json:"limit"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			res, err := t.ghidraClient().Strings(ctx, a.JobID, a.MinLength, a.Offset, clampPage(a.Limit))
			if err != nil {
				return ghidraUnavailable(err)
			}
			return actool.Text(clipToolJSON(res)), nil
		})
}

func (t *ToolSet) ghidraAttackSurface() actool.CoreTool {
	return readTool(
		"ghidra_attack_surface",
		"Rank functions of a finished Ghidra job by the sidecar's deterministic attack-surface score (parsing, memory safety, command execution, crypto, indirect calls, JNI). Use it to choose what to review first. The score is a review prior, not a vulnerability verdict: confirm anything you report with the decompiled code and a fact.",
		obj(map[string]any{
			"job_id":    str("job id of a finished analysis"),
			"band":      str("optional severity band: critical, high, medium or low"),
			"category":  str("optional category filter, for example memory or command"),
			"min_score": intp("optional minimum score"),
			"offset":    intp("pagination offset"),
			"limit":     intp("page size, at most 100"),
		}, "job_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				JobID    string `json:"job_id"`
				Band     string `json:"band"`
				Category string `json:"category"`
				MinScore int    `json:"min_score"`
				Offset   int    `json:"offset"`
				Limit    int    `json:"limit"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			res, err := t.ghidraClient().SecurityFunctions(ctx, a.JobID, a.Band, a.Category, "", a.MinScore, a.Offset, clampPage(a.Limit))
			if err != nil {
				return ghidraUnavailable(err)
			}
			return actool.Text(clipToolJSON(res)), nil
		})
}

func clampPage(limit int) int {
	switch {
	case limit <= 0:
		return 50
	case limit > 200:
		return 200
	}
	return limit
}
