//go:build unix

package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// sharedBrowserStaleLockFiles are Chromium profile lock/port entries cleared
// inside the scope directory before relaunching.
var sharedBrowserStaleLockFiles = []string{
	"SingletonLock",
	"SingletonSocket",
	"SingletonCookie",
	"DevToolsActivePort",
}

// prepareSharedBrowserProfileDir creates and secures the isolated profile
// directory <root>/<scope> without walking children or following symlinks.
//
// Security (matches execpriv_unix.go):
//   - <root> (<data>/browser-profiles) stays root:root 0755 when privilege drop
//     is enabled, so the executor cannot rename or replace <scope> inside it.
//   - Both <root> and <scope> are opened with O_RDONLY|O_DIRECTORY|O_NOFOLLOW
//     (and <scope> relative to rootFD via openat), so a planted symlink at
//     <root> or <scope> is rejected with ELOOP before any fchown or unlinkat.
//   - Ownership is changed only via fchown on the verified directory descriptor
//     — never via filepath.Walk over executor-owned children, which would allow
//     an intermediate-component symlink swap race into root-only paths.
//   - Stale Chromium lock files are removed via unlinkat on scopeFD.
func prepareSharedBrowserProfileDir(root, scope string, priv ExecPrivilege) (string, error) {
	scope = sanitizeProfileScope(scope)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", fmt.Errorf("open profile root %q: %w", root, err)
	}
	defer unix.Close(rootFD)

	if priv.Enabled {
		// Keep root owned by root:root 0755 so the executor can traverse to its
		// scope directory but cannot swap <scope> for a symlink.
		if err := unix.Fchown(rootFD, 0, 0); err != nil {
			return "", fmt.Errorf("chown profile root %q: %w", root, err)
		}
		if err := unix.Fchmod(rootFD, 0o755); err != nil {
			return "", fmt.Errorf("chmod profile root %q: %w", root, err)
		}
	}

	if err := unix.Mkdirat(rootFD, scope, 0o700); err != nil && !errors.Is(err, syscall.EEXIST) && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("mkdir scope %q: %w", scope, err)
	}
	scopeFD, err := unix.Openat(rootFD, scope, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", fmt.Errorf("open profile scope %q (symlink refused): %w", scope, err)
	}
	defer unix.Close(scopeFD)

	if priv.Enabled {
		// Fchown ONLY the verified scope directory descriptor; Chromium runs as
		// the executor and creates all children under it with executor ownership.
		if err := unix.Fchown(scopeFD, int(priv.UID), int(priv.GID)); err != nil {
			return "", fmt.Errorf("chown profile scope %q: %w", scope, err)
		}
		if err := unix.Fchmod(scopeFD, 0o700); err != nil {
			return "", fmt.Errorf("chmod profile scope %q: %w", scope, err)
		}
	}

	// Remove stale lock files relative to the verified scopeFD (never via
	// path-based os.Remove that could traverse an intermediate symlink).
	for _, f := range sharedBrowserStaleLockFiles {
		_ = unix.Unlinkat(scopeFD, f, 0)
	}
	return filepath.Join(root, scope), nil
}
