package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dotcommander/afk/internal/task"
)

// ImportVybeArchive validates and atomically imports a frozen Vybe archive.
func (s *SQLiteStore) ImportVybeArchive(ctx context.Context, opts VybeImportOptions) (VybeImportReport, error) {
	data, err := readVybeArchive(ctx, opts.Source)
	if err != nil {
		return VybeImportReport{}, err
	}
	report := VybeImportReport{SourceSHA256: data.manifest.SourceSHA256, CutoverID: data.manifest.CutoverID, DryRun: !opts.Apply, SourceRows: data.manifest.RowCounts, ArchivedOnly: map[string]int64{}, ArchivedOrphans: map[string]int64{}}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, fmt.Errorf("store: begin Vybe import: %w", err)
	}
	defer rollback(tx)
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT report_json FROM vybe_imports WHERE source_sha256=?`, data.manifest.SourceSHA256).Scan(&prior)
	if err == nil {
		if err := json.Unmarshal([]byte(prior), &report); err != nil {
			return report, fmt.Errorf("store: decode prior Vybe import: %w", err)
		}
		report.AlreadyImported = true
		report.DryRun = !opts.Apply
		return report, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return report, fmt.Errorf("store: lookup Vybe import: %w", err)
	}
	active, err := s.importVybeTasks(ctx, tx, data.tasks, &report)
	if err != nil {
		return report, err
	}
	if err := importVybeEvents(ctx, tx, data.events, active, &report); err != nil {
		return report, err
	}
	if err := importVybeMemories(ctx, tx, data.memories, active, &report); err != nil {
		return report, err
	}
	if err := importVybeArtifacts(ctx, tx, data.artifacts, active, &report); err != nil {
		return report, err
	}
	report.ArchivedOnly["projects"] = data.manifest.RowCounts["projects"]
	report.ArchivedOnly["agent_state"] = data.manifest.RowCounts["agent_state"]
	report.ArchivedOnly["idempotency"] = data.manifest.RowCounts["idempotency"]
	if !opts.Apply {
		return report, nil
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return report, fmt.Errorf("store: encode Vybe report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO vybe_imports(source_sha256,cutover_id,report_json,imported_at) VALUES (?,?,?,?)`, report.SourceSHA256, report.CutoverID, string(encoded), s.nowString()); err != nil {
		return report, fmt.Errorf("store: record Vybe import: %w", err)
	}
	if err := commit(tx); err != nil {
		return report, err
	}
	return report, nil
}

func (s *SQLiteStore) importVybeTasks(ctx context.Context, tx *sql.Tx, sources []vybeTask, report *VybeImportReport) (map[string]bool, error) {
	active := make(map[string]bool, len(sources))
	for _, source := range sources {
		mapped, isActive, gate, err := mapVybeTask(source)
		if err != nil {
			return nil, err
		}
		if err := s.insertImportedTask(ctx, tx, mapped, source); err != nil {
			return nil, err
		}
		active[source.ID] = isActive
		if gate {
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_gates(task_id,name,satisfied,created) VALUES (?, 'legacy-vybe-blocked', 0, ?)`, source.ID, source.UpdatedAt); err != nil {
				return nil, fmt.Errorf("store: import blocked gate: %w", err)
			}
		}
		report.ImportedTasks++
	}
	return active, nil
}

func importVybeEvents(ctx context.Context, tx *sql.Tx, events []vybeEvent, active map[string]bool, report *VybeImportReport) error {
	for _, event := range events {
		if event.TaskID == "" {
			report.ArchivedOnly["events"]++
			continue
		}
		isActive, ok := active[event.TaskID]
		if !ok {
			report.ArchivedOnly["events"]++
			report.ArchivedOrphans["events"]++
			continue
		}
		if !isActive {
			report.ArchivedOnly["events"]++
			continue
		}
		meta, _ := json.Marshal(map[string]any{"vybe_event_id": event.ID, "kind": event.Kind, "agent": event.AgentName, "metadata": json.RawMessage(nullJSON(event.Metadata)), "message": event.Message})
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_events(task_id,type,at,message) VALUES (?, ?, ?, ?)`, event.TaskID, "legacy_vybe", event.CreatedAt, string(meta)); err != nil {
			return fmt.Errorf("store: import event %d: %w", event.ID, err)
		}
		report.ImportedEvents++
	}
	return nil
}

