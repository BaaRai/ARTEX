// Package selfupdate implements ARTEX's one-click update from the web UI: it pulls
// the new binary from a GitHub Release, verifies and stages it, and swaps it in atomically on the next start.
//
// Overall division of labor (see start.sh / start.bat):
//
//	Start script = a dumb supervision loop, responsible only for "deciding whether to relaunch the process after it exits, based on its exit code"
//	This package = all the error-prone logic (download / SHA256 verify / smoke test / swap / rollback on failure)
//
// The swap lives in Go rather than the scripts because sha256 verification and the smoke test would need two implementations in sh and bat,
// (sha256sum / shasum / certutil), and this is precisely the step that must never fail — swap in a binary that cannot run,
// and the supervisor will faithfully relaunch it forever, leaving the user to rescue things on the machine by hand.
//
// A full upgrade goes through three process starts:
//
//	(1) the old server receives /api/update/apply → download and verify → stage artex.new → exit 75
//	(2) the script relaunches the old version → Bootstrap spots artex.new → verify + smoke test → swap → exit 75
//	(3) the script relaunches again, now the new version → Bootstrap records one attempt → clears the marker after a successful start
//
// Any failed step falls back to the old version: in (2) a failed verification deletes the staged file and keeps the old version running; in (3) three
// consecutive crashes before the marker is cleared mean the new version cannot start, so artex.old is swapped back automatically.
package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ExitRestart is the "please relaunch me" exit code (EX_TEMPFAIL). When the start script sees it,
// it reruns immediately without counting toward crash backoff. 0 means the user stopped it normally (the script exits the loop); anything else is treated as a crash.
const ExitRestart = 75

// maxAttempts is the number of start attempts allowed after the swap. Every start of the new version increments the counter; surviving
// settleDelay clears the marker; maxAttempts consecutive crashes mean the new version simply cannot start, so it rolls back automatically.
const maxAttempts = 3

// Paths lists every file an upgrade touches, all anchored under **the executable's directory**.
// CWD is deliberately avoided: when running as a service the working directory may be / or any path, and using CWD would drop the staged files
// somewhere else, breaking the swap logic outright.
type Paths struct {
	Dir     string // directory of the executable
	Current string // currently running binary      artex      / artex.exe
	New     string // staged new version            artex.new  / artex.new.exe
	Sum     string // new version sha256 (hex)      artex.new.sha256 / artex.new.exe.sha256
	Old     string // pre-swap backup of old version artex.old  / artex.old.exe
	Marker  string // upgrade state marker           artex.upgrade.json
}

// ResolvePaths derives all upgrade paths from the current executable.
//
// On Windows .new/.old must also carry the .exe suffix, otherwise both the smoke test and post-swap execution fail,
// so the suffix is stripped off first and re-appended, keeping the naming symmetric across the two platforms.
func ResolvePaths() (Paths, error) {
	exe, err := os.Executable()
	if err != nil {
		return Paths{}, fmt.Errorf("locate executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	name := filepath.Base(exe)
	ext := filepath.Ext(name) // ".exe" on Windows, usually empty on Unix
	stem := strings.TrimSuffix(name, ext)

	join := func(suffix string) string { return filepath.Join(dir, stem+suffix+ext) }
	return Paths{
		Dir:     dir,
		Current: exe,
		New:     join(".new"),
		Sum:     join(".new") + ".sha256",
		Old:     join(".old"),
		Marker:  filepath.Join(dir, stem+".upgrade.json"),
	}, nil
}

// marker records the progress of one swap and triggers automatic rollback when the new version cannot start.
type marker struct {
	From     string `json:"from"`     // version before the upgrade
	To       string `json:"to"`       // target version
	Attempts int    `json:"attempts"` // start attempts made after the swap
	StagedAt int64  `json:"staged_at"`
}

func readMarker(path string) (marker, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return marker{}, false
	}
	var m marker
	if json.Unmarshal(b, &m) != nil {
		return marker{}, false
	}
	return m, true
}

func writeMarker(path string, m marker) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// cleanStaged removes the staged files. Successful swap, failed verification, and user cancellation all go through it, so a leftover
// artex.new is not retried on the next start.
func cleanStaged(p Paths) {
	_ = os.Remove(p.New)
	_ = os.Remove(p.Sum)
}

// CompareVersions compares two version numbers and returns -1/0/1 (a<b / a==b / a>b).
// ok=false means at least one side is not a comparable version number (such as a local dev build's "dev" or
// a git describe output like "0.3.7-2-gabc1234-dirty"); the caller must disable one-click update then,
// otherwise a dev build would be "upgraded" to a release, overwriting uncommitted changes.
func CompareVersions(a, b string) (int, bool) {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return 0, false
	}
	for i := range 3 {
		if av[i] != bv[i] {
			if av[i] < bv[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// parseVersion parses a "v0.3.7" / "0.3.7" style version number into [3]int.
//
// Only a clean three-part form is accepted: for non-tag builds build.sh produces suffixed
// versions via git describe like "0.3.7-2-gabc1234"; those must be judged non-comparable rather than treated as
// 0.3.7 — otherwise a dev build gets misjudged as "already latest" or overwritten by a release.
func parseVersion(s string) ([3]int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return [3]int{}, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// InDocker reports whether the process runs inside a container. Under Docker the swap writes to the container's writable layer,
// and `docker compose up -d` recreating the container reverts to the version bundled in the image — that is expected behavior
// (the user is pulling a new image at that point anyway), but the frontend needs to be able to explain it clearly.
func InDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd")
}
