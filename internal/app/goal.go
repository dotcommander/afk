package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"

	storepkg "github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
)

// --- Sentinels (Error Model; finalized in Phases D/E) ---

var (
	// ErrGoalContractMissing means RunGoalSetup returned without a
	// <contract>...</contract> block.
	ErrGoalContractMissing = errors.New("goal contract missing from agent output (expected <contract>...</contract> block)")
	// ErrGoalSetupNotConfigured means GoalConfig.SetupCommand == "".
	ErrGoalSetupNotConfigured = errors.New("no setup command configured (set 'setup_command' in ~/.config/afk/goal.yaml or pass --setup-command)")
	// ErrGoalAuditNotConfigured means GoalConfig.AuditCommand == ""; auditor skipped.
	ErrGoalAuditNotConfigured = errors.New("no audit command configured (set 'audit_command' in ~/.config/afk/goal.yaml or pass --audit-command)")
	// ErrGoalNoTasks means GoalContract.Tasks is empty.
	ErrGoalNoTasks = errors.New("goal contract contains no tasks")
	// ErrGoalObjectiveTooLong means the objective exceeds 4000 characters.
	ErrGoalObjectiveTooLong = errors.New("objective exceeds 4000 characters")
	// ErrGoalDeclined means the user declined the compiled contract.
	ErrGoalDeclined = errors.New("goal declined by user")
)

// GoalContract is the compiled, human-approved goal: a structured description
// of what success looks like plus the ordered task DAG.
type GoalContract struct {
	Outcome      string   `json:"outcome"`
	DoneCriteria []string `json:"done_criteria"`
	MustDo       []string `json:"must_do"`
	Avoid        []string `json:"avoid"`
	Philosophy   string   `json:"philosophy"`
	// Tasks is the ordered list of task bodies; index order = dependency chain.
	Tasks []string `json:"tasks"`
}

// RunGoalSetup compiles an objective into a GoalContract via the configured
// setup agent and returns it for the caller to present and approve. It does NOT
// write to the queue.
func (s *Service) RunGoalSetup(
	ctx context.Context,
	cfg GoalConfig,
	objective string,
	setupOut, setupErr io.Writer,
) (GoalContract, error) {
	// Fail closed: no setup command configured.
	if cfg.SetupCommand == "" {
		return GoalContract{}, ErrGoalSetupNotConfigured
	}
	prompt, err := buildSetupPrompt(cfg, objective)
	if err != nil {
		return GoalContract{}, err
	}
	output, err := runGoalSetupAgent(ctx, cfg, prompt, setupOut, setupErr)
	if err != nil {
		return GoalContract{}, err
	}
	return parseGoalContract(output)
}

// InsertGoalTasks inserts the contract's tasks under goalID, wiring the
// dependency chain, with rollback on partial failure.
func (s *Service) InsertGoalTasks(ctx context.Context, goalID string, contract GoalContract, cwd string) error {
	if len(contract.Tasks) == 0 {
		return ErrGoalNoTasks
	}
	return s.insertGoalTasks(ctx, goalID, contract.Tasks, cwd)
}

// ValidateGoalConfig checks durable budget invariants before any goal rows are
// written. Token accounting is fail-closed whenever a token cap is configured.
func ValidateGoalConfig(cfg GoalConfig) error {
	if cfg.MaxTokens < 0 || cfg.MaxIterations < 0 || cfg.MaxDuration < 0 {
		return errors.New("goal budget limits must be nonnegative")
	}
	if cfg.MaxTokens == 0 {
		return nil
	}
	re, err := regexp.Compile(cfg.TokenRegex)
	if err != nil {
		return fmt.Errorf("compile token regex: %w", err)
	}
	if re.NumSubexp() != 1 {
		return errors.New("token regex must contain exactly one decimal capture group")
	}
	return nil
}

// CreateGoal atomically persists the approved group, all task rows/events, and
// its ordered dependency chain.
func (s *Service) CreateGoal(ctx context.Context, goalID, objective string, contract GoalContract, cwd string, cfg GoalConfig) error {
	if len(contract.Tasks) == 0 {
		return ErrGoalNoTasks
	}
	if err := ValidateGoalConfig(cfg); err != nil {
		return err
	}
	created := formatTime(s.now())
	members := make([]task.Task, 0, len(contract.Tasks))
	for _, body := range contract.Tasks {
		opts := task.AddOptions{Body: body, GroupID: goalID, Source: "goal:" + goalID, CWD: cwd}
		if err := task.ValidateAddOptions(opts); err != nil {
			return err
		}
		members = append(members, s.newTask(opts))
	}
	return s.store.CreateGoal(ctx, task.GoalGroup{
		ID: goalID, Objective: objective, Outcome: contract.Outcome, Status: "active",
		CreatedAt: created, GroupID: goalID, MaxTokens: int64(cfg.MaxTokens),
		MaxIterations: int64(cfg.MaxIterations), MaxDuration: cfg.MaxDuration, TokenRegex: cfg.TokenRegex,
	}, members)
}

