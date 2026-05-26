package task

import "time"

// ClaimDiagnostics describes derived, read-only worker claim state.
type ClaimDiagnostics struct {
	AgeSeconds int64  `json:"age_seconds,omitzero"`
	Stale      bool   `json:"stale,omitzero"`
	Reason     string `json:"reason,omitzero"`
}

// Blocker explains one unfinished dependency that keeps a task from being ready.
type Blocker struct {
	ID      string `json:"id"`
	Status  Status `json:"status,omitzero"`
	Missing bool   `json:"missing,omitzero"`
}

// BlockedTask carries a todo task plus the dependency blockers that keep it
// out of the ready set.
type BlockedTask struct {
	Task     Task
	Blockers []Blocker
}

// ClaimDiagnosticsFor derives non-mutating claim state for a doing task.
func ClaimDiagnosticsFor(t Task, now time.Time, unleasedStaleAfter time.Duration) (*ClaimDiagnostics, bool) {
	if NormalizeStatus(t.Status) != StatusDoing {
		return nil, false
	}
	started, ok := parseTime(t.Started)
	if !ok {
		return nil, false
	}
	age := now.UTC().Sub(started)
	if age < 0 {
		age = 0
	}
	diag := &ClaimDiagnostics{AgeSeconds: int64(age.Seconds())}
	if leaseExpires, ok := parseTime(t.LeaseExpires); ok && !leaseExpires.After(now.UTC()) {
		diag.Stale = true
		diag.Reason = "lease_expired"
		return diag, true
	}
	if t.LeaseExpires == "" && unleasedStaleAfter > 0 && age > unleasedStaleAfter {
		diag.Stale = true
		diag.Reason = "unleased_age"
	}
	return diag, true
}

func parseTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	return parsed.UTC(), err == nil
}
