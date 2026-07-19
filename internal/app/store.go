package app

import (
	"context"
	"time"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
)

// Store persists tasks and owns atomic queue operations.
// Defined here (where consumed) per Go interface idiom.
type Store interface {
	List(ctx context.Context) ([]task.Task, error)
	Get(ctx context.Context, id string) (task.Task, error)
	Counts(ctx context.Context) (map[task.Status]int, error)
	ActiveLists(ctx context.Context) (todo, doing []task.Task, err error)
	QueueHealth(ctx context.Context, now time.Time, window time.Duration) (task.QueueHealth, error)
	ListByStatus(ctx context.Context, status task.Status) ([]task.Task, error)
	Ready(ctx context.Context) ([]task.Task, error)
	ExplainNotReady(ctx context.Context) (store.NotReadyExplanation, error)
	Add(ctx context.Context, t task.Task) error
	Update(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error
	UpdateGuarded(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error
	UpdateFenced(ctx context.Context, id string, expectWorker string, event task.EventType, message string, fn func(*task.Task) bool) error
	UpdateFencedTask(ctx context.Context, id string, expectWorker string, event task.EventType, message string, fn func(*task.Task) bool) (task.Task, error)
	Delete(ctx context.Context, id string) error
	// Prune physically removes matching rows. Public callers should prefer
	// status=deleted so task history remains inspectable.
	Prune(ctx context.Context, statuses []task.Status) (int, error)
	ClaimNextForWorker(ctx context.Context, now time.Time, leaseExpires time.Time, workerID, agent string) (*task.Task, error)
	Heartbeat(ctx context.Context, taskID, workerID string, now time.Time, leaseExpires time.Time) error
	AddDependency(ctx context.Context, taskID, dependsOnID string) error
	AddRelation(ctx context.Context, taskID, relatedID string, relType task.RelationType) error
	Dependencies(ctx context.Context, taskID string) ([]task.Dependency, error)
	AddGate(ctx context.Context, taskID, name string) error
	SatisfyGate(ctx context.Context, taskID, name string) error
	Gates(ctx context.Context, taskID string) ([]task.Gate, error)
	Events(ctx context.Context, taskID string) ([]task.Event, error)
	Attempts(ctx context.Context, taskID string) ([]task.Attempt, error)
	RequeueStale(ctx context.Context, olderThan time.Duration, now time.Time) ([]task.Task, error)
	// RecentDistinctCWDs returns up to limit distinct non-empty task working
	// directories, selected by most-recent task per directory, then sorted
	// alphabetically. limit <= 0 means no limit.
	RecentDistinctCWDs(ctx context.Context, limit int) ([]string, error)
	AddGoalGroup(ctx context.Context, g task.GoalGroup) error
	CreateGoal(ctx context.Context, g task.GoalGroup, tasks []task.Task) error
	GetGoalGroup(ctx context.Context, id string) (task.GoalGroup, error)
	UpdateGoalGroupStatus(ctx context.Context, id, status string) error
	// CountTasksByGroupID returns per-status task counts for a single goal group.
	CountTasksByGroupID(ctx context.Context, groupID string) (map[string]int, error)
	// BudgetLimitGroup suspends the still-active (todo/doing) tasks in a group by
	// setting their status to budget-limited. Done/failed/deleted tasks are left
	// untouched.
	BudgetLimitGroup(ctx context.Context, groupID string) error
	PrepareGoalInvocation(ctx context.Context, taskID string, now time.Time) (task.GoalGroup, int64, bool, error)
	FinalizeGoalInvocation(ctx context.Context, in store.GoalFinalization) (store.GoalFinalizeResult, error)
	ResumeGoal(ctx context.Context, goalID string, changes store.GoalResumeChanges, now time.Time) (store.GoalResumeResult, error)
}

var _ Store = (*store.SQLiteStore)(nil)
