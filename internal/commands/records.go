package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
	"github.com/google/uuid"
	"time"
)

type CheckpointCmd struct {
	Add  CheckpointAddCmd  `cmd:""`
	List CheckpointListCmd `cmd:""`
}
type CheckpointAddCmd struct {
	TaskID       string `arg:"" required:""`
	ValueJSON    string `arg:"" required:""`
	Kind         string `default:"progress"`
	Key          string
	SourceSystem string `name:"source-system" default:"afk-cli"`
	SourceID     string `name:"source-id"`
}

func (c *CheckpointAddCmd) Run(d *Deps, ctx context.Context) error {
	if !json.Valid([]byte(c.ValueJSON)) {
		return fmt.Errorf("checkpoint value must be valid JSON")
	}
	v := task.Checkpoint{TaskID: c.TaskID, Kind: c.Kind, Key: c.Key, ValueJSON: c.ValueJSON, Provenance: task.Provenance{System: c.SourceSystem, ID: c.SourceID}}
	if v.Provenance.ID == "" {
		v.Provenance.ID = uuid.NewString()
	}
	v.CreatedAt = d.Now().UTC().Format(time.RFC3339)
	p, err := d.Service.AddCheckpoint(ctx, v)
	if err != nil {
		return err
	}
	return output.WriteJSONLine(d.Stdout, p, "checkpoint add")
}

type CheckpointListCmd struct {
	TaskID string `arg:"" required:""`
}

func (c *CheckpointListCmd) Run(d *Deps, ctx context.Context) error {
	rows, err := d.Service.Checkpoints(ctx, c.TaskID)
	if err != nil {
		return err
	}
	return output.WriteJSONLine(d.Stdout, rows, "checkpoint list")
}

type ArtifactCmd struct {
	Add  ArtifactAddCmd  `cmd:""`
	List ArtifactListCmd `cmd:""`
}
type ArtifactAddCmd struct {
	TaskID       string `arg:"" required:""`
	Path         string `arg:"" required:""`
	ID           string
	ContentType  string `name:"content-type"`
	Metadata     string `default:"{}"`
	SourceSystem string `name:"source-system" default:"afk-cli"`
	SourceID     string `name:"source-id"`
}

func (c *ArtifactAddCmd) Run(d *Deps, ctx context.Context) error {
	if !json.Valid([]byte(c.Metadata)) {
		return fmt.Errorf("artifact metadata must be valid JSON")
	}
	a := task.Artifact{ID: c.ID, TaskID: c.TaskID, Path: c.Path, ContentType: c.ContentType, MetadataJSON: c.Metadata, Provenance: task.Provenance{System: c.SourceSystem, ID: c.SourceID}}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.Provenance.ID == "" {
		a.Provenance.ID = a.ID
	}
	a.CreatedAt = d.Now().UTC().Format(time.RFC3339)
	if err := d.Service.AddArtifact(ctx, a); err != nil {
		return err
	}
	return output.WriteJSONLine(d.Stdout, a, "artifact add")
}

type ArtifactListCmd struct {
	TaskID string `arg:"" required:""`
}

func (c *ArtifactListCmd) Run(d *Deps, ctx context.Context) error {
	rows, err := d.Service.Artifacts(ctx, c.TaskID)
	if err != nil {
		return err
	}
	return output.WriteJSONLine(d.Stdout, rows, "artifact list")
}
