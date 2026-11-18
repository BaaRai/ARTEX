package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	browserMCPName             = "browser"
	browserMCPCommand          = "playwright-mcp"
	browserMCPDirectBinSetting = "browser_mcp_direct_bin_v1"
)

// browserMCPSeedArgs is the JSON args array for a fresh browser MCP row.
// --headless: the image has no display.
// --browser chromium: Playwright MCP defaults to the "chrome" channel at
// /opt/google/chrome, which the image does not ship.
const browserMCPSeedArgs = `["--headless","--browser","chromium"]`

// rewriteStockNpxBrowserMCP converts the original seed
// `npx @playwright/mcp [--headless ...]` to the image's global playwright-mcp
// binary. ok is false when the row is not that stock invocation (the operator
// changed the command or the package), so those rows are left alone.
func rewriteStockNpxBrowserMCP(command string, args []string) (string, []string, bool) {
	if command != "npx" || len(args) == 0 {
		return command, args, false
	}
	pkg := args[0]
	if pkg != "@playwright/mcp" && !strings.HasPrefix(pkg, "@playwright/mcp@") {
		return command, args, false
	}
	rest := append([]string{}, args[1:]...)
	if !hasCLIFlag(rest, "--headless") {
		rest = append(rest, "--headless")
	}
	if !hasCLIFlag(rest, "--browser") {
		rest = append(rest, "--browser", "chromium")
	}
	return browserMCPCommand, rest, true
}

func hasCLIFlag(args []string, flag string) bool {
	eq := flag + "="
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, eq) {
			return true
		}
	}
	return false
}

// migrateStockBrowserMCP rewrites a leftover stock `npx @playwright/mcp` browser
// MCP row once. Gated by a settings flag so an operator who later puts npx back
// is not rewritten on the next start. A row whose command or first arg is no
// longer the stock seed is left as-is.
func (d *DB) migrateStockBrowserMCP() error {
	if v, _, _ := d.GetSetting(browserMCPDirectBinSetting); v == "done" {
		return nil
	}
	var command string
	var argsRaw []byte
	err := d.QueryRow(`SELECT COALESCE(command,''), args FROM mcp_servers WHERE name=$1`, browserMCPName).Scan(&command, &argsRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return d.SetSetting(browserMCPDirectBinSetting, "done")
	}
	if err != nil {
		return fmt.Errorf("read browser mcp: %w", err)
	}
	var args []string
	if len(argsRaw) > 0 {
		if err := json.Unmarshal(argsRaw, &args); err != nil {
			return d.SetSetting(browserMCPDirectBinSetting, "done")
		}
	}
	newCommand, newArgs, ok := rewriteStockNpxBrowserMCP(command, args)
	if ok {
		raw, err := json.Marshal(newArgs)
		if err != nil {
			return fmt.Errorf("encode browser mcp args: %w", err)
		}
		if _, err := d.Exec(`UPDATE mcp_servers SET command=$1, args=$2 WHERE name=$3`, newCommand, string(raw), browserMCPName); err != nil {
			return fmt.Errorf("update browser mcp: %w", err)
		}
	}
	return d.SetSetting(browserMCPDirectBinSetting, "done")
}
