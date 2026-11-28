//go:build !unix

package server

import (
	"fmt"
	"os"
	"path/filepath"
)

var sharedBrowserStaleLockFiles = []string{
	"SingletonLock",
	"SingletonSocket",
	"SingletonCookie",
	"DevToolsActivePort",
}

func prepareSharedBrowserProfileDir(root, scope string, _ ExecPrivilege) (string, error) {
	scope = sanitizeProfileScope(scope)
	if fi, err := os.Lstat(root); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("profile root %q must not be a symlink", root)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	profileDir := filepath.Join(root, scope)
	if fi, err := os.Lstat(profileDir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("profile scope %q must not be a symlink", scope)
	}
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return "", err
	}
	for _, f := range sharedBrowserStaleLockFiles {
		_ = os.Remove(filepath.Join(profileDir, f))
	}
	return profileDir, nil
}
