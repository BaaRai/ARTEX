package chatgptplan

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type catalogTokenSource struct {
	token string
	err   error
}

func (s catalogTokenSource) AccessToken(context.Context) (string, error) {
	return s.token, s.err
}

func catalogResponse(status int, body string, header http.Header) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: header}
}

func TestListModelsUsesSIWCCatalogAndPreservesOrder(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodGet || req.URL.String() != "https://api.openai.com/v1/models" {
			t.Errorf("unexpected catalog request: %s %s", req.Method, req.URL)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer private-token" {
			t.Errorf("authorization header = %q", got)
		}
		return catalogResponse(http.StatusOK, `{"models":[
			{"slug":"hidden","display_name":"Hidden","visibility":"hidden"},
			{"slug":"gpt-6.2-sol","display_name":"GPT 6.2 Sol","visibility":"list"},
			{"slug":"gpt-6.1-sol","display_name":"GPT 6.1 Sol","visibility":"list"},
			{"slug":"internal","display_name":"Internal","visibility":"internal"}]}`, nil), nil
	})}
	got, err := listModels(context.Background(), catalogTokenSource{token: "private-token"}, client)
	if err != nil {
		t.Fatal(err)
	}
	want := []Model{{Slug: "gpt-6.2-sol", DisplayName: "GPT 6.2 Sol"}, {Slug: "gpt-6.1-sol", DisplayName: "GPT 6.1 Sol"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
	if calls != 1 {
		t.Fatalf("catalog requests = %d, want 1", calls)
	}
}

func TestListModelsNeverFollowsRedirect(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return catalogResponse(http.StatusFound, "redirected", http.Header{"Location": {"https://example.invalid/steal"}}), nil
	})}
	_, err := listModels(context.Background(), catalogTokenSource{token: "private-token"}, client)
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("error = %v, want HTTP 302", err)
	}
	if calls != 1 {
		t.Fatalf("redirect caused %d requests", calls)
	}
}

func TestListModelsBoundsAndValidatesResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"missing models", `{}`, "models array"},
		{"null models", `{"models":null}`, "models array"},
		{"invalid models", `{"models":{}}`, "models array"},
		{"invalid json", `{"models":[`, "decode"},
		{"too large", strings.Repeat(" ", maxModelCatalogBytes+1), "too large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return catalogResponse(http.StatusOK, tc.body, nil), nil
			})}
			_, err := listModels(context.Background(), catalogTokenSource{token: "private-token"}, client)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestListModelsDoesNotExposeTokenInErrors(t *testing.T) {
	secret := "private-token"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return catalogResponse(http.StatusForbidden, secret, nil), nil
	})}
	_, err := listModels(context.Background(), catalogTokenSource{token: secret}, client)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked token: %v", err)
	}
	_, err = listModels(context.Background(), catalogTokenSource{err: errors.New("sign-in required")}, client)
	if err == nil || !strings.Contains(err.Error(), "sign-in required") {
		t.Fatalf("token source error = %v", err)
	}
}
