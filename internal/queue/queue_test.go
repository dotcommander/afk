package queue_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/afk/internal/queue"
	"github.com/stretchr/testify/require"
)

// newQ returns a Queue wired to <dir>/tasks.jsonl.
func newQ(t *testing.T, dir string) *queue.Queue {
	t.Helper()
	return queue.New(filepath.Join(dir, "tasks.jsonl"))
}

// sampleTasks returns N distinct tasks covering varied statuses and optional fields.
func sampleTasks() []queue.Task {
	return []queue.Task{
		{ID: "t1", Created: "2024-01-01T00:00:00Z", Status: queue.StatusPending, Body: "first task"},
		{ID: "t2", Created: "2024-01-02T00:00:00Z", Status: queue.StatusWorking, Body: "second task", Started: "2024-01-02T01:00:00Z"},
		{ID: "t3", Created: "2024-01-03T00:00:00Z", Status: queue.StatusFailed, Body: "third task", Error: "timeout"},
	}
}

// TestLoad_MissingFileReturnsEmpty verifies Load on a nonexistent path returns nil, nil.
func TestLoad_MissingFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := newQ(t, dir)

	tasks, err := q.Load(context.Background())
	require.NoError(t, err)
	require.Nil(t, tasks) // production code returns nil, nil on ErrNotExist
}

// TestLoad_EmptyFileReturnsEmpty verifies a zero-byte file returns no tasks.
func TestLoad_EmptyFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := newQ(t, dir)

	// Create an empty file.
	f, err := os.Create(filepath.Join(dir, "tasks.jsonl"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	tasks, err := q.Load(context.Background())
	require.NoError(t, err)
	require.Empty(t, tasks)
}

// TestSaveLoad_RoundTrip verifies that tasks survive a Save→Load cycle unchanged.
func TestSaveLoad_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := newQ(t, dir)
	ctx := context.Background()

	want := sampleTasks()
	require.NoError(t, q.Save(ctx, want))

	got, err := q.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestSave_AtomicTempFileCleanup verifies no temp files remain after Save.
// The impl uses pattern .tasks-*.jsonl.tmp in the queue directory.
func TestSave_AtomicTempFileCleanup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := newQ(t, dir)

	require.NoError(t, q.Save(context.Background(), sampleTasks()))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "expected exactly tasks.jsonl, got: %v", entries)
	require.Equal(t, "tasks.jsonl", entries[0].Name())
}

// TestLoad_SkipsBlankLines verifies blank lines between records are ignored.
func TestLoad_SkipsBlankLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := newQ(t, dir)

	raw := `{"id":"a","created":"2024-01-01T00:00:00Z","status":"pending","body":"one"}` + "\n\n" +
		`{"id":"b","created":"2024-01-02T00:00:00Z","status":"done","body":"two"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.jsonl"), []byte(raw), 0o644))

	tasks, err := q.Load(context.Background())
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, "a", tasks[0].ID)
	require.Equal(t, "b", tasks[1].ID)
}

// TestLoad_MalformedJSONReturnsError verifies Load surfaces a parse error.
func TestLoad_MalformedJSONReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := newQ(t, dir)

	raw := `{"id":"ok","created":"2024-01-01T00:00:00Z","status":"pending","body":"fine"}` + "\n" +
		"not-json\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.jsonl"), []byte(raw), 0o644))

	_, err := q.Load(context.Background())
	require.Error(t, err)
}

// TestSaveLoad_OmitsZeroOptionalFields verifies omitzero suppresses absent optional keys.
func TestSaveLoad_OmitsZeroOptionalFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := newQ(t, dir)

	tasks := []queue.Task{
		{ID: "x1", Created: "2024-01-01T00:00:00Z", Status: queue.StatusPending, Body: "no optionals"},
	}
	require.NoError(t, q.Save(context.Background(), tasks))

	raw, err := os.ReadFile(filepath.Join(dir, "tasks.jsonl"))
	require.NoError(t, err)
	line := string(raw)
	require.False(t, strings.Contains(line, `"started"`), "started should be omitted")
	require.False(t, strings.Contains(line, `"finished"`), "finished should be omitted")
	require.False(t, strings.Contains(line, `"error"`), "error should be omitted")
}

// TestDefaultPath verifies DefaultPath returns a non-empty platform-appropriate path.
func TestDefaultPath(t *testing.T) {
	t.Parallel()
	p, err := queue.DefaultPath()
	require.NoError(t, err)
	require.NotEmpty(t, p)
	require.True(t, strings.HasSuffix(p, ".claude/queue/tasks.jsonl"),
		"expected path to end with .claude/queue/tasks.jsonl, got: %s", p)
}

func TestPathReturnsConfiguredPath(t *testing.T) {
	t.Parallel()
	q := queue.New("/tmp/custom/tasks.jsonl")
	require.Equal(t, "/tmp/custom/tasks.jsonl", q.Path())
}

// TestSaveLoad_PreservesOrder verifies JSONL round-trip preserves insertion order.
func TestSaveLoad_PreservesOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := newQ(t, dir)
	ctx := context.Background()

	want := []queue.Task{
		{ID: "z", Created: "2024-01-05T00:00:00Z", Status: queue.StatusPending, Body: "fifth"},
		{ID: "a", Created: "2024-01-01T00:00:00Z", Status: queue.StatusDone, Body: "first"},
		{ID: "m", Created: "2024-01-03T00:00:00Z", Status: queue.StatusWorking, Body: "middle", Started: "2024-01-03T01:00:00Z"},
		{ID: "b", Created: "2024-01-02T00:00:00Z", Status: queue.StatusFailed, Body: "second", Error: "boom"},
		{ID: "q", Created: "2024-01-04T00:00:00Z", Status: queue.StatusPending, Body: "fourth"},
	}
	require.NoError(t, q.Save(ctx, want))

	got, err := q.Load(ctx)
	require.NoError(t, err)
	require.Len(t, got, len(want))
	for i := range want {
		require.Equal(t, want[i].ID, got[i].ID, "order mismatch at index %d", i)
	}
}
