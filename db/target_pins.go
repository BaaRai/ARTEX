package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// TargetScopeRef names what a target pin or a target approval request is bound
// to: one task or one conversation, never both.
type TargetScopeRef struct {
	TaskID         int64
	ConversationID int64
}

func (s TargetScopeRef) check() error {
	if (s.TaskID > 0) == (s.ConversationID > 0) {
		return fmt.Errorf("target scope must name one task or one conversation, got task %d and conversation %d", s.TaskID, s.ConversationID)
	}
	return nil
}

// pendingScope is the scope as intercept_pending stores it: task_id is TEXT
// there, and the column of the other kind is NULL.
func (s TargetScopeRef) pendingScope() (taskID *string, convID *int64) {
	if s.TaskID > 0 {
		v := strconv.FormatInt(s.TaskID, 10)
		return &v, nil
	}
	return nil, &s.ConversationID
}

// TargetKey is the key of an asset target inside its scope: host and port. The
// port is always the part after the last colon, so the key stays unambiguous
// for IPv6 literals and CIDRs.
func TargetKey(host string, port int) string {
	return host + ":" + strconv.Itoa(port)
}

// Target pin states and revocation reasons. The columns carry no CHECK; these
// are the only values written.
const (
	TargetPinActive     = "active"
	TargetPinSuperseded = "superseded"
	TargetPinRevoked    = "revoked"

	TargetPinRevokedByOperator         = "operator"
	TargetPinRevokedTaskDeleted        = "task_deleted"
	TargetPinRevokedConversationDelete = "conversation_deleted"
)

