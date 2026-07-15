package store

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

const (
	vybeArchiveFormat  = "vybe-archive-v1"
	vybeTableTasks     = "tasks"
	vybeTableEvents    = "events"
	vybeTableMemory    = "memory"
	vybeTableArtifacts = "artifacts"
	vybeScopeTask      = "task"
	vybeStatusTodo     = "todo"
	vybeStatusDoing    = "doing"
)

// VybeImportOptions selects a frozen archive and dry-run/apply behavior.
type VybeImportOptions struct {
	Source string
	Apply  bool
}

// VybeImportReport reconciles active imports with archive-only rows.
type VybeImportReport struct {
	SourceSHA256        string           `json:"source_sha256"`
	CutoverID           string           `json:"cutover_id"`
	DryRun              bool             `json:"dry_run"`
	AlreadyImported     bool             `json:"already_imported"`
	SourceRows          map[string]int64 `json:"source_rows"`
	ImportedTasks       int              `json:"imported_tasks"`
	ImportedEvents      int              `json:"imported_events"`
	ImportedCheckpoints int              `json:"imported_checkpoints"`
	ImportedArtifacts   int              `json:"imported_artifacts"`
	ArchivedOnly        map[string]int64 `json:"archived_only"`
	ArchivedOrphans     map[string]int64 `json:"archived_orphans"`
}

type vybeManifest struct {
	FormatVersion        string           `json:"format_version"`
	SourceSHA256         string           `json:"source_sha256"`
	CutoverID            string           `json:"cutover_id"`
	IntegrityCheck       string           `json:"integrity_check"`
	RowCounts            map[string]int64 `json:"row_counts"`
	ReferentialIntegrity struct {
		OK bool `json:"ok"`
	} `json:"referential_integrity"`
}

type vybeTask struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Status        string `json:"status"`
	Version       int64  `json:"version"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	ProjectID     string `json:"project_id"`
	Priority      int    `json:"priority"`
	BlockedReason string `json:"blocked_reason"`
}

type vybeEvent struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	AgentName string          `json:"agent_name"`
	TaskID    string          `json:"task_id"`
	Message   string          `json:"message"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt string          `json:"created_at"`
}

type vybeMemory struct {
	ID            int64           `json:"id"`
	Key           string          `json:"key"`
	Value         json.RawMessage `json:"value"`
	ValueType     string          `json:"value_type"`
	Scope         string          `json:"scope"`
	ScopeID       string          `json:"scope_id"`
	SourceEventID *int64          `json:"source_event_id"`
	SourceTaskID  string          `json:"source_task_id"`
	CreatedAt     string          `json:"created_at"`
}

type vybeArtifact struct {
	ID          string `json:"id"`
	TaskID      string `json:"task_id"`
	EventID     int64  `json:"event_id"`
	FilePath    string `json:"file_path"`
	ContentType string `json:"content_type"`
	ProjectID   string `json:"project_id"`
	CreatedAt   string `json:"created_at"`
}

type vybeImportData struct {
	manifest  vybeManifest
	tasks     []vybeTask
	events    []vybeEvent
	memories  []vybeMemory
	artifacts []vybeArtifact
}

// ImportVybeArchive validates a frozen Vybe export before opening the write
// transaction. Apply writes every selected record and the import receipt in
// one transaction; dry-run always rolls the transaction back.
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

func readVybeArchive(ctx context.Context, root string) (vybeImportData, error) {
	if err := ctx.Err(); err != nil {
		return vybeImportData{}, err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return vybeImportData{}, fmt.Errorf("resolve vybe archive: %w", err)
	}
	var out vybeImportData
	if err := readJSONFile(ctx, filepath.Join(root, "manifest.json"), &out.manifest); err != nil {
		return out, err
	}
	if err := validateVybeManifest(out.manifest); err != nil {
		return out, err
	}
	if err := readVybeRows(ctx, root, &out); err != nil {
		return out, err
	}
	if err := validateVybeRowCounts(out); err != nil {
		return out, err
	}
	if err := validateAndNormalizeVybeData(ctx, &out); err != nil {
		return out, err
	}
	sort.Slice(out.events, func(i, j int) bool { return out.events[i].ID < out.events[j].ID })
	return out, nil
}

func validateVybeManifest(manifest vybeManifest) error {
	if manifest.FormatVersion != vybeArchiveFormat {
		return fmt.Errorf("unsupported vybe archive format %q", manifest.FormatVersion)
	}
	if manifest.IntegrityCheck != "ok" || !manifest.ReferentialIntegrity.OK {
		return fmt.Errorf("vybe archive integrity checks did not pass")
	}
	if err := requireCanonicalID("source_sha256", manifest.SourceSHA256, false); err != nil {
		return fmt.Errorf("vybe archive: %w", err)
	}
	if err := requireCanonicalID("cutover_id", manifest.CutoverID, false); err != nil {
		return fmt.Errorf("vybe archive: %w", err)
	}
	for _, name := range []string{"projects", vybeTableTasks, vybeTableEvents, vybeTableMemory, vybeTableArtifacts, "agent_state", "idempotency"} {
		count, ok := manifest.RowCounts[name]
		if !ok {
			return fmt.Errorf("vybe archive row_counts is missing %q", name)
		}
		if count < 0 {
			return fmt.Errorf("vybe archive row_counts %q is negative", name)
		}
	}
	return nil
}

func readVybeRows(ctx context.Context, root string, out *vybeImportData) error {
	readers := []func() error{
		func() error { return readJSONL(ctx, filepath.Join(root, "tasks.jsonl"), &out.tasks) },
		func() error { return readJSONL(ctx, filepath.Join(root, "events.jsonl"), &out.events) },
		func() error { return readJSONL(ctx, filepath.Join(root, "memories.jsonl"), &out.memories) },
		func() error { return readJSONL(ctx, filepath.Join(root, "artifacts.jsonl"), &out.artifacts) },
	}
	for _, read := range readers {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := read(); err != nil {
			return err
		}
	}
	return nil
}

func validateVybeRowCounts(data vybeImportData) error {
	checks := map[string]int{
		vybeTableTasks:     len(data.tasks),
		vybeTableEvents:    len(data.events),
		vybeTableMemory:    len(data.memories),
		vybeTableArtifacts: len(data.artifacts),
	}
	for name, count := range checks {
		if int64(count) != data.manifest.RowCounts[name] {
			return fmt.Errorf("vybe %s count mismatch: manifest=%d file=%d", name, data.manifest.RowCounts[name], count)
		}
	}
	return nil
}

func readJSONFile(ctx context.Context, path string, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer f.Close() //nolint:errcheck // read-only input
	decoder := json.NewDecoder(f)
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode %s: trailing JSON value", filepath.Base(path))
		}
		return fmt.Errorf("decode %s trailing data: %w", filepath.Base(path), err)
	}
	return nil
}

func readJSONL[T any](ctx context.Context, path string, out *[]T) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer f.Close() //nolint:errcheck // read-only input
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line++
		var row T
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return fmt.Errorf("decode %s line %d: %w", filepath.Base(path), line, err)
		}
		*out = append(*out, row)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return ctx.Err()
}

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
