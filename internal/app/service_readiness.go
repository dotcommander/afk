package app

import (
	"context"
	"fmt"

	"github.com/dotcommander/afk/internal/task"
)

// tasksByID indexes a task slice by ID for O(1) lookup.
func tasksByID(tasks []task.Task) map[string]task.Task {
	byID := make(map[string]task.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	return byID
}

func (s *Service) notReadyReasons(ctx context.Context, id string, tasks map[string]task.Task) ([]NotReadyReason, error) {
	t, ok := tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %s: %w", id, ErrNotFound)
	}

	reasons, err := s.manualBlockReasons(ctx, id)
	if err != nil {
		return nil, err
	}
	reasons = append(reasons, s.resourceLockReasons(id, t, tasks)...)

	dependencyReasons, err := s.dependencyReasons(ctx, id, tasks)
	if err != nil {
		return nil, err
	}
	reasons = append(reasons, dependencyReasons...)
	return reasons, nil
}

func (s *Service) manualBlockReasons(ctx context.Context, id string) ([]NotReadyReason, error) {
	block, err := s.store.BlockForTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if block == nil {
		return nil, nil
	}
	return []NotReadyReason{{TaskID: id, Kind: "manual_block", Detail: block.Reason}}, nil
}

func (s *Service) resourceLockReasons(id string, t task.Task, tasks map[string]task.Task) []NotReadyReason {
	if t.ResourceKey != "" {
		for _, active := range tasks {
			if active.ID == id || active.Status != task.StatusWorking || active.ResourceKey != t.ResourceKey {
				continue
			}
			return []NotReadyReason{{TaskID: id, Kind: "resource_locked", Detail: active.ID}}
		}
	}
	return nil
}

func (s *Service) dependencyReasons(ctx context.Context, id string, tasks map[string]task.Task) ([]NotReadyReason, error) {
	deps, err := s.store.Dependencies(ctx, id)
	if err != nil {
		return nil, err
	}
	reasons := make([]NotReadyReason, 0, len(deps))
	for _, dep := range deps {
		depTask, ok := tasks[dep.DependsOnID]
		if !ok {
			reasons = append(reasons, NotReadyReason{
				TaskID: id,
				Kind:   "dependency_missing",
				Detail: dep.DependsOnID,
			})
			continue
		}
		switch depTask.Status {
		case task.StatusDone:
			continue
		case task.StatusPending:
			reasons = append(reasons, NotReadyReason{TaskID: id, Kind: "dependency_pending", Detail: dep.DependsOnID})
		case task.StatusWorking:
			reasons = append(reasons, NotReadyReason{TaskID: id, Kind: "dependency_working", Detail: dep.DependsOnID})
		case task.StatusFailed:
			reasons = append(reasons, NotReadyReason{TaskID: id, Kind: "dependency_failed", Detail: dep.DependsOnID})
		default:
			reasons = append(reasons, NotReadyReason{TaskID: id, Kind: "dependency_not_done", Detail: dep.DependsOnID})
		}
	}
	return reasons, nil
}
