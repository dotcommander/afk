package app

import (
	"context"

	"github.com/dotcommander/afk/internal/task"
)

// Blocked returns todo tasks with unfinished or missing dependency blockers.
func (s *Service) Blocked(ctx context.Context) ([]task.BlockedTask, error) {
	tasks, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]task.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	var blocked []task.BlockedTask
	for _, t := range tasks {
		if task.NormalizeStatus(t.Status) != task.StatusPending {
			continue
		}
		deps, err := s.store.Dependencies(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		var blockers []task.Blocker
		for _, dep := range deps {
			prereq, ok := byID[dep.DependsOnID]
			if !ok {
				blockers = append(blockers, task.Blocker{ID: dep.DependsOnID, Missing: true})
				continue
			}
			status := task.NormalizeStatus(prereq.Status)
			if status != task.StatusDone {
				blockers = append(blockers, task.Blocker{ID: dep.DependsOnID, Status: status})
			}
		}
		if len(blockers) > 0 {
			blocked = append(blocked, task.BlockedTask{Task: t, Blockers: blockers})
		}
	}
	return blocked, nil
}