// insertGoalTasks inserts each task body in order, blocking task N on task N-1
// (the contract DAG) and tagging each with GroupID=goalID. On any failure it
// rolls back the already-inserted tasks by marking them deleted, so a partial
// goal never lands in the queue.
func (s *Service) insertGoalTasks(ctx context.Context, goalID string, tasks []string, cwd string) error {
	if len(tasks) == 0 {
		return ErrGoalNoTasks
	}
	inserted := make([]string, 0, len(tasks))
	rollback := func() {
		for _, id := range inserted {
			_ = s.Remove(ctx, id)
		}
	}
	var prevID string
	for _, body := range tasks {
		id, err := s.AddWithOptions(ctx, task.AddOptions{
			Body:    body,
			GroupID: goalID,
			Source:  "goal:" + goalID,
			CWD:     cwd,
		})
		if err != nil {
			rollback()
			return err
		}
		inserted = append(inserted, id)
		if prevID != "" {
			if err := s.AddDependency(ctx, id, prevID); err != nil {
				rollback()
				return err
			}
		}
		// Goal-group membership is recorded by GroupID=goalID (set above), not a
		// task→task relation: goalID is a goal_groups row, not a task, so it has
		// no entry in the tasks table for AddRelation to reference.
		prevID = id
	}
	return nil
}

// GetGoalGroup returns the durable goal group record for goalID.
func (s *Service) GetGoalGroup(ctx context.Context, goalID string) (task.GoalGroup, error) {
	return s.store.GetGoalGroup(ctx, goalID)
}

// CountTasksByGroupID returns per-status task counts for a single goal group.
func (s *Service) CountTasksByGroupID(ctx context.Context, groupID string) (map[string]int, error) {
	return s.store.CountTasksByGroupID(ctx, groupID)
}

// ResumeGoal validates explicit durable budget changes before requeueing the
// goal's suspended members transactionally.
func (s *Service) ResumeGoal(ctx context.Context, goalID string, changes storepkg.GoalResumeChanges) (storepkg.GoalResumeResult, error) {
	if changes.MaxTokens == nil && changes.MaxIterations == nil && changes.MaxDuration == nil && changes.TokenRegex == nil {
		return storepkg.GoalResumeResult{}, errors.New("goal resume requires at least one explicit budget change")
	}
	goal, err := s.GetGoalGroup(ctx, goalID)
	if err != nil {
		return storepkg.GoalResumeResult{}, err
	}
	cfg := GoalConfig{MaxTokens: int(goal.MaxTokens), MaxIterations: int(goal.MaxIterations), MaxDuration: goal.MaxDuration, TokenRegex: goal.TokenRegex}
	if changes.MaxTokens != nil {
		cfg.MaxTokens = int(*changes.MaxTokens)
	}
	if changes.MaxIterations != nil {
		cfg.MaxIterations = int(*changes.MaxIterations)
	}
	if changes.MaxDuration != nil {
		cfg.MaxDuration = *changes.MaxDuration
	}
	if changes.TokenRegex != nil {
		cfg.TokenRegex = *changes.TokenRegex
	}
	if err := ValidateGoalConfig(cfg); err != nil {
		return storepkg.GoalResumeResult{}, err
	}
	return s.store.ResumeGoal(ctx, goalID, changes, s.now())
}

// RecordGoalGroup persists the goal group row after user approval. The RAW
// user objective is stored as the durable Objective (so goal status and the
// auditor judge against what the user actually asked); the contract's Outcome
// is stored separately in the Outcome column for reference. Status starts
// "active" and CreatedAt uses the Service clock.
func (s *Service) RecordGoalGroup(ctx context.Context, goalID, objective string, contract GoalContract) error {
	return s.store.AddGoalGroup(ctx, task.GoalGroup{
		ID:        goalID,
		Objective: objective,
		Outcome:   contract.Outcome,
		Status:    "active",
		CreatedAt: formatTime(s.now()),
		GroupID:   goalID,
	})
}
