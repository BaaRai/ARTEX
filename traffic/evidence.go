package traffic

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// EvidenceExchange exposes complete captured bodies only during the callback.
// Readers are closed before releasing the writer lock, so host deletion cannot
// remove a blob between its index lookup and its evidence copy.
type EvidenceExchange struct {
	ID, URL, Method, ContentType string
	TS                           int64
	Status                       int
	ReqHead, RespHead            string
	ReqLen, RespLen              int64
	ReqHash, RespHash            string
	Request, Response            io.Reader
}

// ExchangeExists reports whether id is an exchange in this capture store. A nil
// store or a failed lookup is an error, never a silent false or true.
func (t *Traffic) ExchangeExists(ctx context.Context, id string) (bool, error) {
	if t == nil {
		return false, errors.New("traffic recording store unavailable")
	}
	var one int
	err := t.db.QueryRowContext(ctx, `SELECT 1 FROM exchanges WHERE id=?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (t *Traffic) ReadEvidence(ctx context.Context, ids []string, consume func(EvidenceExchange) error) error {
	if t == nil {
		return errors.New("traffic recording store unavailable")
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := t.readEvidence(id, consume); err != nil {
			return fmt.Errorf("traffic %s: %w", id, err)
		}
	}
	return nil
}

func (t *Traffic) readEvidence(id string, consume func(EvidenceExchange) error) error {
	e := EvidenceExchange{ID: id}
	var legacy string
	if err := t.db.QueryRow(`SELECT ts,url,method,status,content_type,req_len,resp_len,path FROM exchanges WHERE id=?`, id).
		Scan(&e.TS, &e.URL, &e.Method, &e.Status, &e.ContentType, &e.ReqLen, &e.RespLen, &legacy); err != nil {
		return err
	}
	var req, resp []byte
	var reqBlob, respBlob sql.NullString
	err := t.db.QueryRow(`SELECT req_head,req_body,req_blob,resp_head,resp_body,resp_blob FROM exchange_bodies WHERE id=?`, id).
		Scan(&e.ReqHead, &req, &reqBlob, &e.RespHead, &resp, &respBlob)
	if errors.Is(err, sql.ErrNoRows) && legacy != "" {
		if !filepath.IsLocal(legacy) {
			return errors.New("invalid legacy traffic path")
		}
		r, err := os.Open(filepath.Join(t.dir, legacy, "request.http"))
		if err != nil {
			return err
		}
		defer r.Close()
		p, err := os.Open(filepath.Join(t.dir, legacy, "response.http"))
		if err != nil {
			return err
		}
		defer p.Close()
		e.ReqHead, e.Request, err = splitLegacyEvidence(r)
		if err != nil {
			return err
		}
		e.RespHead, e.Response, err = splitLegacyEvidence(p)
		if err != nil {
			return err
		}
		return consume(e)
	}
	if err != nil {
		return err
	}
	e.Request = bytes.NewReader(req)
	e.Response = bytes.NewReader(resp)
	if reqBlob.Valid && reqBlob.String != "" {
		f, _, err := t.Blob(reqBlob.String)
		if err != nil {
			return err
		}
		defer f.Close()
		e.Request = f
		e.ReqHash = reqBlob.String
	}
	if respBlob.Valid && respBlob.String != "" {
		f, _, err := t.Blob(respBlob.String)
		if err != nil {
			return err
		}
		defer f.Close()
		e.Response = f
		e.RespHash = respBlob.String
	}
	return consume(e)
}

func splitLegacyEvidence(r io.Reader) (string, io.Reader, error) {
	b := bufio.NewReader(r)
	var head strings.Builder
	for {
		line, err := b.ReadString('\n')
		if line == "\n" || line == "\r\n" {
			return head.String(), b, nil
		}
		head.WriteString(line)
		if head.Len() > 1<<20 {
			return "", nil, errors.New("legacy traffic header too large")
		}
		if errors.Is(err, io.EOF) {
			return head.String(), b, nil
		}
		if err != nil {
			return "", nil, err
		}
	}
}

// LatestProbeResponse returns the newest captured exchange for (host, path) since
// sinceTS, with status, response length, and SHA-256 hash of the response body.
func (t *Traffic) LatestProbeResponse(host, path string, sinceTS int64) (status int, length int64, bodyHash string, ok bool, err error) {
	if t == nil || t.db == nil {
		return 0, 0, "", false, nil
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	path = strings.TrimSpace(path)
	if host == "" || path == "" {
		return 0, 0, "", false, nil
	}
	rows, err := t.db.Query(`SELECT id, url, status, resp_len FROM exchanges WHERE lower(host)=? AND ts>=? ORDER BY ts DESC LIMIT 25`, host, sinceTS)
	if err != nil {
		return 0, 0, "", false, err
	}
	defer rows.Close()
	var matchID string
	for rows.Next() {
		var id, rawURL string
		var st int
		var ln int64
		if scanErr := rows.Scan(&id, &rawURL, &st, &ln); scanErr != nil {
			continue
		}
		u, perr := url.Parse(rawURL)
		if perr != nil {
			continue
		}
		p := u.Path
		if p == "" {
			p = "/"
		}
		if p == path {
			matchID, status, length = id, st, ln
			break
		}
	}
	if matchID == "" {
		return 0, 0, "", false, nil
	}
	var resp []byte
	var respBlob sql.NullString
	err = t.db.QueryRow(`SELECT resp_body, resp_blob FROM exchange_bodies WHERE id=?`, matchID).Scan(&resp, &respBlob)
	if err != nil {
		return status, length, "", true, nil
	}
	if respBlob.Valid && respBlob.String != "" {
		f, _, berr := t.Blob(respBlob.String)
		if berr == nil {
			defer f.Close()
			h := sha256.New()
			_, _ = io.Copy(h, f)
			bodyHash = hex.EncodeToString(h.Sum(nil))
			return status, length, bodyHash, true, nil
		}
	}
	sum := sha256.Sum256(resp)
	bodyHash = hex.EncodeToString(sum[:])
	return status, length, bodyHash, true, nil
}
