package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/evidence"
)

// S1 receipt check. A finding is verified only when a proof/verification
// traffic body is bound, the agent declared POE L3 or higher, and every quote
// appears verbatim (after whitespace normalization) in one of those response
// bodies. Anything else is unverified, blocked or not_assessed, with a note.

const (
	verificationMinPOE       = 3
	verificationReadCap      = 4 << 20 // bytes read per bound body; a quote beyond this cannot be confirmed
	verificationNoteQuoteLen = 60
)

// boundBody is one bound response body, read through the evidence store and
// normalized for the substring check.
type boundBody struct {
	text      string // whitespace-normalized text (only when !nonText)
	truncated bool   // body was longer than verificationReadCap
	nonText   bool   // binary or non-UTF-8 content: never claimed as a match
}

// normalizeEvidenceText trims and collapses every run of whitespace to one space.
// Case is kept.
func normalizeEvidenceText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// readBoundResponse reads the response side of a bound snapshot through the
// evidence store (same bytes the UI shows), capped at verificationReadCap.
func readBoundResponse(store *evidence.Store, snap db.TrafficEvidenceSnapshot) (boundBody, error) {
	f, _, err := store.OpenBody(snap, "response")
	if err != nil {
		return boundBody{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, verificationReadCap+1))
	if err != nil {
		return boundBody{}, err
	}
	var bb boundBody
	if len(raw) > verificationReadCap {
		bb.truncated = true
		raw = raw[:verificationReadCap]
		// The cap may cut a multi-byte rune: drop at most UTFMax-1 trailing bytes.
		for i := 0; i < utf8.UTFMax-1 && !utf8.Valid(raw) && len(raw) > 0; i++ {
			raw = raw[:len(raw)-1]
		}
	}
	if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
		bb.nonText = true
		return bb, nil
	}
	bb.text = normalizeEvidenceText(string(raw))
	return bb, nil
}

// assessFindingVerification computes the verification state of a finding from
// its bound traffic and the quotes the agent supplied. poe is the declared POE
// level (0 when absent). It never returns "verified" unless every rule passes.
func (s *Server) assessFindingVerification(ctx context.Context, findingID int64, poe int, quotes []string) (status, note string) {
	tr, err := s.m.pg.GetFindingTraffic(ctx, findingID)
	if err != nil {
		return db.VerificationNotAssessed, "binding read error: " + err.Error()
	}
	var proofs []db.FindingTrafficBinding
	for _, b := range tr.Bindings {
		if b.Role == "proof" || b.Role == "verification" {
			proofs = append(proofs, b)
		}
	}
	if len(proofs) == 0 {
		return db.VerificationUnverified, "no proof traffic bound"
	}
	if poe < verificationMinPOE {
		return db.VerificationUnverified, fmt.Sprintf("POE L%d below L3", poe)
	}
	if len(quotes) == 0 {
		return db.VerificationUnverified, "no evidence quotes provided"
	}
	store := s.evidenceStore()
	bodies := make([]boundBody, 0, len(proofs))
	for _, b := range proofs {
		bb, err := readBoundResponse(store, b.Snapshot)
		if err != nil {
			// Fail closed: an unreadable body is never an "unverified" verdict.
			return db.VerificationNotAssessed, "evidence body unreadable: " + err.Error()
		}
		bodies = append(bodies, bb)
	}
	for _, q := range quotes {
		nq := normalizeEvidenceText(q)
		if nq != "" && quoteInBodies(nq, bodies) {
			continue
		}
		for _, bb := range bodies {
			if bb.nonText {
				return db.VerificationNotAssessed, "non-text evidence"
			}
		}
		for _, bb := range bodies {
			if bb.truncated {
				return db.VerificationNotAssessed, "quote not found within read cap of bound evidence"
			}
		}
		return db.VerificationUnverified, "quote not found in bound evidence: " + shortQuote(q)
	}
	return db.VerificationVerified, ""
}

func quoteInBodies(nq string, bodies []boundBody) bool {
	for _, bb := range bodies {
		if !bb.nonText && strings.Contains(bb.text, nq) {
			return true
		}
	}
	return false
}

// shortQuote returns the first verificationNoteQuoteLen runes of the trimmed
// quote, for use in a note.
func shortQuote(q string) string {
	r := []rune(strings.TrimSpace(q))
	if len(r) > verificationNoteQuoteLen {
		r = r[:verificationNoteQuoteLen]
	}
	return string(r)
}

// seedFindingVerificationSchema adds the S1 optional properties to the
// update_finding_report schema of existing databases. Idempotent: a property is
// added only when the stored schema lacks it, and user edits are preserved.
func (s *Server) seedFindingVerificationSchema() {
	raw, err := json.Marshal(s.toolUpdateFindingReport().InputSchema())
	if err != nil {
		log.Printf("[verification] tool schema: %v", err)
		return
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(obj["properties"], &properties); err != nil {
		return
	}
	for _, name := range []string{"poe_level", "evidence_quotes"} {
		if len(properties[name]) == 0 {
			continue
		}
		_, err := s.m.pg.Exec(`UPDATE tools SET schema=jsonb_set(schema,ARRAY['properties',$2::text],$3::jsonb,true),updated_at=now()
WHERE key=$1 AND system AND NOT(COALESCE(schema->'properties','{}'::jsonb) ? $2)`, "update_finding_report", name, string(properties[name]))
		if err != nil {
			log.Printf("[verification] upgrade update_finding_report.%s: %v", name, err)
		}
	}
}
