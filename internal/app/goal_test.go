package app

// Internal test (package app) so it can reach the unexported insertGoalTasks
// helper and package-level audit-agent seams. These tests cover goal config,
// task insertion rollback, and audit decisions.

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// --- §1: GoalConfig load/write ---

// TestGoalConfig covers the load/write behavior contract. LoadGoalConfig reads
// ~/.config/afk/goal.yaml once (sync.Once) and falls back to built-in defaults
// when absent or malformed; an empty SetupCommand fails closed at RunGoalSetup.
func TestGoalConfig(t *testing.T) {
	t.Parallel()

	t.Run("defaults loadable", func(t *testing.T) {
		t.Parallel()
		cfg := LoadGoalConfig()
		// defaultGoalConfig must be the in-memory fallback shape.
		_ = cfg
		if defaultGoalConfig().SetupCommand != "" {
			t.Fatalf("default SetupCommand must be empty (fail-closed)")
		}
	})

	t.Run("empty SetupCommand fails closed", func(t *testing.T) {
		t.Parallel()
		s := newGoalTestService(&goalStoreStub{})
		_, err := s.RunGoalSetup(context.Background(), GoalConfig{}, "obj", nil, nil)
		if !errors.Is(err, ErrGoalSetupNotConfigured) {
			t.Fatalf("RunGoalSetup with empty SetupCommand: err = %v, want ErrGoalSetupNotConfigured", err)
		}
	})
}

// --- §4: insertGoalTasks rollback / success / empty ---

func TestInsertGoalTasks(t *testing.T) {
	t.Parallel()

	t.Run("empty tasks returns ErrGoalNoTasks", func(t *testing.T) {
		t.Parallel()
		s := newGoalTestService(&goalStoreStub{})
		err := s.insertGoalTasks(context.Background(), "goal-1", nil, "/tmp")
		if !errors.Is(err, ErrGoalNoTasks) {
			t.Fatalf("insertGoalTasks([]) = %v, want ErrGoalNoTasks", err)
		}
	})

	t.Run("error on 3rd Add rolls back first two as deleted", func(t *testing.T) {
		t.Parallel()
		stub := &goalStoreStub{failAddOn: 3}
		s := newGoalTestService(stub)
		err := s.insertGoalTasks(context.Background(), "goal-1",
			[]string{"a", "b", "c"}, "/tmp")
		if err == nil {
			t.Fatalf("insertGoalTasks expected error on 3rd Add")
		}
		// First two inserts must be rolled back to deleted. Guard the slice
		// bound so the assertion fails cleanly instead of panicking.
		if len(stub.added) < 2 {
			t.Fatalf("only %d tasks inserted before failure, want >= 2 for rollback check", len(stub.added))
		}
		for _, id := range stub.added[:2] {
			if stub.statuses[id] != task.StatusDeleted {
				t.Fatalf("task %s status = %q, want deleted (rollback)", id, stub.statuses[id])
			}
		}
	})

	t.Run("success wires dependency chain in order", func(t *testing.T) {
		t.Parallel()
		stub := &goalStoreStub{}
		s := newGoalTestService(stub)
		if err := s.insertGoalTasks(context.Background(), "goal-1",
			[]string{"a", "b", "c"}, "/tmp"); err != nil {
			t.Fatalf("insertGoalTasks success path errored: %v", err)
		}
		if len(stub.added) != 3 {
			t.Fatalf("added %d tasks, want 3", len(stub.added))
		}
		// task[1] depends on task[0]; task[2] depends on task[1].
		wantDeps := map[string]string{
			stub.added[1]: stub.added[0],
			stub.added[2]: stub.added[1],
		}
		for taskID, dep := range wantDeps {
			if stub.deps[taskID] != dep {
				t.Fatalf("task %s depends_on %q, want %q", taskID, stub.deps[taskID], dep)
			}
		}
	})
}

// --- RunGoalAudit / RequeueAfterAuditDisapproval ---