// TargetPin is one row of target_pins: a host and port a human approved for one
// scope, with the addresses that were shown at approval time. Port 0 means the
// port was not declared and covers every port of the host.
type TargetPin struct {
	ID             int64     `json:"id"`
	TaskID         *int64    `json:"task_id"`
	ConversationID *int64    `json:"conversation_id"`
	Host           string    `json:"host"`
	Port           int       `json:"port"`
	Version        int       `json:"version"`
	PinnedIPs      []string  `json:"pinned_ips"`
	Resolver       string    `json:"resolver"`
	ResolvedAt     time.Time `json:"resolved_at"`
	Status         string    `json:"status"`
	InterceptID    *int64    `json:"intercept_id"`
	DecidedBy      string    `json:"decided_by"`
	DecidedFrom    string    `json:"decided_from"`
	// NameOnlyEffective: approved while the upstream egress proxy was set, so the
	// pin covers the name and its addresses are not compared. It stays set when
	// the proxy is removed later.
	NameOnlyEffective bool       `json:"name_only_effective"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
	RevokedReason     string     `json:"revoked_reason,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

const targetPinCols = `id, task_id, conversation_id, host, port, version, array_to_json(pinned_ips)::text, resolver, resolved_at, status, intercept_id, decided_by, decided_from, name_only_effective, revoked_at, revoked_reason, created_at`

// targetPinScope matches the expressions of uq_target_pins_active.
const targetPinScope = `COALESCE(task_id,0)=$1 AND COALESCE(conversation_id,0)=$2`

func scanTargetPin(row interface{ Scan(...any) error }) (*TargetPin, error) {
	var p TargetPin
	var ips string
	if err := row.Scan(&p.ID, &p.TaskID, &p.ConversationID, &p.Host, &p.Port, &p.Version, &ips, &p.Resolver, &p.ResolvedAt,
		&p.Status, &p.InterceptID, &p.DecidedBy, &p.DecidedFrom, &p.NameOnlyEffective, &p.RevokedAt, &p.RevokedReason, &p.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(ips), &p.PinnedIPs); err != nil {
		return nil, err
	}
	return &p, nil
}

func queryTargetPins(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]*TargetPin, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TargetPin{}
	for rows.Next() {
		p, err := scanTargetPin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ActiveTargetPins returns the active pins that may cover the target
// (host, port) in scope, the pin on the exact port first. A target on a declared
// port has two candidates: the pin on that port and the pin on port 0. A target
// whose port is not declared (0) has one: a pin on a specific port never covers
// it. The address sets of two pins are never merged by the caller.
func (d *DB) ActiveTargetPins(scope TargetScopeRef, host string, port int) ([]*TargetPin, error) {
	if err := scope.check(); err != nil {
		return nil, err
	}
	return queryTargetPins(d, `SELECT `+targetPinCols+` FROM target_pins
WHERE `+targetPinScope+` AND host=$3 AND port IN ($4, 0) AND status='active'
ORDER BY port DESC`, scope.TaskID, scope.ConversationID, host, port)
}

// ListTargetPins returns every pin of scope in any state, newest first.
func (d *DB) ListTargetPins(scope TargetScopeRef) ([]*TargetPin, error) {
	if err := scope.check(); err != nil {
		return nil, err
	}
	return queryTargetPins(d, `SELECT `+targetPinCols+` FROM target_pins
WHERE `+targetPinScope+` ORDER BY id DESC`, scope.TaskID, scope.ConversationID)
}

// RevokeTargetPin revokes one active pin of scope on behalf of the operator and
// returns the pin as it is afterwards (nil when scope has no such pin). Revoking
// a pin that is no longer active changes nothing.
func (d *DB) RevokeTargetPin(scope TargetScopeRef, pinID int64) (*TargetPin, error) {
	if err := scope.check(); err != nil {
		return nil, err
	}
	if _, err := d.Exec(`UPDATE target_pins SET status='revoked', revoked_at=now(), revoked_reason=$4
WHERE `+targetPinScope+` AND id=$3 AND status='active'`, scope.TaskID, scope.ConversationID, pinID, TargetPinRevokedByOperator); err != nil {
		return nil, err
	}
	p, err := scanTargetPin(d.QueryRow(`SELECT `+targetPinCols+` FROM target_pins
WHERE `+targetPinScope+` AND id=$3`, scope.TaskID, scope.ConversationID, pinID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// revokeTaskPins marks the active pins of a task that is being deleted as
// revoked. They stay as history; task ids are never reused, so a revoked pin
// can never match again.
func revokeTaskPins(tx *sql.Tx, taskID int64) error {
	_, err := tx.Exec(`UPDATE target_pins SET status='revoked', revoked_at=now(), revoked_reason=$2
WHERE task_id=$1 AND status='active'`, taskID, TargetPinRevokedTaskDeleted)
	return err
}

// revokeConversationPins is revokeTaskPins for deleted conversations.
func revokeConversationPins(tx *sql.Tx, convIDs []int64) error {
	_, err := tx.Exec(`UPDATE target_pins SET status='revoked', revoked_at=now(), revoked_reason=$2
WHERE conversation_id=ANY($1::bigint[]) AND status='active'`, convIDs, TargetPinRevokedConversationDelete)
	return err
}

// settleTaskTargetRequests closes the open target requests of a task that is
// being deleted: denied by the system, nothing pinned. Only target rows are
// touched. (A conversation needs no counterpart: its rows cascade.)
func settleTaskTargetRequests(tx *sql.Tx, taskID int64) error {
	_, err := tx.Exec(`UPDATE intercept_pending SET status='denied', decided_at=NOW(), decided_by='system',
	audit=CASE WHEN audit IS NULL THEN NULL ELSE audit ||
	'{"effective_action":"deny","decision_reason":"task deleted","execution_status":"not_executed"}'::jsonb END
WHERE COALESCE(task_id,'')=$1 AND status='pending' AND target_key IS NOT NULL`, strconv.FormatInt(taskID, 10))
	return err
}

// TargetRequest is a new target approval request: the intercept_pending row a
// gated call asks for when one of its targets has no pin that covers it.
type TargetRequest struct {
	Scope     TargetScopeRef
	Host      string
	Port      int
	RuleID    int64 // owning rule of a merged ask; 0 otherwise
	AgentName string
	ToolName  string
	ToolInput []byte
	Reason    string
	Audit     *InterceptAudit
}

// FindOrCreateTargetRequest returns the open request for (scope, host, port),
// creating it when there is none. At most one such row is open at a time
// (uq_intercept_pending_target), across goroutines and across instances that
// share the database, so concurrent calls to one host share one request.
// created reports whether this call wrote the row. id == 0 means the request
// that was open got decided in between: the caller goes back to the pin lookup.
func (d *DB) FindOrCreateTargetRequest(r TargetRequest) (id int64, created bool, err error) {
	if err := r.Scope.check(); err != nil {
		return 0, false, err
	}
	raw := json.RawMessage(r.ToolInput)
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var ruleID *int64
	if r.RuleID != 0 {
		ruleID = &r.RuleID
	}
	taskID, convID := r.Scope.pendingScope()
	key := TargetKey(r.Host, r.Port)
	err = d.QueryRow(`
INSERT INTO intercept_pending(rule_id, conversation_id, task_id, agent_name, tool_name, tool_input, reason, decision_source, audit, target_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'target', $8, $9)
ON CONFLICT (COALESCE(task_id,''), COALESCE(conversation_id,0), target_key)
    WHERE status = 'pending' AND target_key IS NOT NULL
DO NOTHING RETURNING id`,
		ruleID, convID, taskID, r.AgentName, r.ToolName, raw, r.Reason, firstAudit([]*InterceptAudit{r.Audit}), key).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	taskKey := ""
	if taskID != nil {
		taskKey = *taskID
	}
	err = d.QueryRow(`SELECT id FROM intercept_pending
WHERE COALESCE(task_id,'')=$1 AND COALESCE(conversation_id,0)=$2 AND target_key=$3 AND status='pending'`,
		taskKey, r.Scope.ConversationID, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, false, err
}

// TargetRequestKey returns the target key of an approval row: "" for a classic
// ask and for an id that does not exist.
func (d *DB) TargetRequestKey(id int64) (string, error) {
	var key sql.NullString
	err := d.QueryRow(`SELECT target_key FROM intercept_pending WHERE id=$1`, id).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return key.String, err
}

// ResolveTargetRequest settles an open target request with a human decision, in
// one transaction. Allowed pins the addresses the request displayed: the active
// pin of the same key, if any, becomes superseded and a new version holds its
// addresses plus the displayed ones (addresses leave only by revocation).
// Denied pins nothing. approver and remoteAddr go to the pin and to the row's
// audit; decided_by on the row stays the actor class, human. ok is false when
// the row is not an open target request. If the pin cannot be written the row
// stays pending.
func (d *DB) ResolveTargetRequest(id int64, allowed bool, approver, remoteAddr string) (pin *TargetPin, ok bool, err error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var taskID sql.NullString
	var convID sql.NullInt64
	var raw []byte
	err = tx.QueryRow(`SELECT task_id, conversation_id, audit FROM intercept_pending
WHERE id=$1 AND status='pending' AND target_key IS NOT NULL FOR UPDATE`, id).Scan(&taskID, &convID, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var audit InterceptAudit
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &audit); err != nil {
			return nil, false, err
		}
	}
	status, action, reason, execution := "denied", "deny", "user rejected the target; nothing was pinned", "not_executed"
	if allowed {
		if len(audit.Targets) != 1 {
			return nil, false, fmt.Errorf("target request %d records %d targets, want 1", id, len(audit.Targets))
		}
		scope := TargetScopeRef{ConversationID: convID.Int64}
		if taskID.Valid {
			if scope.TaskID, err = strconv.ParseInt(taskID.String, 10, 64); err != nil {
				return nil, false, fmt.Errorf("target request %d: task id %q: %w", id, taskID.String, err)
			}
		}
		if pin, err = writeTargetPin(tx, scope, id, &audit.Targets[0], approver, remoteAddr); err != nil {
			return nil, false, err
		}
		status, action, reason = "allowed", "allow", "user approved the target; the displayed addresses were pinned"
		// A plain target request is host-level: no single call owns its result.
		execution = "unknown"
		if audit.MergedAsk && audit.Correlation == "exact" {
			execution = "awaiting_result"
		}
	} else if len(audit.Targets) == 1 {
		audit.Targets[0].Outcome = "denied"
	}
	fields := map[string]any{
		"effective_action": action, "decision_reason": reason, "execution_status": execution,
		"targets": audit.Targets,
	}
	if approver != "" {
		fields["approver"] = approver
	}
	if remoteAddr != "" {
		fields["approver_addr"] = remoteAddr
	}
	patch, err := json.Marshal(fields)
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(`UPDATE intercept_pending SET status=$2, decided_at=NOW(), decided_by='human',
	audit=COALESCE(audit,'{}'::jsonb) || $3::jsonb WHERE id=$1`, id, status, patch); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return pin, true, nil
}

// writeTargetPin inserts the pin an approved request creates and fills the
// request's target with what was pinned. The only writer of target_pins rows.
func writeTargetPin(tx *sql.Tx, scope TargetScopeRef, interceptID int64, t *InterceptTarget, approver, remoteAddr string) (*TargetPin, error) {
	if err := scope.check(); err != nil {
		return nil, err
	}
	// Versions count every pin the key ever had, revoked ones included, so a
	// version number names one pin of the key for good.
	var version int
	if err := tx.QueryRow(`SELECT COALESCE(max(version),0)+1 FROM target_pins
WHERE `+targetPinScope+` AND host=$3 AND port=$4`, scope.TaskID, scope.ConversationID, t.Host, t.Port).Scan(&version); err != nil {
		return nil, err
	}
	ips := map[string]bool{}
	for _, ip := range t.ResolvedIPs {
		ips[ip] = true
	}
	prev, err := scanTargetPin(tx.QueryRow(`SELECT `+targetPinCols+` FROM target_pins
WHERE `+targetPinScope+` AND host=$3 AND port=$4 AND status='active' FOR UPDATE`, scope.TaskID, scope.ConversationID, t.Host, t.Port))
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		for _, ip := range prev.PinnedIPs {
			ips[ip] = true
		}
		if _, err := tx.Exec(`UPDATE target_pins SET status='superseded' WHERE id=$1`, prev.ID); err != nil {
			return nil, err
		}
	}
	pinned := make([]string, 0, len(ips))
	for ip := range ips {
		pinned = append(pinned, ip)
	}
	sort.Strings(pinned)
	var taskID, convID *int64
	if scope.TaskID > 0 {
		taskID = &scope.TaskID
	} else {
		convID = &scope.ConversationID
	}
	resolvedAt := t.ResolvedAt
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	pin, err := scanTargetPin(tx.QueryRow(`
INSERT INTO target_pins(task_id, conversation_id, host, port, version, pinned_ips, resolver, resolved_at, intercept_id, decided_by, decided_from, name_only_effective)
VALUES ($1, $2, $3, $4, $5, $6::text[], $7, $8, $9, $10, $11, $12)
RETURNING `+targetPinCols,
		taskID, convID, t.Host, t.Port, version, pinned, t.Resolver, resolvedAt, interceptID, approver, remoteAddr, t.NameOnlyEffective))
	if err != nil {
		return nil, err
	}
	t.Outcome, t.PinID, t.PinVersion, t.PinnedIPs = "pinned", pin.ID, pin.Version, pin.PinnedIPs
	return pin, nil
}
