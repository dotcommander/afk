package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestRequestedAddReplayAndCollision(t *testing.T) {
	t.Parallel()
	s := newRetirementStore(t)
	ctx := context.Background()
	tk := task.Task{ID: "vybe-id", Created: "2026-07-11T00:00:00Z", Status: task.StatusTodo, Body: "import me"}
	id, replayed, err := s.AddRequested(ctx, "mytree", "req-1", "task.add:one", tk, "")
	require.NoError(t, err)
	require.Equal(t, "vybe-id", id)
	require.False(t, replayed)
	id, replayed, err = s.AddRequested(ctx, "mytree", "req-1", "task.add:one", task.Task{ID: "different", Status: task.StatusTodo, Body: "ignored"}, "")
	require.NoError(t, err)
	require.Equal(t, "vybe-id", id)
	require.True(t, replayed)
	_, _, err = s.UpdateRequested(ctx, "mytree", "req-1", "task.set:other", "vybe-id", task.EventDone, "done", func(tk *task.Task) bool { return tk.MarkDone(time.Now()) })
	require.ErrorIs(t, err, store.ErrRequestCollision)
}

func TestRequestedKeysAreCanonical(t *testing.T) {
	t.Parallel()
	s := newRetirementStore(t)
	ctx := context.Background()
	tk := task.Task{ID: "canonical", Created: "2026-07-11T00:00:00Z", Status: task.StatusTodo, Body: "task"}
	_, replayed, err := s.AddRequested(ctx, " actor ", " request ", "task.add:one", tk, "")
	require.NoError(t, err)
	require.False(t, replayed)
	id, replayed, err := s.AddRequested(ctx, "actor", "request", "task.add:one", task.Task{ID: "ignored", Status: task.StatusTodo, Body: "ignored"}, "")
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, "canonical", id)
	_, _, err = s.AddRequested(ctx, " ", "request", "task.add:two", task.Task{}, "")
	require.Error(t, err)
}

func TestRequestedUpdateReplaysOriginalSnapshot(t *testing.T) {
	t.Parallel()
	s := newRetirementStore(t)
	ctx := context.Background()
	require.NoError(t, s.Add(ctx, task.Task{ID: "t", Created: "2026-07-11T00:00:00Z", Status: task.StatusTodo, Body: "task"}))
	original, replayed, err := s.UpdateRequested(ctx, "cli", "req", "task.set:stable", "t", task.EventDone, "first", func(tk *task.Task) bool { return tk.MarkDone(time.Unix(10, 0)) })
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, task.StatusDone, original.Status)
	require.NoError(t, s.Update(ctx, "t", task.EventRequeued, "later", func(tk *task.Task) bool { tk.Reset(); return true }))
	replayedTask, replayed, err := s.UpdateRequested(ctx, "cli", "req", "task.set:stable", "t", task.EventDone, "first", func(tk *task.Task) bool { return tk.MarkDone(time.Unix(20, 0)) })
	require.NoError(t, err)
	require.True(t, replayed)
	originalJSON, err := json.Marshal(original)
	require.NoError(t, err)
	replayedJSON, err := json.Marshal(replayedTask)
	require.NoError(t, err)
	require.JSONEq(t, string(originalJSON), string(replayedJSON))
}

