package task

import "time"

// GoalGroup is the durable record of a compiled goal: its objective, current
// status, and the group id that links its member tasks. No I/O — the store
// owns persistence.
type GoalGroup struct {
	ID        string `json:"id"`
	Objective string `json:"objective"`
	// Outcome is the contract's restated outcome, kept for reference; Objective is the raw user objective.
	Outcome            string        `json:"outcome"`
	Status             string        `json:"status"`
	CreatedAt          string        `json:"created_at"`
	GroupID            string        `json:"group_id"`
	MaxTokens          int64         `json:"max_tokens"`
	MaxIterations      int64         `json:"max_iterations"`
	MaxDuration        time.Duration `json:"max_duration_ns"`
	TokenRegex         string        `json:"token_regex"`
	BudgetEpochStarted string        `json:"epoch_started"`
	TokensUsed         int64         `json:"tokens_used"`
	IterationsUsed     int64         `json:"iterations_used"`
	LimitReason        string        `json:"reason"`
	LimitedAt          string        `json:"limited_at"`
}
