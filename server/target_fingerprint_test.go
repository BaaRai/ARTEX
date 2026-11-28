package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestTargetFingerprintFactAndWarningsTableDriven(t *testing.T) {
	skipWithoutDSN(t)

	// Cross-host redirect sink: MUST NOT be followed during target fingerprinting.
	var otherHostHits int
	var otherMu sync.Mutex
	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherMu.Lock()
		otherHostHits++
		otherMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer otherSrv.Close()
	otherHost := strings.TrimPrefix(otherSrv.URL, "http://")

	cases := []struct {
		name           string
		goal           string
		injectUserinfo bool
		handler        func(requested *[]string, seenCreds *bool) http.Handler
		wantWarnings   []string
		wantProbes     map[string]bool
		wantPathsOnly  []string
		wantNoCrossHit bool
	}{
		{
			name: "json_root_with_console_goal_warns_and_records_probes",
			goal: "Audit the operator console on ",
			handler: func(requested *[]string, seenCreds *bool) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					*requested = append(*requested, r.URL.Path)
					if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
						*seenCreds = true
					}
					switch r.URL.Path {
					case "/":
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Server", "gablewrick-edge/2.0")
						_, _ = w.Write([]byte(`{"service":"edge","status":"ok"}`))
					case "/login", "/docs":
						w.WriteHeader(http.StatusOK)
					default:
						w.WriteHeader(http.StatusNotFound)
					}
				})
			},
			wantWarnings: []string{"JSON", "console"},
			wantProbes:   map[string]bool{"/login": true, "/signup": false, "/app": false, "/docs": true},
		},
		{
			name:           "non_ctf_normal_target_root_only_no_extra_probes_and_no_creds",
			goal:           "Enumerate endpoints on ",
			injectUserinfo: true,
			handler: func(requested *[]string, seenCreds *bool) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					*requested = append(*requested, r.URL.Path)
					if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
						*seenCreds = true
					}
					if r.URL.Path == "/" {
						w.Header().Set("Content-Type", "text/html")
						_, _ = w.Write([]byte(`<html><head><title>Edge Service</title><link rel="canonical" href="https://main.example.test/app"></head><body><a href="/docs">Docs</a></body></html>`))
						return
					}
					w.WriteHeader(http.StatusNotFound)
				})
			},
			wantWarnings:  []string{"main.example.test"},
			wantPathsOnly: []string{"/"},
		},
		{
			name: "cross_host_redirect_stopped_and_warned",
			goal: "Capture flag 2 on ",
			handler: func(requested *[]string, seenCreds *bool) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					*requested = append(*requested, r.URL.Path)
					if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
						*seenCreds = true
					}
					switch r.URL.Path {
					case "/":
						http.Redirect(w, r, "http://127.0.0.1:"+strings.Split(otherHost, ":")[1]+"/off-scope", http.StatusFound)
					case "/app", "/signup":
						w.WriteHeader(http.StatusFound)
					default:
						w.WriteHeader(http.StatusNotFound)
					}
				})
			},
			wantWarnings:   []string{"canonical/alternate host"},
			wantProbes:     map[string]bool{"/login": false, "/signup": true, "/app": true, "/docs": false},
			wantNoCrossHit: true,
		},
		{
			name: "ctf_estate_control_plane_json_root_all_probes_404_warns_secondary_vhost",
			goal: "Capture 4 flags in order against Estate control plane on ",
			handler: func(requested *[]string, seenCreds *bool) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					*requested = append(*requested, r.URL.Path)
					if r.URL.Path == "/" {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"service":"gablewrick-estate-edge","status":"ok"}`))
						return
					}
					w.WriteHeader(http.StatusNotFound)
				})
			},
			wantWarnings: []string{"possible secondary/service vhost; verify the main instance URL"},
			wantProbes:   map[string]bool{"/login": false, "/signup": false, "/app": false, "/docs": false},
		},
	}

	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if m.pg == nil {
		db.SkipNoPG(t)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, "", m.dir, m.dir)
	t.Cleanup(func() { stopTestServer(t, s, cancel) })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requested []string
			var reqMu sync.Mutex
			var seenCreds bool
			otherMu.Lock()
			otherHostHits = 0
			otherMu.Unlock()

			srv := httptest.NewServer(tc.handler(&requested, &seenCreds))
			defer srv.Close()

			targetURL := srv.URL + "/"
			if tc.injectUserinfo {
				targetURL = strings.Replace(targetURL, "http://", "http://alice:secret@", 1)
			}

			task, err := m.CreateTaskWithOptions("fp-"+tc.name, tc.goal+targetURL, db.TaskCreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) })

			fp, fid, err := s.FingerprintTarget(ctx, task, targetURL)
			if err != nil || fp == nil || fid <= 0 {
				t.Fatalf("FingerprintTarget failed: fp=%+v fid=%d err=%v", fp, fid, err)
			}

			if seenCreds {
				t.Fatalf("target_fingerprint sent Authorization or Cookie headers unexpectedly")
			}
			if strings.Contains(fp.URL, "secret") || strings.Contains(fp.FinalURL, "secret") {
				t.Fatalf("target_fingerprint leaked userinfo credentials: url=%q final=%q", fp.URL, fp.FinalURL)
			}

			reqMu.Lock()
			gotReqs := append([]string(nil), requested...)
			reqMu.Unlock()
			if len(tc.wantPathsOnly) > 0 {
				if len(gotReqs) != len(tc.wantPathsOnly) || gotReqs[0] != tc.wantPathsOnly[0] {
					t.Fatalf("non-CTF target should only request %v, got %v", tc.wantPathsOnly, gotReqs)
				}
				if len(fp.ProbePaths) != 0 {
					t.Fatalf("non-CTF target should not record extra ProbePaths, got %+v", fp.ProbePaths)
				}
			}

			if tc.wantNoCrossHit {
				otherMu.Lock()
				hits := otherHostHits
				otherMu.Unlock()
				if hits != 0 {
					t.Fatalf("expected 0 requests to cross-host redirect target, got %d", hits)
				}
			}

			for path, wantExists := range tc.wantProbes {
				if fp.ProbeExists[path] != wantExists {
					t.Fatalf("probe %s exists=%v, want %v (codes=%+v)", path, fp.ProbeExists[path], wantExists, fp.ProbePaths)
				}
			}

			warnJoined := strings.Join(fp.Warnings, " | ")
			for _, w := range tc.wantWarnings {
				if !strings.Contains(warnJoined, w) {
					t.Fatalf("expected warning containing %q, got %+v", w, fp.Warnings)
				}
			}

			n, err := task.Store.GetNode(fid)
			if err != nil || n == nil {
				t.Fatal(err)
			}
			var payload struct {
				Subtype string `json:"subtype"`
			}
			if err := json.Unmarshal(n.Payload, &payload); err != nil || payload.Subtype != db.FactSubtypeTargetFingerprint {
				t.Fatalf("expected target_fingerprint fact, got %s", string(n.Payload))
			}

			acts, _, err := task.Store.ActivityList(nil, 0, 50)
			if err != nil {
				t.Fatal(err)
			}
			var sawWarnAct bool
			for _, a := range acts {
				if strings.Contains(a.Summary, "Target fingerprint warning:") && !a.IsError {
					sawWarnAct = true
				}
			}
			if len(tc.wantWarnings) > 0 && !sawWarnAct {
				t.Fatalf("expected non-error Target fingerprint warning activity")
			}
		})
	}
}
