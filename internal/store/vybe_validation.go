package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func validateAndNormalizeVybeData(ctx context.Context, data *vybeImportData) error {
	knownTasks, err := normalizeVybeTasks(ctx, data.tasks)
	if err != nil {
		return err
	}
	if err := normalizeVybeEvents(ctx, data.events); err != nil {
		return err
	}
	if err := normalizeVybeMemories(ctx, data.memories); err != nil {
		return err
	}
	return normalizeVybeArtifacts(ctx, data.artifacts, knownTasks)
}

func normalizeVybeTasks(ctx context.Context, sources []vybeTask) (map[string]struct{}, error) {
	knownTasks := make(map[string]struct{}, len(sources))
	for i := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source := &sources[i]
		if err := normalizeVybeTask(source, knownTasks); err != nil {
			return nil, err
		}
	}
	return knownTasks, nil
}

func normalizeVybeTask(source *vybeTask, knownTasks map[string]struct{}) error {
	if err := requireCanonicalID("task id", source.ID, false); err != nil {
		return err
	}
	if _, exists := knownTasks[source.ID]; exists {
		return fmt.Errorf("vybe task id %q is duplicated", source.ID)
	}
	knownTasks[source.ID] = struct{}{}
	if err := requireCanonicalID("task project_id", source.ProjectID, true); err != nil {
		return fmt.Errorf("vybe task %q: %w", source.ID, err)
	}
	var err error
	source.CreatedAt, err = normalizeVybeTimestamp("task created_at", source.ID, source.CreatedAt)
	if err != nil {
		return err
	}
	source.UpdatedAt, err = normalizeVybeTimestamp("task updated_at", source.ID, source.UpdatedAt)
	if err != nil {
		return err
	}
	_, _, _, err = mapVybeTask(*source)
	return err
}

func normalizeVybeEvents(ctx context.Context, events []vybeEvent) error {
	for i := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		event := &events[i]
		if err := requireCanonicalID("event task_id", event.TaskID, true); err != nil {
			return fmt.Errorf("vybe event %d: %w", event.ID, err)
		}
		var err error
		event.CreatedAt, err = normalizeVybeTimestamp("event created_at", fmt.Sprint(event.ID), event.CreatedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

func normalizeVybeMemories(ctx context.Context, memories []vybeMemory) error {
	for i := range memories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := normalizeVybeMemory(&memories[i]); err != nil {
			return err
		}
	}
	return nil
}

func normalizeVybeMemory(memory *vybeMemory) error {
	if err := requireCanonicalID("memory source_task_id", memory.SourceTaskID, true); err != nil {
		return fmt.Errorf("vybe memory %d: %w", memory.ID, err)
	}
	if memory.Scope == vybeScopeTask {
		if err := requireCanonicalID("task memory scope_id", memory.ScopeID, false); err != nil {
			return fmt.Errorf("vybe task memory %d: %w", memory.ID, err)
		}
		if memory.SourceTaskID != "" && memory.SourceTaskID != memory.ScopeID {
			return fmt.Errorf("vybe task memory %d scope_id %q conflicts with source_task_id %q", memory.ID, memory.ScopeID, memory.SourceTaskID)
		}
	}
	var err error
	memory.CreatedAt, err = normalizeVybeTimestamp("memory created_at", fmt.Sprint(memory.ID), memory.CreatedAt)
	return err
}

func normalizeVybeArtifacts(ctx context.Context, artifacts []vybeArtifact, knownTasks map[string]struct{}) error {
	for i := range artifacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := normalizeVybeArtifact(&artifacts[i], knownTasks); err != nil {
			return err
		}
	}
	return nil
}

func normalizeVybeArtifact(artifact *vybeArtifact, knownTasks map[string]struct{}) error {
	if err := requireCanonicalID("artifact id", artifact.ID, false); err != nil {
		return err
	}
	if err := requireCanonicalID("artifact task_id", artifact.TaskID, false); err != nil {
		return fmt.Errorf("vybe artifact %q: %w", artifact.ID, err)
	}
	if err := requireCanonicalID("artifact project_id", artifact.ProjectID, true); err != nil {
		return fmt.Errorf("vybe artifact %q: %w", artifact.ID, err)
	}
	if _, ok := knownTasks[artifact.TaskID]; !ok {
		return fmt.Errorf("vybe artifact %q references missing task %q", artifact.ID, artifact.TaskID)
	}
	var err error
	artifact.CreatedAt, err = normalizeVybeTimestamp("artifact created_at", artifact.ID, artifact.CreatedAt)
	return err
}

func requireCanonicalID(name, value string, allowEmpty bool) error {
	if value == "" && allowEmpty {
		return nil
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("vybe %s is empty", name)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("vybe %s %q is non-canonical", name, value)
	}
	return nil
}

func normalizeVybeTimestamp(name, id, value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", fmt.Errorf("vybe %s for %q is not RFC3339: %w", name, id, err)
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func nullJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "" {
		return json.RawMessage("null")
	}
	return raw
}
