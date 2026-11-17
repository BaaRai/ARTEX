package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeAcceptsSmallBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"x":"ok"}`))
	var v map[string]string
	if err := decode(r, &v); err != nil || v["x"] != "ok" {
		t.Fatalf("decode: %v %+v", err, v)
	}
}

func TestDecodeCapsBody(t *testing.T) {
	body := strings.NewReader(`{"x":"` + strings.Repeat("a", decodeMaxBytes) + `"}`)
	r := httptest.NewRequest(http.MethodPost, "/", body)
	var v map[string]string
	if err := decode(r, &v); err == nil {
		t.Fatal("body over the limit was accepted")
	}
}
