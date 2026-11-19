package archivestore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isolateAWSCredentials(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE",
		"AWS_ROLE_ARN", "AWS_ROLE_SESSION_NAME", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN", "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE",
		"AWS_EC2_METADATA_SERVICE_ENDPOINT", "AWS_EC2_METADATA_SERVICE_ENDPOINT_MODE",
	} {
		t.Setenv(key, "")
	}
	dir := t.TempDir()
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

func TestArchiveCredentialChain(t *testing.T) {
	config := Config{Endpoint: "https://objects.example.test", Bucket: "test"}
	for _, mode := range []string{"explicit-session", "environment", "shared-profile", "ecs", "imds"} {
		t.Run(mode, func(t *testing.T) {
			isolateAWSCredentials(t)
			c := config
			switch mode {
			case "explicit-session":
				t.Setenv("AWS_ACCESS_KEY_ID", "other-key")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "other-secret")
				c.AccessKey, c.SecretKey, c.SessionToken = "fixture-key", "fixture-secret", "fixture-session"
			case "environment":
				t.Setenv("AWS_ACCESS_KEY_ID", "fixture-key")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "fixture-secret")
				t.Setenv("AWS_SESSION_TOKEN", "fixture-session")
			case "shared-profile":
				t.Setenv("AWS_PROFILE", "archive")
				data := `[archive]
aws_access_key_id=fixture-key
aws_secret_access_key=fixture-secret
aws_session_token=fixture-session
`
				if err := os.WriteFile(os.Getenv("AWS_SHARED_CREDENTIALS_FILE"), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			case "ecs", "imds":
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if mode == "imds" {
						switch r.URL.Path {
						case "/latest/api/token":
							if r.Method != "PUT" {
								t.Error("IMDS did not use token request")
							}
							w.Header().Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
							fmt.Fprint(w, "fixture-imds-token")
							return
						case "/latest/meta-data/iam/security-credentials/":
							if r.Header.Get("X-aws-ec2-metadata-token") != "fixture-imds-token" {
								t.Error("missing IMDSv2 token")
							}
							fmt.Fprint(w, "archive-role")
							return
						case "/latest/meta-data/iam/security-credentials/archive-role":
							if r.Header.Get("X-aws-ec2-metadata-token") != "fixture-imds-token" {
								t.Error("missing IMDSv2 token")
							}
						default:
							t.Errorf("unexpected IMDS request %s", r.URL.Path)
							w.WriteHeader(404)
							return
						}
					} else if r.Header.Get("Authorization") != "fixture-container-token" {
						t.Error("missing ECS authorization token")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"Code": "Success", "AccessKeyId": "fixture-key", "SecretAccessKey": "fixture-secret",
						"Token": "fixture-session", "Expiration": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
					})
				}))
				defer srv.Close()
				if mode == "imds" {
					t.Setenv("AWS_EC2_METADATA_DISABLED", "false")
					t.Setenv("AWS_EC2_METADATA_V1_DISABLED", "true")
					t.Setenv("AWS_EC2_METADATA_SERVICE_ENDPOINT", srv.URL)
				} else {
					t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", srv.URL+"/credentials")
					t.Setenv("AWS_CONTAINER_AUTHORIZATION_TOKEN", "fixture-container-token")
				}
			}
			store, err := New(c)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			credentials, err := store.client.Options().Credentials.Retrieve(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if credentials.AccessKeyID != "fixture-key" || credentials.SecretAccessKey != "fixture-secret" || credentials.SessionToken != "fixture-session" {
				t.Fatal("wrong credential provider or session token")
			}
			if (mode == "imds" || mode == "ecs") && !credentials.CanExpire {
				t.Fatal("role credentials must remain refreshable")
			}
		})
	}
}

func TestArchiveIncompleteCredentialOverride(t *testing.T) {
	isolateAWSCredentials(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "valid-ambient-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "valid-ambient-secret")
	for _, c := range []Config{{AccessKey: "private-value"}, {SecretKey: "private-value"}, {SessionToken: "private-value"}} {
		c.Endpoint, c.Bucket = "https://objects.example.test", "test"
		if _, err := New(c); err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("partial override must fail without disclosing credentials: %v", err)
		}
	}
}
