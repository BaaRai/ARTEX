package server

// stalledAfter is how long a running intent may go without any activity, while
// no model call is in progress, before the task is reported as stalled.
const stalledAfter = 120 // seconds

// engineModeInput is the live engine state for one task.
type engineModeInput struct {
	Paused       bool
	Running      bool  // engine ready, started and not paused
	ActiveCalls  int64 // model calls in progress
	InFlight     int   // persisted intents in state "running"
	LastActivity int64 // unix seconds of the last engine heartbeat, 0 if none
}

// engineModeFor maps live engine state to the spec enum
// exploring | paused | stalled | idle. A live event-driven engine with no work
// is healthy idle; only a persisted running intent with no model call and no
// activity for stalledAfter seconds counts as stalled.
func engineModeFor(in engineModeInput, now int64) string {
	switch {
	case in.Paused:
		return "paused"
	case in.Running && in.ActiveCalls == 0 && in.InFlight > 0 && in.LastActivity > 0 && now-in.LastActivity > stalledAfter:
		return "stalled"
	case in.Running && in.ActiveCalls > 0:
		return "exploring"
	default:
		return "idle"
	}
}
