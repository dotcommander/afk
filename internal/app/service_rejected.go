package app

import (
	"context"
	"fmt"

	"github.com/dotcommander/afk/internal/task"
)

// ListRejected returns every record in the sidecar. Returns ErrSidecarDisabled
// when the service was built without WithSidecarPath.
func (s *Service) ListRejected() ([]RejectionRecord, error) {
	if s.sidecarPath == "" {
		return nil, ErrSidecarDisabled
	}
	return ReadRejections(s.sidecarPath)
}

// RemoveRejected drops the rejection at idx (0-based) and returns the removed
// record. Returns ErrSidecarDisabled when no sidecar is configured and
// ErrRejectionIndexOutOfRange when idx is invalid.
func (s *Service) RemoveRejected(idx int) (RejectionRecord, error) {
	if s.sidecarPath == "" {
		return RejectionRecord{}, ErrSidecarDisabled
	}
	return RemoveRejectionAt(s.sidecarPath, idx)
}

// RetryRejected re-runs AddWithOptions for the rejection at idx (0-based)
// using the metadata captured at rejection time. On success the new task is
// returned and the record is removed from the sidecar. On validation failure the
// sidecar is untouched so the operator can continue to triage.
func (s *Service) RetryRejected(ctx context.Context, idx int) (task.Task, error) {
	if s.sidecarPath == "" {
		return task.Task{}, ErrSidecarDisabled
	}
	records, err := ReadRejections(s.sidecarPath)
	if err != nil {
		return task.Task{}, err
	}
	if idx < 0 || idx >= len(records) {
		return task.Task{}, ErrRejectionIndexOutOfRange
	}
	rec := records[idx]
	opts := task.AddOptions{
		Body:        rec.Body,
		Priority:    rec.Priority,
		Tags:        rec.Tags,
		CWD:         rec.CWD,
		Source:      rec.Source,
		Agent:       rec.Agent,
		GroupID:     rec.Group,
		ResourceKey: rec.ResourceKey,
	}
	id, err := s.AddWithOptions(ctx, opts)
	if err != nil {
		return task.Task{}, err
	}
	created, err := s.Show(ctx, id)
	if err != nil {
		return task.Task{}, err
	}
	if _, removeErr := RemoveRejectionAt(s.sidecarPath, idx); removeErr != nil {
		// Task created successfully; sidecar cleanup failed. Surface the
		// cleanup error but include the created task ID in the message so
		// the operator can verify state.
		return created, fmt.Errorf("retry succeeded as task %s but sidecar cleanup failed: %w", created.ID, removeErr)
	}
	return created, nil
}