func TestTaskRecordsAndCAS(t *testing.T) {
	t.Parallel()
	s := newRetirementStore(t)
	ctx := context.Background()
	require.NoError(t, s.Add(ctx, task.Task{ID: "t", Created: "2026-07-11T00:00:00Z", Status: task.StatusTodo, Body: "task"}))
	eventID := int64(42)
	checkpoint, err := s.AddCheckpoint(ctx, task.Checkpoint{TaskID: "t", Kind: "progress", ValueJSON: `{"step":1}`, Provenance: task.Provenance{System: "test", ID: "cp", EventID: &eventID}, CreatedAt: "2026-07-11T00:01:00Z"})
	require.NoError(t, err)
	require.NotZero(t, checkpoint.ID)
	require.NoError(t, s.AddArtifact(ctx, task.Artifact{ID: "a", TaskID: "t", Path: "report.md", MetadataJSON: "{}", Provenance: task.Provenance{System: "test", ID: "artifact"}, CreatedAt: "2026-07-11T00:02:00Z"}))
	checkpoints, err := s.Checkpoints(ctx, "t")
	require.NoError(t, err)
	require.Len(t, checkpoints, 1)
	require.Equal(t, eventID, *checkpoints[0].Provenance.EventID)
	artifacts, err := s.Artifacts(ctx, "t")
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	require.NoError(t, s.Add(ctx, task.Task{ID: "empty", Created: "2026-07-11T00:00:00Z", Status: task.StatusTodo, Body: "empty"}))
	empty, err := s.Checkpoints(ctx, "empty")
	require.NoError(t, err)
	require.NotNil(t, empty)
	require.Empty(t, empty)
	_, err = s.Checkpoints(ctx, "missing")
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.Artifacts(ctx, "missing")
	require.ErrorIs(t, err, store.ErrNotFound)
	revision, err := s.UpdateCAS(ctx, "t", 1, task.EventClaimed, "", func(tk *task.Task) bool { tk.MarkWorking(time.Now()); return true })
	require.NoError(t, err)
	require.Equal(t, int64(2), revision)
	_, err = s.UpdateCAS(ctx, "t", 1, task.EventDone, "", func(*task.Task) bool { return true })
	var conflict *store.ConflictError
	require.True(t, errors.As(err, &conflict))
	require.Equal(t, int64(2), conflict.Current)
	revision, err = s.UpdateCAS(ctx, "t", 2, task.EventDone, "finished", func(tk *task.Task) bool { return tk.MarkDone(time.Now()) })
	require.NoError(t, err)
	require.Equal(t, int64(3), revision)
	attempts, err := s.Attempts(ctx, "t")
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, task.StatusDone, attempts[0].Status)
	require.NotEmpty(t, attempts[0].Finished)
}

func TestExplicitClaimSatisfiesGateAtomicallyAndReplaysForWorker(t *testing.T) {
	t.Parallel()
	s := newRetirementStore(t)
	ctx := context.Background()
	require.NoError(t, s.Add(ctx, task.Task{ID: "t", Created: "2026-07-11T00:00:00Z", Status: task.StatusTodo, Body: "task"}))
	require.NoError(t, s.AddGate(ctx, "t", "legacy-vybe-blocked"))
	claimed, err := s.ClaimTaskForWorker(ctx, "t", time.Now(), time.Time{}, "mytree:t", "mytree", []string{"legacy-vybe-blocked"})
	require.NoError(t, err)
	require.Equal(t, task.StatusDoing, claimed.Status)
	gates, err := s.Gates(ctx, "t")
	require.NoError(t, err)
	require.Len(t, gates, 1)
	firstSatisfiedAt := *gates[0].SatisfiedAt
	replayed, err := s.ClaimTaskForWorker(ctx, "t", time.Now(), time.Time{}, "mytree:t", "mytree", []string{"legacy-vybe-blocked"})
	require.NoError(t, err)
	require.Equal(t, claimed.ID, replayed.ID)
	attempts, err := s.Attempts(ctx, "t")
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	gates, err = s.Gates(ctx, "t")
	require.NoError(t, err)
	require.Equal(t, firstSatisfiedAt, *gates[0].SatisfiedAt)
}

