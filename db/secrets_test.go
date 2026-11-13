package db

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var testSecretKey = []byte("0123456789abcdef0123456789abcdef")

func TestSecretBoxCrypto(t *testing.T) {
	b, _ := newSecretBox(testSecretKey)
	c1, err := b.encrypt("t.c", "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := b.encrypt("t.c", "hunter2")
	if !isEncrypted(c1) || isEncrypted("hunter2") || c1 == c2 || strings.Contains(c1, "hunter2") {
		t.Fatalf("want distinct enc:v1: ciphertexts, got %q %q", c1, c2)
	}
	if p, err := b.decrypt("t.c", c1); err != nil || p != "hunter2" {
		t.Fatalf("round trip: %q %v", p, err)
	}
	if _, err := b.decrypt("t.other", c1); err == nil {
		t.Fatal("wrong AAD must fail")
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(c1, secretPrefix))
	raw[len(raw)-1] ^= 1
	if _, err := b.decrypt("t.c", secretPrefix+base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
	if v, _ := b.encrypt("t.c", ""); v != "" {
		t.Fatal("empty must stay empty")
	}
	sealed, err := b.seal(colMCPEnv, `{"b":1, "a":"tok"}`)
	if err != nil || !isSealed(colMCPEnv, sealed) || strings.Contains(sealed, "tok") {
		t.Fatalf("not sealed: %s %v", sealed, err)
	}
	if p, err := b.open(colMCPEnv, sealed); err != nil || p != `{"a":"tok","b":1}` {
		t.Fatalf("canonical JSON round trip: %q %v", p, err)
	}
	if _, err := b.seal(colMCPEnv, `{"__artex_enc":"x"}`); err == nil {
		t.Fatal("reserved key must be refused")
	}
	if out, _ := b.seal(colMCPEnv, `{}`); out != `{}` {
		t.Fatalf("empty object must stay empty, got %s", out)
	}
}

type secretRows struct{ llm, mcp, cred, ch, task int64 }

var plainSecrets = []string{"sk-plain-llm", "mcp-plain-tok", "plain-pass", "hook-plain-secret"}

// secretFixture writes one secret to each location, through the db API or, for
// a legacy database, as raw plaintext SQL.
func secretFixture(t *testing.T, d *DB, legacy bool) (r secretRows) {
	t.Helper()
	task, err := d.CreateTask("secrets at rest", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	r.task = task.ID
	t.Cleanup(func() {
		_ = d.DeleteTask(r.task)
		d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, r.llm)
		d.Exec(`DELETE FROM mcp_servers WHERE id=$1`, r.mcp)
		d.Exec(`DELETE FROM notification_channels WHERE id=$1`, r.ch)
	})
	env, cfg := `{"Authorization":"mcp-plain-tok"}`, `{"url":"https://h.example","headers":{"X":"hook-plain-secret"}}`
	if legacy {
		ins := func(id *int64, q string, args ...any) {
			if err := d.QueryRow(q, args...).Scan(id); err != nil {
				t.Fatal(err)
			}
		}
		ins(&r.llm, `INSERT INTO llm_profiles(name,format,model,api_key) VALUES ('t-sec','openai','m','sk-plain-llm') RETURNING id`)
		ins(&r.mcp, `INSERT INTO mcp_servers(name,transport,env) VALUES ('t-sec','http',$1) RETURNING id`, env)
		ins(&r.cred, `INSERT INTO task_credentials(task_id,domain,username,password) VALUES ($1,'x.example','u','plain-pass') RETURNING id`, r.task)
		ins(&r.ch, `INSERT INTO notification_channels(name,kind,config) VALUES ('t-sec','webhook',$1) RETURNING id`, cfg)
		return r
	}
	r.llm, err = d.SaveProfile(&LLMProfile{Name: "t-sec", Format: "openai", Model: "m", APIKey: "sk-plain-llm"})
	if err == nil {
		r.mcp, err = d.SaveMCP(&MCPServer{Name: "t-sec", Transport: "http", Env: json.RawMessage(env)})
	}
	if err == nil {
		var c TaskCredential
		c, err = d.UpsertTaskCredential(r.task, TaskCredentialInput{Domain: "x.example", Username: "u", Password: "plain-pass"})
		r.cred = c.ID
	}
	if err == nil {
		r.ch, err = d.SaveNotificationChannel(context.Background(), &NotificationChannel{Name: "t-sec", Kind: "webhook", Config: json.RawMessage(cfg)})
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// rawSecrets reads the four columns as stored in PostgreSQL.
func rawSecrets(t *testing.T, d *DB, r secretRows) string {
	t.Helper()
	var a, b, c, e string
	if err := d.QueryRow(`SELECT (SELECT api_key FROM llm_profiles WHERE id=$1), (SELECT env::text FROM mcp_servers WHERE id=$2),
(SELECT password FROM task_credentials WHERE id=$3), (SELECT config::text FROM notification_channels WHERE id=$4)`,
		r.llm, r.mcp, r.cred, r.ch).Scan(&a, &b, &c, &e); err != nil {
		t.Fatal(err)
	}
	if !isEncrypted(a) || !isSealed(colMCPEnv, b) || !isEncrypted(c) || !isSealed(colNotifConfig, e) {
		t.Fatal("a stored secret is not encrypted")
	}
	all := strings.Join([]string{a, b, c, e}, "|")
	for _, p := range plainSecrets {
		if strings.Contains(all, p) {
			t.Fatalf("plaintext %q found at rest", p)
		}
	}
	return all
}

// assertPlainReads checks that every db read path returns the decrypted secret.
func assertPlainReads(t *testing.T, d *DB, r secretRows) {
	t.Helper()
	got := ""
	if p, err := d.ProfileByID(r.llm); err == nil && p != nil {
		got += p.APIKey
	}
	ms, _ := d.ListMCP()
	for _, m := range ms {
		if m.ID == r.mcp {
			got += string(m.Env)
		}
	}
	if creds, err := d.TaskCredentials(r.task); err == nil && len(creds) == 1 {
		got += creds[0].Password
	}
	if ch, err := d.NotificationChannelByID(context.Background(), r.ch); err == nil {
		got += string(ch.Config)
	}
	for _, p := range plainSecrets {
		if !strings.Contains(got, p) {
			t.Fatalf("%q not read back in clear", p)
		}
	}
}

func TestSecretsAtRestWriteRead(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() }) // after the fixture cleanup
	r := secretFixture(t, d, false)
	rawSecrets(t, d, r)
	assertPlainReads(t, d, r)
	if _, err := d.SaveMCP(&MCPServer{Name: "t-sec-reserved", Transport: "http", Env: json.RawMessage(`{"__artex_enc":"x"}`)}); err == nil {
		t.Fatal("reserved key must be refused on write")
	}
}

func TestSecretsBackfillLegacyAndKeyChecks(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() }) // after the fixture cleanup
	r := secretFixture(t, d, true)
	t.Setenv(secretKeyEnv, base64.StdEncoding.EncodeToString(testSecretKey))
	if err := d.InitSecrets(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	first := rawSecrets(t, d, r)
	assertPlainReads(t, d, r)
	if err := d.InitSecrets(t.TempDir()); err != nil || rawSecrets(t, d, r) != first {
		t.Fatalf("second backfill must leave ciphertexts untouched (%v)", err)
	}

	// Encrypted data, no env key, no key file: refuse, and write no new key.
	t.Setenv(secretKeyEnv, "")
	dir := t.TempDir()
	if err := d.InitSecrets(dir); err == nil || !strings.Contains(err.Error(), "no key is available") {
		t.Fatalf("want refusal without a key, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, secretKeyFile)); !os.IsNotExist(err) {
		t.Fatal("a new key was generated over encrypted data")
	}
	t.Setenv(secretKeyEnv, hex.EncodeToString([]byte("ffffffffffffffffffffffffffffffff")))
	if err := d.InitSecrets(dir); err == nil || !strings.Contains(err.Error(), "secret key check") {
		t.Fatalf("want wrong-key refusal, got %v", err)
	}
	assertPlainReads(t, d, r) // the active key is unchanged
}
