package server

import (
	"log"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/agent"
)

// triageClient builds the advisory triage client from the configured profile, or
// nil when triage is off. The profile's base_url must serve the decision-model
// contract at <base_url>/systemone; the model is the profile's model. Built per
// agent run (cheap: one settings read + one profile read), so toggling the setting
// takes effect on the next run without an agent rebuild.
//
// Fail-open by design: an unreadable setting, a missing profile or a profile with
// no base_url/model returns nil, and report_finding behaves exactly as before.
func (s *Server) triageClient() agent.TriageClient {
	id := s.triageProfileID()
	log.Printf("[triage] triageClient: profile_id=%d", id)
	if id <= 0 {
		return nil
	}
	p, err := s.m.pg.ProfileByID(id)
	if err != nil || p == nil {
		return nil
	}
	if strings.TrimSpace(p.BaseURL) == "" || strings.TrimSpace(p.Model) == "" {
		return nil
	}
	return &agent.SystemOneClient{BaseURL: p.BaseURL, APIKey: p.APIKey, Model: p.Model}
}

// applyTriageThreshold pushes the stored threshold into the agent package. Called on
// boot and on a settings change; an unreadable or out-of-range value keeps the default.
func (s *Server) applyTriageThreshold() {
	if s.m.pg == nil {
		return
	}
	raw, ok, err := s.m.pg.GetSetting(settingTriageMinConfidence)
	if err != nil || !ok {
		return
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil && v > 0 && v <= 1 {
		agent.SetTriageMinConfidence(v)
	}
}

// triageMinConfidence reports the stored threshold for the settings API (0 = default).
func (s *Server) triageMinConfidence() float64 {
	if s.m.pg == nil {
		return 0
	}
	raw, ok, err := s.m.pg.GetSetting(settingTriageMinConfidence)
	if err != nil || !ok {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0
	}
	return v
}

// triageProfileID returns the configured triage profile id, 0 when off or unreadable.
func (s *Server) triageProfileID() int64 {
	if s.m.pg == nil {
		return 0
	}
	raw, ok, err := s.m.pg.GetSetting(settingTriageProfile)
	if err != nil || !ok {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id < 0 {
		return 0
	}
	return id
}