func TestImportVybeArchiveDryRunApplyReplay(t *testing.T) {
	t.Parallel()
	archive := writeVybeFixture(t, false)
	s := newRetirementStore(t)
	ctx := context.Background()
	report, err := s.ImportVybeArchive(ctx, store.VybeImportOptions{Source: archive})
	require.NoError(t, err)
	require.True(t, report.DryRun)
	require.Equal(t, 3, report.ImportedTasks)
	require.Equal(t, 1, report.ImportedEvents)
	require.Equal(t, 1, report.ImportedCheckpoints)
	require.Equal(t, 1, report.ImportedArtifacts)
	require.Equal(t, int64(1), report.ArchivedOrphans["events"])
	_, err = s.Get(ctx, "pending")
	require.ErrorIs(t, err, store.ErrNotFound)
	report, err = s.ImportVybeArchive(ctx, store.VybeImportOptions{Source: archive, Apply: true})
	require.NoError(t, err)
	require.False(t, report.DryRun)
	pending, err := s.Get(ctx, "pending")
	require.NoError(t, err)
	require.Equal(t, task.PriorityUrgent, pending.Priority)
	blocked, err := s.Get(ctx, "blocked")
	require.NoError(t, err)
	require.Equal(t, task.StatusTodo, blocked.Status)
	gates, err := s.Gates(ctx, "blocked")
	require.NoError(t, err)
	require.Len(t, gates, 1)
	require.Equal(t, "legacy-vybe-blocked", gates[0].Name)
	done, err := s.Get(ctx, "done")
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, done.Status)
	replayed, err := s.ImportVybeArchive(ctx, store.VybeImportOptions{Source: archive, Apply: true})
	require.NoError(t, err)
	require.True(t, replayed.AlreadyImported)
}

func TestImportVybeArchiveRejectsOrphanAndRollsBack(t *testing.T) {
	t.Parallel()
	archive := writeVybeFixture(t, true)
	s := newRetirementStore(t)
	_, err := s.ImportVybeArchive(context.Background(), store.VybeImportOptions{Source: archive, Apply: true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing task")
	_, err = s.Get(context.Background(), "pending")
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestImportVybeArchiveRejectsTaskMemoryOwnerMismatch(t *testing.T) {
	t.Parallel()
	archive := writeVybeFixture(t, false)
	writeLines(t, filepath.Join(archive, "memories.jsonl"), []map[string]any{
		{"id": 4, "key": "checkpoint", "value": "halfway", "value_type": "string", "scope": "task", "scope_id": "pending", "source_task_id": "done", "created_at": "2026-07-11T00:00:00Z"},
		{"id": 5, "key": "global", "value": "archive", "value_type": "string", "scope": "global", "source_task_id": "pending", "created_at": "2026-07-11T00:00:00Z"},
	})
	s := newRetirementStore(t)
	_, err := s.ImportVybeArchive(context.Background(), store.VybeImportOptions{Source: archive, Apply: true})
	require.ErrorContains(t, err, "conflicts with source_task_id")
	_, err = s.Get(context.Background(), "pending")
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestImportVybeArchiveNormalizesTimestampsAndNamespacesProvenance(t *testing.T) {
	t.Parallel()
	archive := writeVybeFixture(t, false)
	for _, name := range []string{"tasks.jsonl", "events.jsonl", "memories.jsonl", "artifacts.jsonl"} {
		rows := readLines(t, filepath.Join(archive, name))
		for _, row := range rows {
			if _, ok := row["created_at"]; ok {
				row["created_at"] = "2026-07-11T02:00:00+02:00"
			}
			if _, ok := row["updated_at"]; ok {
				row["updated_at"] = "2026-07-11T02:00:00+02:00"
			}
		}
		writeLines(t, filepath.Join(archive, name), rows)
	}

	s := newRetirementStore(t)
	ctx := context.Background()
	_, err := s.ImportVybeArchive(ctx, store.VybeImportOptions{Source: archive, Apply: true})
	require.NoError(t, err)
	pending, err := s.Get(ctx, "pending")
	require.NoError(t, err)
	require.Equal(t, "2026-07-11T00:00:00Z", pending.Created)
	checkpoints, err := s.Checkpoints(ctx, "pending")
	require.NoError(t, err)
	require.Len(t, checkpoints, 2)
	require.Equal(t, "task:pending", checkpoints[0].Provenance.ID)
	require.Equal(t, "memory:4", checkpoints[1].Provenance.ID)
	require.Equal(t, "2026-07-11T00:00:00Z", checkpoints[1].CreatedAt)
	artifacts, err := s.Artifacts(ctx, "pending")
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	require.Equal(t, "artifact:art", artifacts[0].Provenance.ID)
	require.Equal(t, "2026-07-11T00:00:00Z", artifacts[0].CreatedAt)
	events, err := s.Events(ctx, "pending")
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "2026-07-11T00:00:00Z", events[0].At)
}

func TestImportVybeArchiveRejectsEveryTimestampClassBeforeWriting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file string
		row  int
		key  string
	}{
		{name: "task created", file: "tasks.jsonl", key: "created_at"},
		{name: "task updated", file: "tasks.jsonl", key: "updated_at"},
		{name: "event", file: "events.jsonl", key: "created_at"},
		{name: "memory", file: "memories.jsonl", key: "created_at"},
		{name: "artifact", file: "artifacts.jsonl", key: "created_at"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := writeVybeFixture(t, false)
			rows := readLines(t, filepath.Join(archive, tt.file))
			rows[tt.row][tt.key] = "not-a-time"
			writeLines(t, filepath.Join(archive, tt.file), rows)
			s := newRetirementStore(t)
			_, err := s.ImportVybeArchive(context.Background(), store.VybeImportOptions{Source: archive, Apply: true})
			require.ErrorContains(t, err, "not RFC3339")
			_, err = s.Get(context.Background(), "pending")
			require.ErrorIs(t, err, store.ErrNotFound)
		})
	}
}

