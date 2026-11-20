package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestArchiveStoreLoadsCredentialsBeforeSecretScrub(t *testing.T) {
	skipWithoutDSN(t)
	// New scrubs all startup secrets; restore the test database/key environment
	// afterward so the remaining package tests keep their isolated configuration.
	for _, key := range secretEnv {
		t.Setenv(key, os.Getenv(key))
	}
	var signed atomic.Bool
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected S3 operation: %s", r.Method)
		}
		signed.Store(strings.Contains(r.Header.Get("Authorization"), "Credential=archive-fixture-key/") &&
			r.Header.Get("X-Amz-Security-Token") == "archive-fixture-session")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	t.Setenv("ARTEX_ARCHIVE_STORAGE", "s3")
	t.Setenv("ARTEX_ARCHIVE_S3_ENDPOINT", endpoint.URL)
	t.Setenv("ARTEX_ARCHIVE_S3_REGION", "us-east-1")
	t.Setenv("ARTEX_ARCHIVE_S3_BUCKET", "fixture")
	t.Setenv("ARTEX_ARCHIVE_S3_PREFIX", "archives")
	t.Setenv("ARTEX_ARCHIVE_S3_PATH_STYLE", "true")
	t.Setenv("ARTEX_ARCHIVE_S3_ACCESS_KEY_ID", "archive-fixture-key")
	t.Setenv("ARTEX_ARCHIVE_S3_SECRET_ACCESS_KEY", "archive-fixture-secret")
	t.Setenv("ARTEX_ARCHIVE_S3_SESSION_TOKEN", "archive-fixture-session")
	t.Setenv("ARTEX_ARCHIVE_S3_SSE", "AES256")
	t.Setenv("ARTEX_ARCHIVE_S3_KMS_KEY_ID", "")
	// If initialization happens after the scrub, the SDK would silently select
	// this different ambient identity instead of the dedicated override.
	t.Setenv("AWS_ACCESS_KEY_ID", "ambient-fixture-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-fixture-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	t.Setenv("AWS_CA_BUNDLE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "no-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Exercise real startup without leaving archive/scheduler workers alive.
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir(), WithSecretScrub())
	s.archiveWG.Wait()
	if s.archiveStoreErr != nil || s.archiveStore == nil {
		t.Fatalf("S3 initialization failed: %v", s.archiveStoreErr)
	}
	for _, key := range []string{"ARTEX_ARCHIVE_S3_ACCESS_KEY_ID", "ARTEX_ARCHIVE_S3_SECRET_ACCESS_KEY", "ARTEX_ARCHIVE_S3_SESSION_TOKEN"} {
		if _, present := os.LookupEnv(key); present {
			t.Fatalf("%s remains in the child-process environment", key)
		}
	}
	requestCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := s.archiveStore.Delete(requestCtx, s.archiveStore.URI(1, 2, "startup-probe")); err != nil {
		t.Fatal(err)
	}
	if !signed.Load() {
		t.Fatal("archive client lost its dedicated credentials during startup scrub")
	}
}
