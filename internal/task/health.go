package task

// QueueHealth summarizes bounded, actionable queue-health signals.
// Age fields and TerminalFailureRate are nil when their denominator is empty.
type QueueHealth struct {
	WindowSeconds          int64    `json:"window_seconds"`
	OldestReadyAgeSeconds  *int64   `json:"oldest_ready_age_seconds"`
	OldestActiveAgeSeconds *int64   `json:"oldest_active_age_seconds"`
	StaleRequeues          int      `json:"stale_requeues"`
	RetryAttempts          int      `json:"retry_attempts"`
	TerminalAttempts       int      `json:"terminal_attempts"`
	TerminalFailures       int      `json:"terminal_failures"`
	TerminalFailureRate    *float64 `json:"terminal_failure_rate"`
}