func TestImportVybeArchiveRejectsInvalidManifestBeforeWriting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(t *testing.T, archive string)
		want   string
	}{
		{name: "trailing JSON", mutate: func(t *testing.T, archive string) {
			path := filepath.Join(archive, "manifest.json")
			contents, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, append(contents, []byte("\n{}")...), 0o600))
		}, want: "trailing JSON value"},
		{name: "empty cutover", mutate: func(t *testing.T, archive string) {
			updateManifest(t, archive, func(manifest map[string]any) { manifest["cutover_id"] = " " })
		}, want: "cutover_id"},
		{name: "missing row count", mutate: func(t *testing.T, archive string) {
			updateManifest(t, archive, func(manifest map[string]any) {
				delete(manifest["row_counts"].(map[string]any), "agent_state")
			})
		}, want: `row_counts is missing "agent_state"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := writeVybeFixture(t, false)
			tt.mutate(t, archive)
			s := newRetirementStore(t)
			_, err := s.ImportVybeArchive(context.Background(), store.VybeImportOptions{Source: archive, Apply: true})
			require.ErrorContains(t, err, tt.want)
			_, err = s.Get(context.Background(), "pending")
			require.ErrorIs(t, err, store.ErrNotFound)
		})
	}
}

func TestImportVybeArchiveRejectsNonCanonicalReferencesAndCancellation(t *testing.T) {
	t.Parallel()
	archive := writeVybeFixture(t, false)
	artifacts := readLines(t, filepath.Join(archive, "artifacts.jsonl"))
	artifacts[0]["task_id"] = " pending "
	writeLines(t, filepath.Join(archive, "artifacts.jsonl"), artifacts)
	s := newRetirementStore(t)
	_, err := s.ImportVybeArchive(context.Background(), store.VybeImportOptions{Source: archive, Apply: true})
	require.ErrorContains(t, err, "non-canonical")

	archive = writeVybeFixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.ImportVybeArchive(ctx, store.VybeImportOptions{Source: archive, Apply: true})
	require.ErrorIs(t, err, context.Canceled)
	_, err = s.Get(context.Background(), "pending")
	require.ErrorIs(t, err, store.ErrNotFound)
	report, err := s.ImportVybeArchive(context.Background(), store.VybeImportOptions{Source: archive, Apply: true})
	require.NoError(t, err)
	require.False(t, report.AlreadyImported)
}

func writeVybeFixture(t *testing.T, orphan bool) string {
	t.Helper()
	dir := t.TempDir()
	tasks := []map[string]any{{"id": "pending", "title": "Pending", "description": "work", "status": "pending", "version": 2, "created_at": "2026-07-10T00:00:00Z", "updated_at": "2026-07-11T00:00:00Z", "project_id": "p", "priority": 100}, {"id": "blocked", "title": "Blocked", "status": "blocked", "blocked_reason": "awaiting input", "version": 1, "created_at": "2026-07-10T00:00:00Z", "updated_at": "2026-07-11T00:00:00Z", "priority": 0}, {"id": "done", "title": "Done", "status": "completed", "version": 1, "created_at": "2026-07-09T00:00:00Z", "updated_at": "2026-07-10T00:00:00Z", "priority": -10}}
	events := []map[string]any{{"id": 7, "kind": "note", "agent_name": "agent", "task_id": "pending", "message": "progress", "metadata": "{}", "created_at": "2026-07-11T00:00:00Z"}, {"id": 8, "kind": "done", "agent_name": "agent", "task_id": "done", "message": "done", "created_at": "2026-07-11T00:00:00Z"}, {"id": 9, "kind": "historical", "agent_name": "agent", "task_id": "missing", "message": "archived", "created_at": "2026-07-11T00:00:00Z"}}
	memories := []map[string]any{{"id": 4, "key": "checkpoint", "value": "halfway", "value_type": "string", "scope": "task", "scope_id": "pending", "source_event_id": 7, "created_at": "2026-07-11T00:00:00Z"}, {"id": 5, "key": "global", "value": "archive", "value_type": "string", "scope": "global", "source_task_id": "pending", "created_at": "2026-07-11T00:00:00Z"}}
	artifactTask := "pending"
	if orphan {
		artifactTask = "missing"
	}
	artifacts := []map[string]any{{"id": "art", "task_id": artifactTask, "event_id": 7, "file_path": "report.md", "content_type": "text/markdown", "project_id": "p", "created_at": "2026-07-11T00:00:00Z"}}
	writeLines(t, filepath.Join(dir, "tasks.jsonl"), tasks)
	writeLines(t, filepath.Join(dir, "events.jsonl"), events)
	writeLines(t, filepath.Join(dir, "memories.jsonl"), memories)
	writeLines(t, filepath.Join(dir, "artifacts.jsonl"), artifacts)
	manifest := map[string]any{"format_version": "vybe-archive-v1", "source_sha256": "abc123", "cutover_id": "cutover", "integrity_check": "ok", "referential_integrity": map[string]any{"ok": true}, "row_counts": map[string]int{"projects": 1, "tasks": len(tasks), "events": len(events), "memory": len(memories), "artifacts": len(artifacts), "agent_state": 1, "idempotency": 10}}
	b, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600))
	return dir
}

func writeLines(t *testing.T, path string, rows []map[string]any) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	enc := json.NewEncoder(f)
	for _, row := range rows {
		require.NoError(t, enc.Encode(row))
	}
	require.NoError(t, f.Close())
}

func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	dec := json.NewDecoder(f)
	var rows []map[string]any
	for {
		var row map[string]any
		err := dec.Decode(&row)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		rows = append(rows, row)
	}
	require.NoError(t, f.Close())
	return rows
}

func updateManifest(t *testing.T, archive string, mutate func(map[string]any)) {
	t.Helper()
	path := filepath.Join(archive, "manifest.json")
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(contents, &manifest))
	mutate(manifest)
	contents, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, contents, 0o600))
}

func newRetirementStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	s, err := store.NewSQLite(context.Background(), store.Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}
