package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The bulk endpoint validates the whole request before touching the database, so
// a malformed body, an unknown status and an empty id list are client errors.
func TestBulkFindingStatusValidation(t *testing.T) {
	s := &Server{m: &Manager{}}
	cases := []struct {
		name string
		body string
		want int
	}{
		{"bad json", `{`, http.StatusBadRequest},
		{"unknown status", `{"ids":[1],"status":"nope"}`, http.StatusBadRequest},
		{"empty ids", `{"ids":[],"status":"confirmed"}`, http.StatusBadRequest},
		{"non-positive ids", `{"ids":[0,-3],"status":"confirmed"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/exploration/findings/bulk-status", strings.NewReader(tc.body))
			s.bulkPatchFindingStatus(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
