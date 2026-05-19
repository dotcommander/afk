package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestWriteListTableAndJSONL(t *testing.T) {
	t.Parallel()
	tasks := []task.Task{{ID: "1", Created: "2025-01-02T03:04:05Z", Status: task.StatusPending, Body: "hello"}}

	var table bytes.Buffer
	require.NoError(t, output.WriteList(&table, tasks, false))
	require.Contains(t, table.String(), "ID")
	require.Contains(t, table.String(), "hello")

	var jsonl bytes.Buffer
	require.NoError(t, output.WriteList(&jsonl, tasks, true))
	var got task.Task
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(jsonl.String())), &got))
	require.Equal(t, "1", got.ID)
}

func TestWriteShowAndCount(t *testing.T) {
	t.Parallel()
	tk := task.Task{
		ID:          "1",
		Created:     "2025-01-02T03:04:05Z",
		Status:      task.StatusFailed,
		Body:        "body",
		Priority:    "high",
		Tags:        []string{"repo:afk"},
		CWD:         "/tmp/repo",
		Source:      "cli",
		Agent:       "codex",
		GroupID:     "group",
		ResourceKey: "repo:/tmp/repo",
		Finished:    "2025-01-02T03:05:05Z",
		Error:       "oops",
	}

	var show bytes.Buffer
	require.NoError(t, output.WriteShow(&show, tk, false))
	require.Contains(t, show.String(), "Status: failed")
	require.Contains(t, show.String(), "Priority: high")
	require.Contains(t, show.String(), "Tags: repo:afk")
	require.Contains(t, show.String(), "CWD: /tmp/repo")
	require.Contains(t, show.String(), "Source: cli")
	require.Contains(t, show.String(), "Agent: codex")
	require.Contains(t, show.String(), "Group: group")
	require.Contains(t, show.String(), "Resource: repo:/tmp/repo")
	require.Contains(t, show.String(), "Finished: 2025-01-02T03:05:05Z")
	require.Contains(t, show.String(), "Error: oops")

	var count bytes.Buffer
	require.NoError(t, output.WriteCount(&count, map[task.Status]int{task.StatusFailed: 1}))
	require.Contains(t, count.String(), "pending: 0")
	require.Contains(t, count.String(), "failed: 1")
}

func TestWriteEmptyListNoOutput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, output.WriteList(&out, nil, false))
	require.Empty(t, out.String())
}

func TestWriteDependencies(t *testing.T) {
	t.Parallel()

	deps := []task.Dependency{{TaskID: "blocked", DependsOnID: "prereq", Created: "2025-01-02T03:04:05Z"}}
	var table bytes.Buffer
	require.NoError(t, output.WriteDependencies(&table, deps, false))
	require.Contains(t, table.String(), "BLOCKED_BY")
	require.Contains(t, table.String(), "prereq")

	var jsonOut bytes.Buffer
	require.NoError(t, output.WriteDependencies(&jsonOut, deps, true))
	require.Contains(t, jsonOut.String(), `"depends_on_id":"prereq"`)
}

func TestWriteExplainTextAndJSON(t *testing.T) {
	t.Parallel()
	tk := task.Task{ID: "1", Created: "2025-01-02T03:04:05Z", Status: task.StatusFailed, Body: "body"}
	events := []task.Event{{ID: 1, TaskID: "1", Type: task.EventFailed, At: "2025-01-02T03:05:05Z", Message: "boom"}}
	attempts := []task.Attempt{{ID: 1, TaskID: "1", Started: "2025-01-02T03:04:05Z", Finished: "2025-01-02T03:05:05Z", Status: task.StatusFailed, Error: "boom"}}

	var text bytes.Buffer
	require.NoError(t, output.WriteExplain(&text, tk, events, attempts, false))
	require.Contains(t, text.String(), "Events:")
	require.Contains(t, text.String(), "failed  boom")
	require.Contains(t, text.String(), "Attempts:")

	var jsonOut bytes.Buffer
	require.NoError(t, output.WriteExplain(&jsonOut, tk, events, attempts, true))
	require.Contains(t, jsonOut.String(), `"task"`)
	require.Contains(t, jsonOut.String(), `"attempts"`)
}

func TestWriteShowJSONAndOptionalFields(t *testing.T) {
	t.Parallel()
	tk := task.Task{
		ID:      "1",
		Created: "2025-01-02T03:04:05Z",
		Status:  task.StatusWorking,
		Body:    "body",
		Started: "2025-01-02T03:04:06Z",
	}

	var jsonOut bytes.Buffer
	require.NoError(t, output.WriteShow(&jsonOut, tk, true))
	var got task.Task
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(jsonOut.String())), &got))
	require.Equal(t, task.StatusWorking, got.Status)

	var text bytes.Buffer
	require.NoError(t, output.WriteShow(&text, tk, false))
	require.Contains(t, text.String(), "Started: 2025-01-02T03:04:06Z")
	require.NotContains(t, text.String(), "Finished:")
}

func TestWriteListTruncatesLongUnicodeBody(t *testing.T) {
	t.Parallel()
	tasks := []task.Task{{
		ID:      "1",
		Created: "2025-01-02T03:04:05Z",
		Status:  task.StatusPending,
		Body:    strings.Repeat("界", 70),
	}}

	var out bytes.Buffer
	require.NoError(t, output.WriteList(&out, tasks, false))
	require.Contains(t, out.String(), "…")
}

func TestWriteCountJSON(t *testing.T) {
	t.Parallel()
	tally := map[task.Status]int{
		task.StatusPending: 2,
		task.StatusWorking: 1,
		task.StatusDone:    3,
		task.StatusFailed:  0,
	}

	var buf bytes.Buffer
	require.NoError(t, output.WriteCountJSON(&buf, tally))

	out := strings.TrimSpace(buf.String())
	require.True(t, strings.HasSuffix(buf.String(), "\n"), "output must be newline-terminated")

	var got map[string]int
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, 2, got["pending"])
	require.Equal(t, 1, got["working"])
	require.Equal(t, 3, got["done"])
	require.Equal(t, 0, got["failed"])
	require.Len(t, got, 4, "must emit exactly four canonical status keys")
}

func TestWriteCountJSONEmptyTally(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, output.WriteCountJSON(&buf, map[task.Status]int{}))

	var got map[string]int
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got))
	require.Equal(t, 0, got["pending"])
	require.Equal(t, 0, got["working"])
	require.Equal(t, 0, got["done"])
	require.Equal(t, 0, got["failed"])
	require.Len(t, got, 4)
}
