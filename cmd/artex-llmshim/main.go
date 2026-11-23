package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type config struct {
	addr      string
	upstream  string
	tokenFile string
}

// loadToken resolves the OAuth token. A -token-file wins when set (read + trim,
// error on read failure or empty); otherwise ARTEX_SHIM_TOKEN is used. When
// both are empty it returns an error.
func loadToken(tokenFile string) (string, error) {
	if tokenFile != "" {
		b, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", err
		}
		tok := strings.TrimSpace(string(b))
		if tok == "" {
			return "", errors.New("token file is empty: " + tokenFile)
		}
		return tok, nil
	}

	if tok := os.Getenv("ARTEX_SHIM_TOKEN"); tok != "" {
		return tok, nil
	}

	return "", errors.New("no token: set ARTEX_SHIM_TOKEN or -token-file")
}

func run(cfg config) error {
	token, err := loadToken(cfg.tokenFile)
	if err != nil {
		return err
	}

	upstream, err := url.Parse(cfg.upstream)
	if err != nil {
		return err
	}

	proxy := newProxy(upstream, token)
	log.Printf("llmshim: listening on %s, forwarding to %s", cfg.addr, upstream)
	return http.ListenAndServe(cfg.addr, logRequests(proxy))
}

func main() {
	var cfg config
	flag.StringVar(&cfg.addr, "addr", "127.0.0.1:8788", "listen address (loopback by default)")
	flag.StringVar(&cfg.upstream, "upstream", "https://api.anthropic.com", "upstream base URL")
	flag.StringVar(&cfg.tokenFile, "token-file", "", "path to a file holding the OAuth token (overrides ARTEX_SHIM_TOKEN)")
	flag.Parse()

	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}
