package app

import (
	"context"
	"errors"

	"github.com/dotcommander/afk/internal/task"
	"github.com/google/uuid"
)

// AddCheckpoint writes one task-scoped durable progress record.
func (s *Service) AddCheckpoint(ctx context.Context, c task.Checkpoint) (task.Checkpoint, error) {
	writer, ok := s.store.(interface {
		AddCheckpoint(context.Context, task.Checkpoint) (task.Checkpoint, error)
	})
	if !ok {
		return task.Checkpoint{}, errors.New("checkpoints are unsupported by this store")
	}
	if c.CreatedAt == "" {
		c.CreatedAt = formatTime(s.now())
	}
	if c.Provenance.System == "" {
		c.Provenance.System = "afk-cli"
	}
	if c.Provenance.ID == "" {
		c.Provenance.ID = uuid.NewString()
	}
	return writer.AddCheckpoint(ctx, c)
}

// Checkpoints returns durable progress records in insertion order.
func (s *Service) Checkpoints(ctx context.Context, taskID string) ([]task.Checkpoint, error) {
	reader, ok := s.store.(interface {
		Checkpoints(context.Context, string) ([]task.Checkpoint, error)
	})
	if !ok {
		return nil, errors.New("checkpoints are unsupported by this store")
	}
	return reader.Checkpoints(ctx, taskID)
}

// AddArtifact writes one task-owned artifact record.
func (s *Service) AddArtifact(ctx context.Context, a task.Artifact) error {
	writer, ok := s.store.(interface {
		AddArtifact(context.Context, task.Artifact) error
	})
	if !ok {
		return errors.New("artifacts are unsupported by this store")
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt == "" {
		a.CreatedAt = formatTime(s.now())
	}
	if a.MetadataJSON == "" {
		a.MetadataJSON = "{}"
	}
	if a.Provenance.System == "" {
		a.Provenance.System = "afk-cli"
	}
	if a.Provenance.ID == "" {
		a.Provenance.ID = a.ID
	}
	return writer.AddArtifact(ctx, a)
}

// Artifacts returns task artifacts in source order.
func (s *Service) Artifacts(ctx context.Context, taskID string) ([]task.Artifact, error) {
	reader, ok := s.store.(interface {
		Artifacts(context.Context, string) ([]task.Artifact, error)
	})
	if !ok {
		return nil, errors.New("artifacts are unsupported by this store")
	}
	return reader.Artifacts(ctx, taskID)
}
