package traffic

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/accessaudit"
)

func TestExecutionAuditMalformedCredentialClearsConnectionIdentity(t *testing.T) {
	tr, _, addr := startProxy(t)
	tr.SetCaptureEnabled(false)
	audit := executionAuditStore(t, tr)
	leased, _, release := tr.ExecutionProxy(accessaudit.Identity{TaskID: "81"})
	defer release()
	credential, _ := url.Parse(leased)
	password, _ := credential.User.Password()
	authorization := "Basic " + base64.StdEncoding.EncodeToString([]byte(credential.User.Username()+":"+password))
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credential reached target")
		}
		w.WriteHeader(204)
	}))
	defer target.Close()
	client := proxyClient(t, tr, addr)
	for i, auth := range []string{authorization, "Bearer malformed"} {
		path := "/known"
		if i == 1 {
			path = "/invalid"
		}
		req, _ := http.NewRequest("GET", target.URL+path, nil)
		req.Header.Set("Proxy-Authorization", auth)
		reused := false
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}))
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if i == 1 && !reused {
			t.Fatal("test did not reuse the authenticated proxy connection")
		}
	}
	for _, event := range executionEvents(t, audit) {
		if strings.Contains(event.URL, "/known") && event.TaskID != "81" {
			t.Fatal("registered identity missing")
		}
		if strings.Contains(event.URL, "/invalid") && event.TaskID != "" {
			t.Fatal("malformed credential inherited previous identity")
		}
	}
}
