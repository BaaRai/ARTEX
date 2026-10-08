package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// smokeEnv lets the child process launched by the smoke test skip Bootstrap entirely.
//
// Strictly speaking it would be fine without it: the child's os.Executable() is
// artex.new, so every derived path carries the .new infix and can never touch the
// real upgrade files. But relying on that coincidence is too fragile — an explicit
// short-circuit is obvious at a glance and saves the child a pointless disk probe.
const smokeEnv = "ARTEX_SELFUPDATE_SMOKE"

// Action is Bootstrap's instruction to main.
type Action int

const (
	// Continue: start the server as usual.
	Continue Action = iota
	// Restart: exit immediately with ExitRestart so the supervisor script relaunches us.
	Restart
)

// State describes the upgrade state at this startup, so /api/update/check can
// faithfully tell the frontend "did the last upgrade succeed or was it rolled back".
type State struct {
	Pending     bool   // swapped but not yet confirmed stable
	RolledBack  bool   // this startup just performed an automatic rollback
	FailedStage bool   // staged file failed verification/smoke test and was discarded
	Detail      string // one-sentence user-facing explanation
}

// Bootstrap runs at the very beginning of main, and must be called before any port
// is listened on or any database is opened.
//
// Three situations:
//
//	① staged file artex.new exists → verify + smoke test; on success swap and ask
//	                                 for a restart; on failure discard it and keep
//	                                 running the old version
//	② only the marker file remains → the swap just happened; count one attempt;
//	                                 roll back after enough consecutive failures
//	③ nothing                      → normal startup
func Bootstrap() (Action, State) {
	if os.Getenv(smokeEnv) != "" {
		return Continue, State{}
	}
	p, err := ResolvePaths()
	if err != nil {
		log.Printf("[update] skipping bootstrap: %v", err)
		return Continue, State{}
	}

	if _, err := os.Stat(p.New); err == nil {
		return applyStaged(p)
	}

	m, ok := readMarker(p.Marker)
	if !ok {
		return Continue, State{}
	}
	return confirmOrRollback(p, m)
}

// applyStaged handles the "staged file exists" situation: swap if it verifies,
// discard it if not.
//
// This is the only place in the whole upgrade chain that overwrites the executable,
// and the final gate — the smoke test stops problems like a corrupted download,
// a wrong architecture choice, or missing dynamic libraries. Once a binary that
// cannot run is let through, the supervisor script will tirelessly keep relaunching
// it while the Go code never gets a chance to run, making automatic rollback impossible.
func applyStaged(p Paths) (Action, State) {
	m, _ := readMarker(p.Marker)

	if err := verifyStaged(p); err != nil {
		log.Printf("[update] staged new version failed verification, discarded, continuing with the current version: %v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "new version failed verification and was discarded: " + err.Error()}
	}

	if err := swap(p); err != nil {
		log.Printf("[update] swap failed, continuing with the current version: %v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "swap failed: " + err.Error()}
	}

	// Swap succeeded. Keep the marker; the next startup (running the new version) confirms whether it is stable.
	m.Attempts = 0
	if m.StagedAt == 0 {
		m.StagedAt = time.Now().Unix()
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] failed to write upgrade marker (automatic rollback capability lost): %v", err)
	}
	log.Printf("[update] swapped to %s, exiting to restart (exit %d)", orUnknown(m.To), ExitRestart)
	return Restart, State{Pending: true}
}

// confirmOrRollback handles "the startup after a swap": counts attempts, and swaps
// the old version back once the limit is exceeded.
//
// The counter only increments after the Go code is running, so it covers failures
// of the "executable but crashes during initialization" kind (incompatible config,
// port occupied, DB migration blew up); "cannot exec at all" is stopped by the
// smoke test before the swap — together they are complete.
func confirmOrRollback(p Paths, m marker) (Action, State) {
	m.Attempts++
	if m.Attempts > maxAttempts {
		if err := rollback(p); err != nil {
			// If even the rollback fails, do not restart again, or we fall into an
			// infinite restart loop. Remove the marker and let the process start
			// as-is — if it cannot come up, the user can at least see why in the logs.
			log.Printf("[update] new version failed to start %d times in a row, and rollback failed too: %v", maxAttempts, err)
			_ = os.Remove(p.Marker)
			return Continue, State{Detail: "new version failed to start and rollback failed: " + err.Error()}
		}
		log.Printf("[update] new version failed to start %d times in a row, rolled back to %s, exiting to restart (exit %d)",
			maxAttempts, orUnknown(m.From), ExitRestart)
		_ = os.Remove(p.Marker)
		return Restart, State{RolledBack: true, Detail: fmt.Sprintf("new version failed to start, rolled back to %s", orUnknown(m.From))}
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] failed to update upgrade marker: %v", err)
	}
	log.Printf("[update] new version starting (attempt %d/%d); the upgrade will be confirmed once it runs stably",
		m.Attempts, maxAttempts)
	return Continue, State{Pending: true}
}

