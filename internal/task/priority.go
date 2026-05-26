package task

import (
	"fmt"
	"strings"
)

// Priority is the canonical task priority. The persisted form is the
// lowercase string ("urgent", "high", "normal", "low", or empty); the
// Go-side type adds compile-time safety over plain string.
type Priority string

// Canonical priority values. The empty Priority is treated as PriorityNormal
// by the scheduler ordinal but is preserved on disk to distinguish "unset"
// from "explicitly normal".
const (
	PriorityUrgent Priority = "urgent"
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

// ParsePriority canonicalizes user input (case- and whitespace-insensitive)
// into a Priority value. Empty input returns the empty Priority with no
// error — the scheduler treats it the same as PriorityNormal but the
// distinction survives in storage.
func ParsePriority(s string) (Priority, error) {
	normalized := strings.ToLower(strings.TrimSpace(s))
	switch Priority(normalized) {
	case "", PriorityUrgent, PriorityHigh, PriorityNormal, PriorityLow:
		return Priority(normalized), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidPriority, s)
	}
}

// Ordinal returns the scheduler sort key matching the SQL CASE in
// store.schedulerOrderSQL. Lower ordinal sorts first. Unknown values are
// treated as normal, matching the SQL ELSE branch.
func (p Priority) Ordinal() int {
	switch Priority(strings.ToLower(strings.TrimSpace(string(p)))) {
	case PriorityUrgent:
		return 0
	case PriorityHigh:
		return 1
	case PriorityLow:
		return 3
	default:
		return 2
	}
}
