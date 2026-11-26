package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestSkillEndpointsHideDriverErrors(t *testing.T) {
	raw, err := sql.Open("pgx", "postgres://127.0.0.1:1/none?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: &Manager{pg: &db.DB{DB: raw}}}

	cases := []struct {
		name string
		req  *http.Request
		h    http.HandlerFunc
		want string
	}{
		{
			name: "usage",
			req:  skillErrReq(http.MethodGet, "/api/skills/recon/usage", "name", "recon", ""),
			h:    s.fsSkillUsage,
			want: "failed to load skill usage",
		},
		{
			name: "missing",
			req:  httptest.NewRequest(http.MethodGet, "/api/skills/missing", nil),
			h:    s.fsMissingSkills,
			want: "failed to load missing skills",
		},
		{
			name: "visibility",
			req:  skillErrReq(http.MethodGet, "/api/visibility/skill/recon", "name", "recon", ""),
			h:    s.pgSkillVisibility,
			want: "failed to load skill visibility",
		},
		{
			name: "toggle",
			req:  skillErrReq(http.MethodPost, "/api/visibility/skill/toggle", "", "", `{"agent_id":"1","skill_name":"recon","visible":true}`),
			h:    s.pgToggleSkillVisibility,
			want: "failed to update skill visibility",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.h(rec, tc.req)
			if rec.Code != 500 {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["error"] != tc.want || strings.Contains(rec.Body.String(), "database") {
				t.Fatalf("body = %s, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}

func skillErrReq(method, target, key, val, jsonBody string) *http.Request {
	var r *http.Request
	if jsonBody == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(jsonBody))
	}
	if key != "" {
		r.SetPathValue(key, val)
	}
	return r
}
