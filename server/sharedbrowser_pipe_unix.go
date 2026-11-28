//go:build unix

package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// sharedBrowserUsesPipe reports whether this platform launches Chromium with
// --remote-debugging-pipe so no unauthenticated DevTools TCP port is opened.
const sharedBrowserUsesPipe = true

// startSharedBrowserCDP launches Chromium using --remote-debugging-pipe (FD 3
// for incoming commands, FD 4 for outgoing responses/events). Because Chromium
// binds no TCP port and writes no DevToolsActivePort file, an agent Bash
// subprocess cannot connect directly to Chromium.
func (b *sharedBrowser) startSharedBrowserCDP(_ context.Context, cmd *exec.Cmd, _ string) (*cdpConn, <-chan struct{}, error) {
	// FD 3: parent writes -> Chromium reads.
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("cdp pipe in: %w", err)
	}
	// FD 4: Chromium writes -> parent reads.
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, nil, fmt.Errorf("cdp pipe out: %w", err)
	}
	cmd.ExtraFiles = []*os.File{inR, outW}
	if err := cmd.Start(); err != nil {
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	// Close the child ends in the parent so EOF propagates cleanly on exit.
	_ = inR.Close()
	_ = outW.Close()

	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	conn := newPipeCDP(inW, outR, b.onEvent)
	return conn, exited, nil
}
