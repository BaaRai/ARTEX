package db

import (
	"encoding/json"
	"fmt"
)

// activityOutboxPayload is Activity plus Detail. Activity.Detail is omitted
// from ordinary JSON (the list payload stays small); the outbox has to keep it
// so a replayed row is the same step that failed to store.
type activityOutboxPayload struct {
	Activity
	Detail string `json:"detail,omitempty"`
}

func marshalActivity(a Activity) ([]byte, error) {
	return json.Marshal(activityOutboxPayload{Activity: a, Detail: a.Detail})
}

func unmarshalActivity(raw []byte) (Activity, error) {
	var payload activityOutboxPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Activity{}, err
	}
	a := payload.Activity
	a.Detail = payload.Detail
	return a, nil
}

// EnqueueActivity keeps one activity that AppendActivity could not store.
func (s *ExplorationStore) EnqueueActivity(a Activity, cause string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("activity outbox: no store")
	}
	raw, err := marshalActivity(a)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO activity_outbox (exploration_id, payload, last_error) VALUES ($1, $2::jsonb, $3)`, s.expID, string(raw), cause)
	return err
}

// FlushActivityOutbox inserts queued activities in order and deletes each row
// that lands. A row that still cannot be inserted stays, with last_error
// updated, and the flush stops so later rows keep their order. A payload that
// cannot be decoded is discarded.
func (s *ExplorationStore) FlushActivityOutbox() (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("activity outbox: no store")
	}
	rows, err := s.db.Query(`SELECT id, payload FROM activity_outbox WHERE exploration_id=$1 ORDER BY id`, s.expID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type item struct {
		id      int64
		payload []byte
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.payload); err != nil {
			return 0, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	stored := 0
	for _, it := range items {
		a, err := unmarshalActivity(it.payload)
		if err != nil {
			if _, delErr := s.db.Exec(`DELETE FROM activity_outbox WHERE id=$1`, it.id); delErr != nil {
				return stored, delErr
			}
			continue
		}
		if _, err := s.AppendActivity(a); err != nil {
			_, _ = s.db.Exec(`UPDATE activity_outbox SET last_error=$2 WHERE id=$1`, it.id, err.Error())
			return stored, err
		}
		if _, err := s.db.Exec(`DELETE FROM activity_outbox WHERE id=$1`, it.id); err != nil {
			return stored, err
		}
		stored++
	}
	return stored, nil
}
