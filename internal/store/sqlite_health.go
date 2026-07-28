package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

const (
	oldestReadyHealthSQL = `
SELECT created FROM tasks
WHERE status = ? AND created != ''` + readyWhereSQL + `
ORDER BY created
LIMIT 1`
	oldestActiveHealthSQL = `
SELECT started FROM tasks
WHERE status = ? AND started != ''
ORDER BY started
LIMIT 1`
	staleRequeuesHealthSQL = `SELECT COUNT(*) FROM task_events WHERE type='requeued' AND message='stale' AND at>=?`
	retryAttemptsHealthSQL = `
SELECT COUNT(*)
FROM task_attempts current
WHERE current.started >= ?
AND EXISTS (
	SELECT 1 FROM task_attempts prior
	WHERE prior.task_id = current.task_id AND prior.id < current.id
)`
	terminalAttemptsHealthSQL = `
SELECT started, finished, status
FROM task_attempts
WHERE status IN ('done', 'failed') AND finished >= ? AND finished <= ?`
)

// QueueHealth returns queue ages plus lifecycle rates bounded to window.
func (s *SQLiteStore) QueueHealth(ctx context.Context, now time.Time, window time.Duration) (task.QueueHealth, error) {
	now = now.UTC()
	cutoff := now.Add(-window).Format(time.RFC3339)
	nowText := now.Format(time.RFC3339)
	health := task.QueueHealth{WindowSeconds: int64(window / time.Second)}

	readyCreated, err := s.oldestReadyCreated(ctx, nowText)
	if err != nil {
		return task.QueueHealth{}, err
	}
	health.OldestReadyAgeSeconds = ageSeconds(now, readyCreated)

	activeStarted, err := s.oldestActiveStarted(ctx)
	if err != nil {
		return task.QueueHealth{}, err
	}
	health.OldestActiveAgeSeconds = ageSeconds(now, activeStarted)

	if err := s.db.QueryRowContext(ctx, staleRequeuesHealthSQL, cutoff).Scan(&health.StaleRequeues); err != nil {
		return task.QueueHealth{}, fmt.Errorf("store: count stale requeues: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, retryAttemptsHealthSQL, cutoff).Scan(&health.RetryAttempts); err != nil {
		return task.QueueHealth{}, fmt.Errorf("store: count retry attempts: %w", err)
	}
	if err := s.populateTerminalAttemptHealth(ctx, cutoff, nowText, &health); err != nil {
		return task.QueueHealth{}, err
	}
	if health.TerminalAttempts > 0 {
		rate := float64(health.TerminalFailures) / float64(health.TerminalAttempts)
		health.TerminalFailureRate = &rate
	}
	return health, nil
}

func (s *SQLiteStore) populateTerminalAttemptHealth(ctx context.Context, cutoff, now string, health *task.QueueHealth) error {
	rows, err := s.db.QueryContext(ctx, terminalAttemptsHealthSQL, cutoff, now)
	if err != nil {
		return fmt.Errorf("store: query terminal attempts: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	durations := make([]float64, 0)
	for rows.Next() {
		var started, finished, status string
		if err := rows.Scan(&started, &finished, &status); err != nil {
			return fmt.Errorf("store: scan terminal attempt: %w", err)
		}
		health.TerminalAttempts++
		if status == string(task.StatusFailed) {
			health.TerminalFailures++
		}
		startedAt, startedErr := time.Parse(time.RFC3339, started)
		finishedAt, finishedErr := time.Parse(time.RFC3339, finished)
		if startedErr != nil || finishedErr != nil || finishedAt.Before(startedAt) {
			continue
		}
		durations = append(durations, finishedAt.Sub(startedAt).Seconds())
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: iterate terminal attempts: %w", err)
	}
	health.TerminalAttemptDurationSeconds = summarizeDurations(durations)
	return nil
}

func summarizeDurations(samples []float64) task.DurationDistribution {
	distribution := task.DurationDistribution{Count: len(samples)}
	if len(samples) == 0 {
		return distribution
	}

	var total float64
	for _, sample := range samples {
		total += sample
	}
	sort.Float64s(samples)
	avg := total / float64(len(samples))
	p50 := nearestRank(samples, 0.50)
	p90 := nearestRank(samples, 0.90)
	distribution.Avg = &avg
	distribution.P50 = &p50
	distribution.P90 = &p90
	return distribution
}

func nearestRank(sortedSamples []float64, percentile float64) float64 {
	rank := int(math.Ceil(percentile*float64(len(sortedSamples)))) - 1
	return sortedSamples[rank]
}

func (s *SQLiteStore) oldestReadyCreated(ctx context.Context, now string) (string, error) {
	var created string
	err := s.db.QueryRowContext(ctx, oldestReadyHealthSQL, string(task.StatusTodo), now, string(task.StatusDone), string(task.StatusDoing)).Scan(&created)
	if err == nil || err == sql.ErrNoRows {
		return created, nil
	}
	return "", fmt.Errorf("store: oldest ready task: %w", err)
}

func (s *SQLiteStore) oldestActiveStarted(ctx context.Context) (string, error) {
	var started string
	err := s.db.QueryRowContext(ctx, oldestActiveHealthSQL, string(task.StatusDoing)).Scan(&started)
	if err == nil || err == sql.ErrNoRows {
		return started, nil
	}
	return "", fmt.Errorf("store: oldest active task: %w", err)
}

func ageSeconds(now time.Time, value string) *int64 {
	if value == "" {
		return nil
	}
	then, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	age := int64(now.Sub(then) / time.Second)
	if age < 0 {
		age = 0
	}
	return &age
}