func importVybeMemories(ctx context.Context, tx *sql.Tx, memories []vybeMemory, active map[string]bool, report *VybeImportReport) error {
	for _, memory := range memories {
		if memory.Scope != vybeScopeTask {
			report.ArchivedOnly["memory"]++
			continue
		}
		taskID := strings.TrimSpace(memory.ScopeID)
		if taskID == "" {
			return fmt.Errorf("vybe task memory %d has an empty scope_id", memory.ID)
		}
		if sourceTaskID := strings.TrimSpace(memory.SourceTaskID); sourceTaskID != "" && sourceTaskID != taskID {
			return fmt.Errorf("vybe task memory %d scope_id %q conflicts with source_task_id %q", memory.ID, taskID, sourceTaskID)
		}
		isActive, ok := active[taskID]
		if !ok {
			report.ArchivedOnly["memory"]++
			report.ArchivedOrphans["memory"]++
			continue
		}
		if !isActive {
			report.ArchivedOnly["memory"]++
			continue
		}
		value, _ := json.Marshal(map[string]any{"value": json.RawMessage(nullJSON(memory.Value)), "value_type": memory.ValueType, "scope": memory.Scope, "scope_id": memory.ScopeID})
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_checkpoints(task_id,kind,checkpoint_key,value_json,source_system,source_id,source_event_id,created_at) VALUES (?, 'legacy_vybe_memory', ?, ?, 'vybe', ?, ?, ?)`, taskID, memory.Key, string(value), "memory:"+fmt.Sprint(memory.ID), memory.SourceEventID, memory.CreatedAt); err != nil {
			return fmt.Errorf("store: import memory %d: %w", memory.ID, err)
		}
		report.ImportedCheckpoints++
	}
	return nil
}

func importVybeArtifacts(ctx context.Context, tx *sql.Tx, artifacts []vybeArtifact, active map[string]bool, report *VybeImportReport) error {
	for _, artifact := range artifacts {
		isActive, ok := active[artifact.TaskID]
		if !ok {
			return fmt.Errorf("vybe artifact %q references missing task %q", artifact.ID, artifact.TaskID)
		}
		if !isActive {
			report.ArchivedOnly["artifacts"]++
			continue
		}
		meta, _ := json.Marshal(map[string]any{"vybe_project_id": artifact.ProjectID})
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_artifacts(id,task_id,path,content_type,metadata_json,source_system,source_id,source_event_id,created_at) VALUES (?,?,?,?,?,'vybe',?,?,?)`, artifact.ID, artifact.TaskID, artifact.FilePath, artifact.ContentType, string(meta), "artifact:"+artifact.ID, artifact.EventID, artifact.CreatedAt); err != nil {
			return fmt.Errorf("store: import artifact %q: %w", artifact.ID, err)
		}
		report.ImportedArtifacts++
	}
	return nil
}

func (s *SQLiteStore) insertImportedTask(ctx context.Context, tx *sql.Tx, mapped task.Task, source vybeTask) error {
	if err := s.insertTask(ctx, tx, mapped); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"original_status": source.Status, "priority": source.Priority, "project_id": source.ProjectID, "version": source.Version, "created_at": source.CreatedAt, "updated_at": source.UpdatedAt, "blocked_reason": source.BlockedReason})
	_, err := tx.ExecContext(ctx, `INSERT INTO task_checkpoints(task_id,kind,checkpoint_key,value_json,source_system,source_id,created_at) VALUES (?, 'import_metadata', 'vybe', ?, 'vybe', ?, ?)`, source.ID, string(metadata), "task:"+source.ID, source.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: import task metadata %s: %w", source.ID, err)
	}
	return nil
}

func mapVybeTask(source vybeTask) (task.Task, bool, bool, error) {
	if strings.TrimSpace(source.ID) == "" {
		return task.Task{}, false, false, fmt.Errorf("vybe task has an empty id")
	}
	if strings.TrimSpace(source.Title) == "" {
		return task.Task{}, false, false, fmt.Errorf("vybe task %q has an empty title", source.ID)
	}
	if source.CreatedAt == "" || source.UpdatedAt == "" {
		return task.Task{}, false, false, fmt.Errorf("vybe task %q has missing timestamps", source.ID)
	}
	status, active, gate, err := mapVybeStatus(source)
	if err != nil {
		return task.Task{}, false, false, err
	}
	priority := mapVybePriority(source.Priority)
	body := strings.TrimSpace(source.Title)
	if description := strings.TrimSpace(source.Description); description != "" {
		body += "\n\n" + description
	}
	finished := ""
	if !active {
		finished = source.UpdatedAt
	}
	return task.Task{ID: source.ID, Created: source.CreatedAt, Status: status, Body: body, Priority: priority, Source: "vybe", GroupID: source.ProjectID, Finished: finished}, active, gate, nil
}

func mapVybeStatus(source vybeTask) (task.Status, bool, bool, error) {
	switch source.Status {
	case "pending", vybeStatusTodo:
		return task.StatusTodo, true, false, nil
	case "completed", "done":
		return task.StatusDone, false, false, nil
	case "in_progress", vybeStatusDoing:
		return task.StatusTodo, true, false, nil
	case "blocked":
		if failureBlocked(source.BlockedReason) {
			return task.StatusFailed, false, false, nil
		}
		return task.StatusTodo, true, true, nil
	case "failed":
		return task.StatusFailed, false, false, nil
	default:
		return "", false, false, fmt.Errorf("vybe task %q has invalid status %q", source.ID, source.Status)
	}
}

func mapVybePriority(priorityValue int) task.Priority {
	priority := task.PriorityNormal
	switch {
	case priorityValue >= 100:
		priority = task.PriorityUrgent
	case priorityValue >= 10:
		priority = task.PriorityHigh
	case priorityValue <= -10:
		priority = task.PriorityLow
	}
	return priority
}

func failureBlocked(reason string) bool {
	r := strings.ToLower(strings.TrimSpace(reason))
	return strings.HasPrefix(r, "fail") || strings.HasPrefix(r, "error") || strings.Contains(r, "terminal failure")
}
