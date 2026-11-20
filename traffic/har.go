package traffic

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
)

const (
	// HARVersion is the HAR specification version produced and accepted.
	HARVersion = "1.2"

	// DefaultHARMaxImportEntries caps how many entries a single HAR import accepts.
	DefaultHARMaxImportEntries = 2000
	// DefaultHARMaxImportBytes caps total decoded request+response body bytes
	// imported in a single HAR document.
	DefaultHARMaxImportBytes int64 = 32 << 20

	// DefaultHARMaxExportEntries caps how many exchanges a single HAR export emits.
	DefaultHARMaxExportEntries = 1000
	// DefaultHARMaxExportBytes caps total raw request+response body bytes emitted
	// in a single HAR export.
	DefaultHARMaxExportBytes int64 = 32 << 20

	maxHARImportErrors = 50
)

// ErrInvalidHAR marks a malformed or unsupported HAR document (mapped to HTTP 400).
var ErrInvalidHAR = errors.New("invalid HAR")

// HAR is a minimal HAR 1.2 envelope. Only fields backed by the traffic store
// are populated on export; timings, serverIPAddress, and synthetic headers are
// never invented.
type HAR struct {
	Log *HARLog `json:"log"`
}

type HARLog struct {
	Version         string     `json:"version"`
	Creator         HARCreator `json:"creator"`
	Entries         []HAREntry `json:"entries"`
	Truncated       bool       `json:"_truncated,omitempty"`
	TruncatedReason string     `json:"_truncatedReason,omitempty"`
	TotalMatched    int        `json:"_totalMatched,omitempty"`
}

type HARCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type HAREntry struct {
	StartedDateTime string      `json:"startedDateTime"`
	Request         HARRequest  `json:"request"`
	Response        HARResponse `json:"response"`
}

type HARRequest struct {
	Method      string       `json:"method"`
	URL         string       `json:"url"`
	HTTPVersion string       `json:"httpVersion,omitempty"`
	Headers     []HARHeader  `json:"headers"`
	PostData    *HARPostData `json:"postData,omitempty"`
	BodySize    int          `json:"bodySize"`
}