func TestRunGoalAudit(t *testing.T) {
	t.Parallel()

	cfg := GoalConfig{
		AuditCommand:        "stub-audit {{.Prompt}}",
		AuditPromptTemplate: `<objective>{{.EscapedObjective}}</objective>{{.CompletionNote}}`,
		AuditTimeout:        time.Minute,
	}

	t.Run("empty AuditCommand returns ErrGoalAuditNotConfigured", func(t *testing.T) {
		t.Parallel()
		s := newGoalTestService(&goalStoreStub{})
		_, err := s.RunGoalAudit(context.Background(), GoalConfig{}, "g1", "t1", "done", nil, nil)
		if !errors.Is(err, ErrGoalAuditNotConfigured) {
			t.Fatalf("RunGoalAudit empty command: err = %v, want ErrGoalAuditNotConfigured", err)
		}
	})

	// The seam-swapping subtests below mutate the package-level runAuditAgent
	// var, so they run sequentially (no t.Parallel) to avoid racing each other.
	t.Run("approved marker -> Approved", func(t *testing.T) {
		restore := swapAuditAgent(func(_ context.Context, _, _ string, _ time.Duration, stdout, _ io.Writer) error {
			_, _ = io.WriteString(stdout, "looks good\n<approved/>")
			return nil
		})
		defer restore()
		s := newGoalTestService(&goalStoreStub{groups: map[string]task.GoalGroup{"g1": {ID: "g1", Objective: "ship it"}}})
		res, err := s.RunGoalAudit(context.Background(), cfg, "g1", "t1", "done", nil, nil)
		if err != nil {
			t.Fatalf("RunGoalAudit: unexpected error %v", err)
		}
		if !res.Approved || res.Disapproved {
			t.Fatalf("res = %+v, want Approved=true Disapproved=false", res)
		}
	})

	t.Run("disapproved marker -> Disapproved", func(t *testing.T) {
		restore := swapAuditAgent(func(_ context.Context, _, _ string, _ time.Duration, stdout, _ io.Writer) error {
			_, _ = io.WriteString(stdout, "missing tests\n<disapproved/>")
			return nil
		})
		defer restore()
		s := newGoalTestService(&goalStoreStub{groups: map[string]task.GoalGroup{"g1": {ID: "g1", Objective: "ship it"}}})
		res, err := s.RunGoalAudit(context.Background(), cfg, "g1", "t1", "done", nil, nil)
		if err != nil {
			t.Fatalf("RunGoalAudit: unexpected error %v", err)
		}
		if res.Approved || !res.Disapproved {
			t.Fatalf("res = %+v, want Approved=false Disapproved=true", res)
		}
	})

	t.Run("agent error -> fail-safe disapproval", func(t *testing.T) {
		restore := swapAuditAgent(func(_ context.Context, _, _ string, _ time.Duration, _, _ io.Writer) error {
			return ErrAgentTimeout
		})
		defer restore()
		s := newGoalTestService(&goalStoreStub{groups: map[string]task.GoalGroup{"g1": {ID: "g1", Objective: "ship it"}}})
		res, err := s.RunGoalAudit(context.Background(), cfg, "g1", "t1", "done", nil, nil)
		if err != nil {
			t.Fatalf("RunGoalAudit: timeout must be recorded in result, not returned: %v", err)
		}
		if res.Approved {
			t.Fatalf("timed-out audit must never approve: %+v", res)
		}
		if !res.Disapproved || res.Error == "" {
			t.Fatalf("res = %+v, want Disapproved=true and Error set", res)
		}
	})
}

func TestRequeueAfterAuditDisapproval(t *testing.T) {
	t.Parallel()

	stub := &goalStoreStub{}
	stub.ensure()
	stub.statuses["t1"] = task.StatusDoing
	s := newGoalTestService(stub)
	if err := s.RequeueAfterAuditDisapproval(context.Background(), "t1", "auditor disapproved: missing tests"); err != nil {
		t.Fatalf("RequeueAfterAuditDisapproval: %v", err)
	}
	if stub.statuses["t1"] != task.StatusTodo {
		t.Fatalf("task status = %q, want todo", stub.statuses["t1"])
	}
}

// swapAuditAgent substitutes the audit exec seam for the duration of a test and
// returns a restore closure.
func swapAuditAgent(fn func(context.Context, string, string, time.Duration, io.Writer, io.Writer) error) func() {
	prev := runAuditAgent
	runAuditAgent = fn
	return func() { runAuditAgent = prev }
}

// --- helpers ---

func newGoalTestService(stub *goalStoreStub) *Service {
	n := 0
	return NewService(stub, func() time.Time { return goalFixedNow },
		WithIDGenerator(func() string {
			n++
			return goalTestID(n)
		}))
}

var goalFixedNow = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

func goalTestID(n int) string {
	return "t" + string(rune('0'+n))
}

// goalStoreStub is an in-process Store for insertGoalTasks tests; no SQLite.
// It embeds Store so methods not exercised by these tests are nil and panic
// loudly if hit unexpectedly.
type goalStoreStub struct {
	Store
	failAddOn int                       // 1-based Add call index to fail on; 0 = never
	addCalls  int                       // count of Add calls
	added     []string                  // ids in insertion order
	statuses  map[string]task.Status    // current status per id
	deps      map[string]string         // taskID -> dependsOnID (last wins)
	groups    map[string]task.GoalGroup // goalID -> group (for audit objective lookup)
}

func (s *goalStoreStub) GetGoalGroup(_ context.Context, id string) (task.GoalGroup, error) {
	if g, ok := s.groups[id]; ok {
		return g, nil
	}
	return task.GoalGroup{}, ErrNotFound
}

func (s *goalStoreStub) ensure() {
	if s.statuses == nil {
		s.statuses = map[string]task.Status{}
	}
	if s.deps == nil {
		s.deps = map[string]string{}
	}
}

func (s *goalStoreStub) Add(_ context.Context, t task.Task) error {
	s.ensure()
	s.addCalls++
	if s.failAddOn != 0 && s.addCalls == s.failAddOn {
		return errors.New("stub: Add failed")
	}
	s.added = append(s.added, t.ID)
	s.statuses[t.ID] = task.StatusTodo
	return nil
}

func (s *goalStoreStub) Update(_ context.Context, id string, _ task.EventType, _ string, fn func(*task.Task) bool) error {
	s.ensure()
	t := task.Task{ID: id, Status: s.statuses[id]}
	fn(&t)
	s.statuses[id] = t.Status
	return nil
}

func (s *goalStoreStub) UpdateGuarded(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error {
	return s.Update(ctx, id, event, message, fn)
}

func (s *goalStoreStub) Delete(_ context.Context, id string) error {
	s.ensure()
	s.statuses[id] = task.StatusDeleted
	return nil
}

func (s *goalStoreStub) AddDependency(_ context.Context, taskID, dependsOnID string) error {
	s.ensure()
	s.deps[taskID] = dependsOnID
	return nil
}

func (s *goalStoreStub) AddRelation(_ context.Context, _, _ string, _ task.RelationType) error {
	return nil
}
