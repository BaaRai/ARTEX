package traffic

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
)

// decodeBody undoes a single Content-Encoding layer (gzip, deflate or br) and
// returns at most limit decoded bytes. The proxy records bodies as they crossed
// the wire, so a compressed body has to be decoded before its text can be
// indexed or matched. ok is false when the body is not encoded, the encoding is
// unsupported or stacked, or the data does not decode. The output is bounded
// because the body comes from the target.
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
	if err != nil {
		return nil, false
	}
	return out, true
}

// indexBody returns the full-text index text of a recorded body: the decoded
// text of a compressed body, otherwise what spill computed.
func indexBody(header http.Header, body []byte, spilled string) string {
	dec, ok := decodeBody(header, body, maxIndexBody)
	if !ok {
		return spilled
	}
	if isBinaryBody(header.Get("Content-Type"), dec) {
		return ""
	}
	return string(dec)
}