type HARPostData struct {
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

type HARResponse struct {
	Status      int         `json:"status"`
	StatusText  string      `json:"statusText,omitempty"`
	HTTPVersion string      `json:"httpVersion,omitempty"`
	Headers     []HARHeader `json:"headers"`
	Content     HARContent  `json:"content"`
	BodySize    int         `json:"bodySize"`
}

type HARContent struct {
	Size     int    `json:"size"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

type HARHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HARImportOptions configures safety caps for ImportHAR. Zero values use the
// package defaults.
type HARImportOptions struct {
	MaxEntries    int
	MaxTotalBytes int64
}

// HARImportError describes one entry that could not be imported.
type HARImportError struct {
	Index  int    `json:"index"`
	URL    string `json:"url,omitempty"`
	Reason string `json:"reason"`
}

// HARImportResult summarizes one ImportHAR run.
type HARImportResult struct {
	Imported  int              `json:"imported"`
	Skipped   int              `json:"skipped"`
	Failed    int              `json:"failed"`
	Truncated bool             `json:"truncated,omitempty"`
	Errors    []HARImportError `json:"errors,omitempty"`
}

// HARExportOptions configures ExportHAR. Zero caps use package defaults.
type HARExportOptions struct {
	CreatorVersion string
	MaxEntries     int
	MaxTotalBytes  int64
}

// HARExportMeta reports export counts and whether a cap stopped the export.
type HARExportMeta struct {
	Exported        int
	TotalMatched    int
	TotalBytes      int64
	Truncated       bool
	TruncatedReason string // "" | "max_entries" | "max_bytes"
}

// ImportHAR parses a HAR 1.2 JSON document and inserts valid entries into the
// traffic store. Entries receive a deterministic ID so re-importing the same
// HAR is idempotent and skips existing rows. Imported traffic is never passed
// through capture rewrite rules and never gains exchange_rewrites rows.
func (t *Traffic) ImportHAR(raw []byte, opts HARImportOptions) (HARImportResult, error) {
	if t == nil {
		return HARImportResult{}, errors.New("traffic store is not available")
	}
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = DefaultHARMaxImportEntries
	}
	if opts.MaxTotalBytes <= 0 {
		opts.MaxTotalBytes = DefaultHARMaxImportBytes
	}

	doc, err := decodeHARDocument(raw)
	if err != nil {
		return HARImportResult{}, err
	}
	if len(doc.Log.Entries) > opts.MaxEntries {
		return HARImportResult{}, fmt.Errorf("%w: HAR contains %d entries, exceeding limit of %d", ErrInvalidHAR, len(doc.Log.Entries), opts.MaxEntries)
	}

	var res HARImportResult
	addErr := func(idx int, rawURL, reason string) {
		if len(res.Errors) < maxHARImportErrors {
			res.Errors = append(res.Errors, HARImportError{Index: idx, URL: rawURL, Reason: reason})
		}
	}

	var totalBytes int64
	for i, entry := range doc.Log.Entries {
		parsed, err := parseHAREntry(entry)
		if err != nil {
			res.Failed++
			addErr(i, entry.Request.URL, err.Error())
			continue
		}
		entryBytes := int64(len(parsed.reqBody) + len(parsed.respBody))
		if totalBytes+entryBytes > opts.MaxTotalBytes {
			remaining := len(doc.Log.Entries) - i
			res.Truncated = true
			res.Skipped += remaining
			addErr(i, entry.Request.URL, fmt.Sprintf("decoded body byte cap (%d bytes) reached; %d remaining entries skipped", opts.MaxTotalBytes, remaining))
			break
		}

		inserted, err := t.insertHARExchange(parsed)
		if err != nil {
			res.Failed++
			addErr(i, entry.Request.URL, err.Error())
			continue
		}
		if !inserted {
			res.Skipped++
			continue
		}
		totalBytes += entryBytes
		res.Imported++
	}
	return res, nil
}

func decodeHARDocument(raw []byte) (HAR, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return HAR{}, fmt.Errorf("%w: empty HAR document", ErrInvalidHAR)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var doc HAR
	if err := dec.Decode(&doc); err != nil {
		return HAR{}, fmt.Errorf("%w: malformed JSON: %v", ErrInvalidHAR, err)
	}
	// Reject trailing non-whitespace tokens after the root object.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return HAR{}, fmt.Errorf("%w: malformed JSON: trailing content after HAR object", ErrInvalidHAR)
	}
	if doc.Log == nil {
		return HAR{}, fmt.Errorf("%w: missing log object", ErrInvalidHAR)
	}
	if strings.TrimSpace(doc.Log.Version) != HARVersion {
		return HAR{}, fmt.Errorf("%w: unsupported HAR version %q (expected %s)", ErrInvalidHAR, doc.Log.Version, HARVersion)
	}
	if doc.Log.Entries == nil {
		return HAR{}, fmt.Errorf("%w: missing log.entries", ErrInvalidHAR)
	}
	return doc, nil
}

type parsedHARExchange struct {
	id          string
	ts          int64
	host        string
	method      string
	urlTemplate string
	rawURL      string
	status      int
	reqCT       string
	respCT      string
	reqHead     string
	reqBody     []byte
	respHead    string
	respBody    []byte
}

func parseHAREntry(entry HAREntry) (parsedHARExchange, error) {
	tsStr := strings.TrimSpace(entry.StartedDateTime)
	if tsStr == "" {
		return parsedHARExchange{}, errors.New("missing startedDateTime")
	}
	parsedTS, err := time.Parse(time.RFC3339Nano, tsStr)
	if err != nil {
		return parsedHARExchange{}, fmt.Errorf("invalid startedDateTime %q", entry.StartedDateTime)
	}
	ts := parsedTS.UTC().Unix()

	rawURL := strings.TrimSpace(entry.Request.URL)
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return parsedHARExchange{}, fmt.Errorf("invalid request url %q", entry.Request.URL)
	}
	method := strings.ToUpper(strings.TrimSpace(entry.Request.Method))
	if method == "" || !validHeaderName(method) {
		return parsedHARExchange{}, fmt.Errorf("invalid request method %q", entry.Request.Method)
	}
	if entry.Response.Status < 0 || entry.Response.Status > 999 {
		return parsedHARExchange{}, fmt.Errorf("invalid response status %d", entry.Response.Status)
	}

	reqHeaderText, reqCT, err := formatHARHeaders(entry.Request.Headers)
	if err != nil {
		return parsedHARExchange{}, fmt.Errorf("invalid request headers: %w", err)
	}
	respHeaderText, respHeaderCT, err := formatHARHeaders(entry.Response.Headers)
	if err != nil {
		return parsedHARExchange{}, fmt.Errorf("invalid response headers: %w", err)
	}

	var reqBody []byte
	if entry.Request.PostData != nil {
		reqBody, err = decodeHARBody(entry.Request.PostData.Text, entry.Request.PostData.Encoding)
		if err != nil {
			return parsedHARExchange{}, fmt.Errorf("invalid request postData: %w", err)
		}
		if reqCT == "" {
			reqCT = strings.TrimSpace(entry.Request.PostData.MimeType)
		}
	}
	respBody, err := decodeHARBody(entry.Response.Content.Text, entry.Response.Content.Encoding)
	if err != nil {
		return parsedHARExchange{}, fmt.Errorf("invalid response content: %w", err)
	}

	respCT := strings.TrimSpace(entry.Response.Content.MimeType)
	if respCT == "" {
		respCT = respHeaderCT
	}

	reqProto := sanitizeHARProto(entry.Request.HTTPVersion)
	respProto := sanitizeHARProto(entry.Response.HTTPVersion)
	reqHead := fmt.Sprintf("%s %s %s\n%s", method, u.RequestURI(), reqProto, reqHeaderText)

	statusText := strings.TrimSpace(entry.Response.StatusText)
	if strings.ContainsAny(statusText, "\r\n\x00") {
		return parsedHARExchange{}, errors.New("response statusText contains control characters")
	}
	respHead := fmt.Sprintf("%s %d\n%s", respProto, entry.Response.Status, respHeaderText)
	if statusText != "" {
		respHead = fmt.Sprintf("%s %d %s\n%s", respProto, entry.Response.Status, statusText, respHeaderText)
	}

	canonicalURL := u.String()
	id := harExchangeID(ts, method, canonicalURL, entry.Response.Status, reqBody, respBody)
	return parsedHARExchange{
		id:          id,
		ts:          ts,
		host:        u.Hostname(),
		method:      method,
		urlTemplate: db.TemplatePath(u.EscapedPath()),
		rawURL:      canonicalURL,
		status:      entry.Response.Status,
		reqCT:       reqCT,
		respCT:      respCT,
		reqHead:     reqHead,
		reqBody:     reqBody,
		respHead:    respHead,
		respBody:    respBody,
	}, nil
}

// harExchangeID derives a deterministic ID from the exchange's timestamp (Unix
// seconds), method, canonical URL, status code, and SHA-256 digests of both
// bodies. Using Unix seconds matches exchanges.ts so export -> re-import
// produces the exact same ID.
func harExchangeID(ts int64, method, rawURL string, status int, reqBody, respBody []byte) string {
	reqSum := sha256.Sum256(reqBody)
	respSum := sha256.Sum256(respBody)
	h := sha256.New()
	fmt.Fprintf(h, "%d\x00%s\x00%s\x00%d\x00%x\x00%x", ts, method, rawURL, status, reqSum, respSum)
	return "har-" + hex.EncodeToString(h.Sum(nil))
}

func formatHARHeaders(headers []HARHeader) (formatted string, contentType string, err error) {
	var b strings.Builder
	for _, h := range headers {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			return "", "", errors.New("empty header name")
		}
		// Skip HTTP/2 pseudo-headers (:method, :authority, :scheme, :path, :status)
		// emitted by browser DevTools HAR exports; their semantics live in the
		// request/status line.
		if strings.HasPrefix(name, ":") {
			continue
		}
		if !validHeaderName(name) {
			return "", "", fmt.Errorf("invalid header name %q", h.Name)
		}
		if strings.ContainsAny(h.Value, "\r\n\x00") {
			return "", "", fmt.Errorf("header %s contains a line break or NUL byte", name)
		}
		if contentType == "" && strings.EqualFold(name, "Content-Type") {
			contentType = strings.TrimSpace(h.Value)
		}
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(h.Value)
		b.WriteByte('\n')
	}
	return b.String(), contentType, nil
}

func decodeHARBody(text, encoding string) ([]byte, error) {
	enc := strings.ToLower(strings.TrimSpace(encoding))
	switch enc {
	case "":
		if text == "" {
			return nil, nil
		}
		return []byte(text), nil
	case "base64":
		if text == "" {
			return nil, nil
		}
		decoded, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 body: %w", err)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported body encoding %q", encoding)
	}
}

func sanitizeHARProto(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.ContainsAny(v, " \t\r\n\x00") || !strings.HasPrefix(strings.ToUpper(v), "HTTP/") {
		return "HTTP/1.1"
	}
	return v
}

// insertHARExchange inserts one HAR exchange idempotently (INSERT OR IGNORE)
// through the same spill/blob_refs/FTS path as the recorder and ImportArchive.
// It never writes to exchange_rewrites.
func (t *Traffic) insertHARExchange(ex parsedHARExchange) (bool, error) {
	t.wmu.Lock()
	defer t.wmu.Unlock()

	var exists int
	if err := t.db.QueryRow(`SELECT 1 FROM exchanges WHERE id=?`, ex.id).Scan(&exists); err == nil {
		return false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	reqB := t.spill(ex.reqBody, ex.reqCT)
	respB := t.spill(ex.respBody, ex.respCT)

	tx, err := t.db.Begin()
	if err != nil {
		return false, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.Exec(`INSERT OR IGNORE INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,'')`,
		ex.id, ex.ts, ex.host, ex.method, ex.urlTemplate, ex.rawURL, ex.status, ex.respCT, len(ex.reqBody), len(ex.respBody))
	if err != nil {
		return false, fmt.Errorf("writing index: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	var rowid int64
	if err := tx.QueryRow(`SELECT rowid FROM exchanges WHERE id=?`, ex.id).Scan(&rowid); err != nil {
		return false, fmt.Errorf("fetching rowid: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO exchange_bodies(id,req_head,req_body,req_blob,resp_head,resp_body,resp_blob)
VALUES(?,?,?,?,?,?,?)`,
		ex.id, ex.reqHead, reqB.inline, nullIfEmpty(reqB.hash), ex.respHead, respB.inline, nullIfEmpty(respB.hash)); err != nil {
		return false, fmt.Errorf("writing body: %w", err)
	}
	for _, h := range []string{reqB.hash, respB.hash} {
		if h == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO blob_refs(hash,exchange_id) VALUES(?,?)`, h, ex.id); err != nil {
			return false, fmt.Errorf("registering blob reference: %w", err)
		}
	}
	if t.fts {
		idx := strings.Join([]string{ex.rawURL, ex.reqHead, reqB.index, ex.respHead, respB.index}, "\n")
		if _, err := tx.Exec(`INSERT INTO ex_fts(rowid,content) VALUES(?,?)`, rowid, idx); err != nil {
			return false, fmt.Errorf("writing full-text index: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing: %w", err)
	}
	return true, nil
}

// ExportHAR exports exchanges matching f as a HAR 1.2 document in stable
// chronological order (ts ASC, id ASC), capped by opts.MaxEntries and
// opts.MaxTotalBytes. Truncation is reported in both HARExportMeta and the
// HAR 1.2 custom fields _truncated / _truncatedReason / _totalMatched.
func (t *Traffic) ExportHAR(f PageQuery, opts HARExportOptions) (HAR, HARExportMeta, error) {
	if t == nil {
		return HAR{}, HARExportMeta{}, errors.New("traffic store is not available")
	}
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = DefaultHARMaxExportEntries
	}
	if opts.MaxTotalBytes <= 0 {
		opts.MaxTotalBytes = DefaultHARMaxExportBytes
	}
	creatorVer := strings.TrimSpace(opts.CreatorVersion)
	if creatorVer == "" {
		creatorVer = "dev"
	}

	where, args := t.pageWhere(f)
	var totalMatched int
	if err := t.db.QueryRow(`SELECT COUNT(*) FROM exchanges`+where, args...).Scan(&totalMatched); err != nil {
		return HAR{}, HARExportMeta{}, err
	}

	type exRow struct {
		id          string
		ts          int64
		host        string
		method      string
		rawURL      string
		status      int
		contentType string
		legacyPath  string
	}

	// Fetch at most MaxEntries+1 rows so we know whether the entry cap fired.
	sel := `SELECT id,ts,host,method,url,status,content_type,path FROM exchanges` +
		where + ` ORDER BY ts ASC, id ASC LIMIT ?`
	qargs := append(append([]any{}, args...), opts.MaxEntries+1)
	rows, err := t.db.Query(sel, qargs...)
	if err != nil {
		return HAR{}, HARExportMeta{}, err
	}
	var matched []exRow
	for rows.Next() {
		var r exRow
		if err := rows.Scan(&r.id, &r.ts, &r.host, &r.method, &r.rawURL, &r.status, &r.contentType, &r.legacyPath); err != nil {
			rows.Close()
			return HAR{}, HARExportMeta{}, err
		}
		matched = append(matched, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return HAR{}, HARExportMeta{}, err
	}
	rows.Close()

	meta := HARExportMeta{TotalMatched: totalMatched}
	hitEntryCap := len(matched) > opts.MaxEntries
	if hitEntryCap {
		matched = matched[:opts.MaxEntries]
	}

	entries := make([]HAREntry, 0, len(matched))
	for _, r := range matched {
		reqHead, reqBytes, respHead, respBytes, err := t.loadFullExchange(r.id, r.legacyPath)
		if err != nil {
			return HAR{}, HARExportMeta{}, err
		}
		entryBytes := int64(len(reqBytes) + len(respBytes))
		if meta.TotalBytes+entryBytes > opts.MaxTotalBytes {
			meta.Truncated = true
			meta.TruncatedReason = "max_bytes"
			break
		}
		meta.TotalBytes += entryBytes

		reqProto, reqHeaders := parseStoredRequestHead(reqHead)
		respProto, respStatusText, respHeaders := parseStoredResponseHead(respHead)

		harReq := HARRequest{
			Method:      r.method,
			URL:         r.rawURL,
			HTTPVersion: reqProto,
			Headers:     reqHeaders,
			BodySize:    len(reqBytes),
		}
		if len(reqBytes) > 0 {
			text, enc := encodeHARBody(reqBytes)
			harReq.PostData = &HARPostData{
				MimeType: findHeaderValue(reqHeaders, "Content-Type"),
				Text:     text,
				Encoding: enc,
			}
		}

		harContent := HARContent{
			Size:     len(respBytes),
			MimeType: r.contentType,
		}
		if len(respBytes) > 0 {
			harContent.Text, harContent.Encoding = encodeHARBody(respBytes)
		}

		harResp := HARResponse{
			Status:      r.status,
			StatusText:  respStatusText,
			HTTPVersion: respProto,
			Headers:     respHeaders,
			Content:     harContent,
			BodySize:    len(respBytes),
		}

		entries = append(entries, HAREntry{
			StartedDateTime: time.Unix(r.ts, 0).UTC().Format(time.RFC3339),
			Request:         harReq,
			Response:        harResp,
		})
	}

	if !meta.Truncated && hitEntryCap {
		meta.Truncated = true
		meta.TruncatedReason = "max_entries"
	}
	meta.Exported = len(entries)

	doc := HAR{
		Log: &HARLog{
			Version:         HARVersion,
			Creator:         HARCreator{Name: "ARTEX", Version: creatorVer},
			Entries:         entries,
			Truncated:       meta.Truncated,
			TruncatedReason: meta.TruncatedReason,
		},
	}
	if meta.Truncated {
		doc.Log.TotalMatched = meta.TotalMatched
	}
	return doc, meta, nil
}

func (t *Traffic) loadFullExchange(id, legacyPath string) (reqHead string, reqBody []byte, respHead string, respBody []byte, err error) {
	var reqBlob, respBlob sql.NullString
	err = t.db.QueryRow(`SELECT req_head,req_body,req_blob,resp_head,resp_body,resp_blob
FROM exchange_bodies WHERE id=?`, id).Scan(&reqHead, &reqBody, &reqBlob, &respHead, &respBody, &respBlob)
	if errors.Is(err, sql.ErrNoRows) && strings.TrimSpace(legacyPath) != "" {
		rb, readErr := os.ReadFile(filepath.Join(t.dir, legacyPath, "request.http"))
		if readErr != nil && !os.IsNotExist(readErr) {
			return "", nil, "", nil, readErr
		}
		pb, readErr := os.ReadFile(filepath.Join(t.dir, legacyPath, "response.http"))
		if readErr != nil && !os.IsNotExist(readErr) {
			return "", nil, "", nil, readErr
		}
		reqHead, reqBody = splitLegacyHTTP(rb)
		respHead, respBody = splitLegacyHTTP(pb)
		return reqHead, reqBody, respHead, respBody, nil
	}
	if err != nil {
		return "", nil, "", nil, err
	}
	if reqBlob.Valid && reqBlob.String != "" {
		p, err := t.blobPath(reqBlob.String)
		if err != nil {
			return "", nil, "", nil, err
		}
		reqBody, err = os.ReadFile(p)
		if err != nil {
			return "", nil, "", nil, err
		}
	}
	if respBlob.Valid && respBlob.String != "" {
		p, err := t.blobPath(respBlob.String)
		if err != nil {
			return "", nil, "", nil, err
		}
		respBody, err = os.ReadFile(p)
		if err != nil {
			return "", nil, "", nil, err
		}
	}
	return reqHead, reqBody, respHead, respBody, nil
}

func splitLegacyHTTP(raw []byte) (string, []byte) {
	if idx := bytes.Index(raw, []byte("\r\n\r\n")); idx >= 0 {
		return string(raw[:idx]), raw[idx+4:]
	}
	if idx := bytes.Index(raw, []byte("\n\n")); idx >= 0 {
		return string(raw[:idx]), raw[idx+2:]
	}
	return string(raw), nil
}

// encodeHARBody returns plain text for valid NUL-free UTF-8 bodies, and base64
// otherwise so binary payloads survive export and re-import bit-for-bit.
func encodeHARBody(body []byte) (text, encoding string) {
	if utf8.Valid(body) && bytes.IndexByte(body, 0) < 0 {
		return string(body), ""
	}
	return base64.StdEncoding.EncodeToString(body), "base64"
}

func parseStoredRequestHead(raw string) (httpVersion string, headers []HARHeader) {
	headers = []HARHeader{}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return "", headers
	}
	first := strings.TrimSpace(lines[0])
	if parts := strings.SplitN(first, " ", 3); len(parts) == 3 && strings.HasPrefix(strings.ToUpper(parts[2]), "HTTP/") {
		httpVersion = strings.TrimSpace(parts[2])
	}
	headers = appendParsedHeaders(headers, lines[1:])
	return httpVersion, headers
}

func parseStoredResponseHead(raw string) (httpVersion, statusText string, headers []HARHeader) {
	headers = []HARHeader{}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return "", "", headers
	}
	first := strings.TrimSpace(lines[0])
	if parts := strings.SplitN(first, " ", 3); len(parts) >= 2 && strings.HasPrefix(strings.ToUpper(parts[0]), "HTTP") {
		if strings.HasPrefix(strings.ToUpper(parts[0]), "HTTP/") {
			httpVersion = strings.TrimSpace(parts[0])
		}
		if len(parts) == 3 {
			statusText = strings.TrimSpace(parts[2])
		}
	}
	headers = appendParsedHeaders(headers, lines[1:])
	return httpVersion, statusText, headers
}

func appendParsedHeaders(dst []HARHeader, lines []string) []HARHeader {
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		if name == "" || !validHeaderName(name) {
			continue
		}
		val := strings.TrimSpace(line[colon+1:])
		dst = append(dst, HARHeader{Name: name, Value: val})
	}
	return dst
}

func findHeaderValue(headers []HARHeader, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}
