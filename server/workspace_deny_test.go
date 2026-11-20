package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceDeniesPlatformPaths(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"traffic/_ca", "traffic/_index", "traffic/host", "other"} {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(dir, "traffic", "_ca", "mitmproxy-ca.pem")
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: &Manager{dir: dir}}

	for _, rel := range []string{
		"traffic/_ca",
		"traffic/_ca/mitmproxy-ca.pem",
		"traffic/_index",
		"traffic/_index/index.sqlite",
		"TRAFFIC/_CA/mitmproxy-ca.pem",
		"traffic/_ca.",
		"traffic/_ca./mitmproxy-ca.pem",
	} {
		if _, ok := s.wsResolveConfined(rel); ok {
			t.Errorf("%q resolved, want a refusal", rel)
		}
	}

	for _, rel := range []string{"", "traffic", "traffic/host", "traffic/_caX", "other"} {
		if _, ok := s.wsResolveConfined(rel); !ok {
			t.Errorf("%q refused, want a resolution", rel)
		}
	}

	link := filepath.Join(dir, "alias")
	if err := os.Symlink(filepath.Join(dir, "traffic", "_ca"), link); err == nil {
		if _, ok := s.wsResolveConfined("alias/mitmproxy-ca.pem"); ok {
			t.Error("symlink alias to traffic/_ca resolved")
		}
	}
}

func TestWorkspacePathDenied(t *testing.T) {
	root := filepath.Clean("/data")
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/data/traffic/_ca", true},
		{"/data/traffic/_ca/x", true},
		{"/data/traffic/_index/index.sqlite", true},
		{"/data/traffic/_caX", false},
		{"/data/traffic", false},
		{"/data/other", false},
		{"/elsewhere/traffic/_ca", true}, // outside the root: fail closed
	} {
		if got := workspacePathDenied(root, filepath.FromSlash(tc.path)); got != tc.want {
			t.Errorf("workspacePathDenied(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
