package traffic

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
)

// decodeBody undoes a single Content-Encoding layer (gzip, deflate or br) and
// returns at most limit decoded bytes. The proxy records bodies as they crossed
// the wire, so a compressed body has to be decoded before its text can be
// indexed or matched. ok is false when the body is not encoded, the encoding is
// unsupported or stacked, or the data does not decode. A truncated stream
// yields what decoded before the cut. The output is bounded because the body
// comes from the target.
func decodeBody(header http.Header, body []byte, limit int) (out []byte, ok bool) {
	enc := strings.ToLower(strings.TrimSpace(header.Get("Content-Encoding")))
	if enc == "" || enc == "identity" || len(body) == 0 || strings.Contains(enc, ",") {
		return nil, false
	}
	var zr io.Reader
	switch enc {
	case "gzip", "x-gzip":
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, false
		}
		zr = gz
	case "deflate":
		// HTTP deflate is zlib (RFC 9110); some servers send a raw deflate stream.
		if z, err := zlib.NewReader(bytes.NewReader(body)); err == nil {
			zr = z
		} else {
			zr = flate.NewReader(bytes.NewReader(body))
		}
	case "br":
		zr = brotli.NewReader(bytes.NewReader(body))
	default:
		return nil, false
	}
	out, err := io.ReadAll(io.LimitReader(zr, int64(limit)))
	if err != nil && (!errors.Is(err, io.ErrUnexpectedEOF) || len(out) == 0) {
		return nil, false
	}
	return out, true
}

// indexBody returns the full-text index text of a compressed body. ok is false
// when the body is not one decodeBody handles, and the index text spill
// computes stands. It does no locking, so callers run it before taking wmu.
func indexBody(header http.Header, body []byte) (text string, ok bool) {
	ct := header.Get("Content-Type")
	if isBinaryBody(ct, nil) {
		// A binary content type is never indexed, encoded or not.
		return "", false
	}
	dec, ok := decodeBody(header, body, maxIndexBody)
	if !ok {
		return "", false
	}
	if isBinaryBody(ct, dec) {
		return "", true
	}
	return string(dec), true
}
