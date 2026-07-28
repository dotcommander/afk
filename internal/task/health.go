package task

// DurationDistribution summarizes duration samples in seconds. Percentiles use
// the nearest-rank method. Statistics are nil when Count is zero.
type DurationDistribution struct {
	Count int      `json:"count"`
	Avg   *float64 `json:"avg"`
	P50   *float64 `json:"p50"`
	P90   *float64 `json:"p90"`
}

// QueueHealth summarizes bounded, actionable queue-health signals.
// Age fields and TerminalFailureRate are nil when their denominator is empty.
type QueueHealth struct {
	WindowSeconds                  int64                `json:"window_seconds"`
	OldestReadyAgeSeconds          *int64               `json:"oldest_ready_age_seconds"`
	OldestActiveAgeSeconds         *int64               `json:"oldest_active_age_seconds"`
	StaleRequeues                  int                  `json:"stale_requeues"`
	RetryAttempts                  int                  `json:"retry_attempts"`
	TerminalAttempts               int                  `json:"terminal_attempts"`
	TerminalFailures               int                  `json:"terminal_failures"`
	TerminalFailureRate            *float64             `json:"terminal_failure_rate"`
	TerminalAttemptDurationSeconds DurationDistribution `json:"terminal_attempt_duration_seconds"`
}
