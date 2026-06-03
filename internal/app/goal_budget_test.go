package app

import (
	"testing"
	"testing/synctest"
	"time"
)

// TestBudgetState exercises BudgetState accounting per Verification Surface §2.
// Behavior is implemented in Phase B; these tests encode the contract and may
// fail until then.
func TestBudgetState(t *testing.T) {
	t.Parallel()

	t.Run("AccountIteration accumulates tokens", func(t *testing.T) {
		t.Parallel()
		var b BudgetState
		now := time.Unix(0, 0)
		for range 3 {
			b.AccountIteration(LoopResult{}, 500, now)
		}
		if b.TokensUsed != 1500 {
			t.Fatalf("TokensUsed = %d, want 1500", b.TokensUsed)
		}
	})

	t.Run("BudgetOK when all caps zero", func(t *testing.T) {
		t.Parallel()
		var b BudgetState
		if got := b.BudgetExceeded(GoalConfig{}, time.Unix(0, 0)); got != BudgetOK {
			t.Fatalf("BudgetExceeded = %v, want BudgetOK", got)
		}
	})

	t.Run("BudgetTokensExceeded", func(t *testing.T) {
		t.Parallel()
		b := BudgetState{TokensUsed: 100}
		cfg := GoalConfig{MaxTokens: 100}
		if got := b.BudgetExceeded(cfg, time.Unix(0, 0)); got != BudgetTokensExceeded {
			t.Fatalf("BudgetExceeded = %v, want BudgetTokensExceeded", got)
		}
	})

	t.Run("BudgetIterations", func(t *testing.T) {
		t.Parallel()
		b := BudgetState{Iterations: 5}
		cfg := GoalConfig{MaxIterations: 5}
		if got := b.BudgetExceeded(cfg, time.Unix(0, 0)); got != BudgetIterations {
			t.Fatalf("BudgetExceeded = %v, want BudgetIterations", got)
		}
	})

	t.Run("BudgetWallClock", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var b BudgetState
			start := time.Now()
			b.AccountIteration(LoopResult{}, 0, start)
			cfg := GoalConfig{MaxDuration: time.Minute}
			time.Sleep(2 * time.Minute)
			if got := b.BudgetExceeded(cfg, time.Now()); got != BudgetWallClock {
				t.Fatalf("BudgetExceeded = %v, want BudgetWallClock", got)
			}
		})
	})

	t.Run("StartedAt set once on first AccountIteration", func(t *testing.T) {
		t.Parallel()
		var b BudgetState
		first := time.Unix(100, 0)
		later := time.Unix(200, 0)
		b.AccountIteration(LoopResult{}, 0, first)
		if !b.StartedAt.Equal(first) {
			t.Fatalf("StartedAt = %v, want %v", b.StartedAt, first)
		}
		b.AccountIteration(LoopResult{}, 0, later)
		if !b.StartedAt.Equal(first) {
			t.Fatalf("StartedAt changed to %v, want unchanged %v", b.StartedAt, first)
		}
	})
}
