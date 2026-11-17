package server

import (
	"encoding/json"
	"log"

	"github.com/Autumn-27/artex/agent"
)

// SeedTool only inserts new rows. Repair existing built-ins too, while keeping
// descriptions, bindings, enabled/deferred flags and parameter annotations.
func (s *Server) seedFactInputSchema() {
	row, err := s.m.pg.GetTool("record_fact")
	if err != nil {
		log.Printf("[facts] read record_fact schema: %v", err)
		return
	}
	if row == nil || !row.System || row.Kind != "builtin" {
		return
	}
	var schema map[string]any
	if err := json.Unmarshal(row.Schema, &schema); err != nil {
		log.Printf("[facts] decode record_fact schema: %v", err)
		return
	}
	raw, err := json.Marshal(agent.RepairRecordFactSchema(schema))
	if err != nil {
		log.Printf("[facts] encode record_fact schema: %v", err)
		return
	}
	// Compare-and-swap preserves concurrent operator edits; JSONB equality
	// avoids changing updated_at again on every startup.
	if _, err := s.m.pg.Exec(`UPDATE tools SET schema=$2::jsonb, updated_at=now()
WHERE key='record_fact' AND system AND kind='builtin'
  AND schema=$1::jsonb AND schema IS DISTINCT FROM $2::jsonb`, string(row.Schema), string(raw)); err != nil {
		log.Printf("[facts] repair record_fact schema: %v", err)
	}
}
