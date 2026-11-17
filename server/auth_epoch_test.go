package server

import (
	"testing"
	"time"
)

func TestTokenBeforePasswordEpochIsRejected(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	tok, err := signJWT(key)
	if err != nil {
		t.Fatal(err)
	}
	if !verifyJWT(tok, key, 0) {
		t.Fatal("a token with no password epoch must be accepted")
	}
	if verifyJWT(tok, key, time.Now().Add(time.Minute).Unix()) {
		t.Fatal("a token issued before the password epoch must be rejected")
	}
	if !verifyJWT(tok, key, time.Now().Add(-time.Minute).Unix()) {
		t.Fatal("a token issued after the password epoch must be accepted")
	}
}
