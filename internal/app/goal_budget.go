package app

// Per-goal budget accounting: BudgetState tracks cumulative token/iteration/
// wall-clock use for one goal group and reports the first exceeded cap.

import "time"

// BudgetState tracks cumulative resource use for one goal group.
// Zero value is valid: all caps start at 0 (= not enforced).
type BudgetState struct {
	TokensUsed int
	Iterations int
	StartedAt  time.Time // zero = not started; set on first AccountIteration call
}

// BudgetLimitReason names the first budget cap that was exceeded.
type BudgetLimitReason int

// Budget limit reasons returned by BudgetExceeded; BudgetOK means within all caps.
const (
	BudgetOK             BudgetLimitReason = iota
	BudgetTokensExceeded                   // MaxTokens > 0 && TokensUsed >= MaxTokens
	BudgetIterations                       // MaxIterations > 0 && Iterations >= MaxIterations
	BudgetWallClock                        // MaxDuration > 0 && elapsed >= MaxDuration
)

// AccountIteration updates state from one LoopResult and a parsed token count.
// tokensThisIteration may be 0 when the agent output carries no token metadata.
// StartedAt is set once, on the first call.
func (b *BudgetState) AccountIteration(_ LoopResult, tokensThisIteration int, now time.Time) {
	if b.StartedAt.IsZero() {
		b.StartedAt = now
	}
	b.TokensUsed += tokensThisIteration
	b.Iterations++
}

// BudgetExceeded returns the first exceeded cap, or BudgetOK. A cap of 0 means
// unlimited and is never enforced.
func (b *BudgetState) BudgetExceeded(cfg GoalConfig, now time.Time) BudgetLimitReason {
	if cfg.MaxTokens > 0 && b.TokensUsed >= cfg.MaxTokens {
		return BudgetTokensExceeded
	}
	if cfg.MaxIterations > 0 && b.Iterations >= cfg.MaxIterations {
		return BudgetIterations
	}
	if cfg.MaxDuration > 0 && !b.StartedAt.IsZero() && now.Sub(b.StartedAt) >= cfg.MaxDuration {
		return BudgetWallClock
	}
	return BudgetOK
}
