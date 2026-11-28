package server

import "testing"

func TestCurrentBuildInfoUsesInjectedIdentity(t *testing.T) {
	bi := currentBuildInfo("main-test", "0123456789abcdef")
	if bi.Version != "main-test" {
		t.Fatalf("Version = %q, want main-test", bi.Version)
	}
	if bi.Commit != "0123456789abcdef" {
		t.Fatalf("Commit = %q, want the injected revision", bi.Commit)
	}
	if bi.GoVersion == "" {
		t.Fatal("GoVersion is empty; debug.ReadBuildInfo should report the toolchain")
	}
}

func TestCurrentBuildInfoDevWithoutRevision(t *testing.T) {
	bi := currentBuildInfo("dev", "")
	if bi.Version != "dev" {
		t.Fatalf("Version = %q, want dev", bi.Version)
	}
	// A test binary has no VCS stamp, so the empty commit stays empty rather
	// than turning into a misleading value.
	if bi.Commit != "" {
		t.Fatalf("Commit = %q, want empty when the build has no revision", bi.Commit)
	}
}
