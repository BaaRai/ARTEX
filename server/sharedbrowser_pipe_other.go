//go:build !unix

package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const sharedBrowserUsesPipe = false

// startSharedBrowserCDP starts Chromium on non-Unix platforms (where ExtraFiles
// is unavailable) using an ephemeral loopback port, deletes DevToolsActivePort
// immediately after reading it, and routes MCP clients through ARTEX's
// authenticated CDP proxy.
func (b *sharedBrowser) startSharedBrowserCDP(ctx context.Context, cmd *exec.Cmd, profileDir string) (*cdpConn, <-chan struct{}, error) {
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()

	port, path, err := waitDevToolsPort(ctx, profileDir, exited)
	// Remove DevToolsActivePort immediately so it is not left readable on disk.
	_ = os.Remove(filepath.Join(profileDir, "DevToolsActivePort"))
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, nil, err
	}
	conn, err := dialCDP(ctx, "ws://127.0.0.1:"+port+path, b.onEvent)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, nil, fmt.Errorf("connect to the browser: %w", err)
	}
	return conn, exited, nil
}
