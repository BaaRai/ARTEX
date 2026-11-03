package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgresDSNPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	os.WriteFile(cfgPath, []byte(`{"database":{"host":"10.1.2.3","port":6000,"user":"u","password":"p","dbname":"d","sslmode":"require"}}`), 0o644)
	t.Setenv("ARTEX_CONFIG", cfgPath)

	// no env DSN → built from config file fields
	t.Setenv("ARTEX_PG_DSN", "")
	got, _, err := PostgresDSN()
	want := "postgres://u:p@10.1.2.3:6000/d?sslmode=require"
	if err != nil || got != want {
		t.Fatalf("from file: got %q err %v want %q", got, err, want)
	}

	// env wins over config file
	t.Setenv("ARTEX_PG_DSN", "postgres://envwins/x")
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://envwins/x" {
		t.Fatalf("env should win, got %q err %v", got, err)
	}

	// no env, no file → error (no built-in default)
	t.Setenv("ARTEX_PG_DSN", "")
	t.Setenv("ARTEX_CONFIG", filepath.Join(dir, "nope.json"))
	if got, _, err := PostgresDSN(); err == nil {
		t.Fatalf("missing config should error, got %q", got)
	}

	// full dsn in config file is used verbatim
	os.WriteFile(cfgPath, []byte(`{"database":{"dsn":"postgres://full/dsn"}}`), 0o644)
	t.Setenv("ARTEX_CONFIG", cfgPath)
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://full/dsn" {
		t.Fatalf("file dsn verbatim, got %q err %v", got, err)
	}
}

// A config file that exists but is not valid JSON must fail loudly, not run with
// defaults (or report "no database config") as if the file were absent.
func TestMalformedConfigIsAnError(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	// What install.sh used to write for a password containing a double quote.
	os.WriteFile(cfgPath, []byte(`{"database":{"host":"h","password":"a"b","dbname":"d"},"skill_dir":"/s"}`), 0o644)
	t.Setenv("ARTEX_CONFIG", cfgPath)
	t.Setenv("ARTEX_PG_DSN", "")
	t.Setenv("ARTEX_SKILL_DIR", "")

	if _, _, err := PostgresDSN(); err == nil || !strings.Contains(err.Error(), "parse config file") {
		t.Fatalf("PostgresDSN: want a parse error, got %v", err)
	}
	if d, err := SkillDir(); err == nil {
		t.Fatalf("SkillDir: want a parse error, got dir %q", d)
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load: want a parse error")
	}
}