// Settle confirms the new version has been running stably and clears the upgrade marker.
//
// Called by main with a delay after the HTTP listener is up: only surviving that
// period counts; otherwise the marker stays in place and the next startup keeps
// counting attempts until a rollback is triggered.
func Settle() {
	p, err := ResolvePaths()
	if err != nil {
		return
	}
	settle(p)
}

func settle(p Paths) {
	if _, ok := readMarker(p.Marker); !ok {
		return // not a post-upgrade startup, nothing to do
	}
	if err := os.Remove(p.Marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[update] failed to clear upgrade marker: %v", err)
		return
	}
	log.Printf("[update] new version is running stably, upgrade complete (previous version kept as %s)", p.Old)
}

// SettleDelay is the runtime duration required to decide "the new version survived".
const SettleDelay = 30 * time.Second

// verifyStaged verifies the staged file: compare SHA256 first, then actually launch it once.
func verifyStaged(p Paths) error {
	want, err := os.ReadFile(p.Sum)
	if err != nil {
		return fmt.Errorf("failed to read checksum: %w", err)
	}
	got, err := fileSHA256(p.New)
	if err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(string(want)), got) {
		return errors.New("SHA256 mismatch (download corrupted or tampered with)")
	}
	return smokeTest(p.New)
}

// smokeTest launches the new binary with -h to confirm it can really execute on
// the current system. This stops a whole class of problems: truncated downloads,
// wrong architecture (exec format error), missing dependencies, and so on.
func smokeTest(bin string) error {
	if err := os.Chmod(bin, 0o755); err != nil {
		return fmt.Errorf("failed to grant execute permission: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "-h")
	cmd.Env = append(os.Environ(), smokeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return errors.New("smoke test timed out (new binary unresponsive)")
	}
	if err != nil {
		snippet := strings.TrimSpace(string(out))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return fmt.Errorf("smoke test failed: %v: %s", err, snippet)
	}
	return nil
}

// swap replaces the current binary with the staged new version.
//
// Both Unix and Windows allow renaming a running executable (what Windows forbids
// is deleting and overwriting; rename is not on that list), so there is no need to
// split by platform here, nor to stop ourselves first.
func swap(p Paths) error {
	// Windows rename does not overwrite an existing target, so the .old left by the previous upgrade must be removed first.
	if err := os.Remove(p.Old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to clean up old backup %s: %w", p.Old, err)
	}
	if err := os.Rename(p.Current, p.Old); err != nil {
		return fmt.Errorf("failed to back up the current version: %w", err)
	}
	if err := os.Rename(p.New, p.Current); err != nil {
		// The swap failed but the current version has already been moved away; it must be put back as-is, or the next startup has no executable.
		if rerr := os.Rename(p.Old, p.Current); rerr != nil {
			return fmt.Errorf("failed to install the new version (%v), and restoring the current version failed too: %w", err, rerr)
		}
		return fmt.Errorf("failed to install the new version: %w", err)
	}
	_ = os.Remove(p.Sum)
	return nil
}

// rollback swaps back the old version that swap backed up.
func rollback(p Paths) error {
	if _, err := os.Stat(p.Old); err != nil {
		return fmt.Errorf("no backup to roll back to %s: %w", p.Old, err)
	}
	// Move the unstartable new version to .failed for troubleshooting instead of deleting it outright.
	failed := p.Current + ".failed"
	_ = os.Remove(failed)
	if err := os.Rename(p.Current, failed); err != nil {
		return fmt.Errorf("failed to move the failing version away: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		return fmt.Errorf("failed to restore the old version: %w", err)
	}
	return nil
}

// Rollback implements /api/update/rollback: actively return to the previous version.
// It only does the swap; the restart is likewise left to the supervisor script (the caller then exits with ExitRestart).
func Rollback() error {
	p, err := ResolvePaths()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p.Old); err != nil {
		return errors.New("no previous version to roll back to (" + p.Old + " does not exist)")
	}
	cleanStaged(p)
	if err := smokeTest(p.Old); err != nil {
		return fmt.Errorf("the previous version cannot execute, refusing to roll back: %w", err)
	}
	// Swap current and backup: after the rollback you can still roll forward again.
	tmp := p.Current + ".swap"
	_ = os.Remove(tmp)
	if err := os.Rename(p.Current, tmp); err != nil {
		return fmt.Errorf("failed to move the current version away: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		_ = os.Rename(tmp, p.Current)
		return fmt.Errorf("failed to install the previous version: %w", err)
	}
	if err := os.Rename(tmp, p.Old); err != nil {
		log.Printf("[update] failed to tidy up the backup after rollback (does not affect operation): %v", err)
	}
	_ = os.Remove(p.Marker)
	return nil
}

// HasBackup reports whether a previous version exists to roll back to, so the frontend can decide whether to show the rollback button.
func HasBackup() bool {
	p, err := ResolvePaths()
	if err != nil {
		return false
	}
	_, err = os.Stat(p.Old)
	return err == nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown version"
	}
	return s
}
