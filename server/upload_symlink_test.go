package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRefuseUploadSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secret")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "upload")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := refuseUploadSymlink(link); err == nil {
		t.Fatal("symlink destination was accepted")
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "keep" {
		t.Fatalf("target changed to %q", body)
	}
	missing := filepath.Join(dir, "fresh")
	if err := refuseUploadSymlink(missing); err != nil {
		t.Fatal(err)
	}
}
