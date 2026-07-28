package output_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestWriteListTableAndJSONL(t *testing.T) {
	t.Parallel()
	tasks := []task.Task{{ID: "1", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "hello"}}

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

func TestWriteTaskJSONLine(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, output.WriteTaskJSONLine(&out, task.Task{ID: "1", Status: task.StatusTodo, Body: "body"}, "claim"))
	require.Contains(t, out.String(), `"id":"1"`)
	require.Contains(t, out.String(), `"status":"todo"`)
}

func TestWriteExplainTaskDetailAndCount(t *testing.T) {
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
		Dependencies: []task.Dependency{{
			TaskID:      "1",
			DependsOnID: "0",
			Created:     "2025-01-02T03:03:05Z",
		}},
	}

	var explain bytes.Buffer
	require.NoError(t, output.WriteExplain(&explain, tk, nil, nil, false))
	require.Contains(t, explain.String(), "Status: failed")
	require.Contains(t, explain.String(), "Priority: high")
	require.Contains(t, explain.String(), "Tags: repo:afk")
	require.Contains(t, explain.String(), "CWD: /tmp/repo")
	require.Contains(t, explain.String(), "Source: cli")
	require.Contains(t, explain.String(), "Agent: codex")
	require.Contains(t, explain.String(), "Group: group")
	require.Contains(t, explain.String(), "Resource: repo:/tmp/repo")
	require.Contains(t, explain.String(), "Finished: 2025-01-02T03:05:05Z")
	require.Contains(t, explain.String(), "Error: oops")
	require.Contains(t, explain.String(), "Dependencies:")
	require.Contains(t, explain.String(), "  0")

	var count bytes.Buffer
	require.NoError(t, output.WriteCount(&count, map[task.Status]int{task.StatusFailed: 1}))
	require.Contains(t, count.String(), "todo: 0")
	require.Contains(t, count.String(), "failed: 1")
}

func TestWriteEmptyListNoOutput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, output.WriteList(&out, nil, false))
	require.Empty(t, out.String())
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

func TestWriteExplainJSONAndOptionalFields(t *testing.T) {
	t.Parallel()
	tk := task.Task{
		ID:      "1",
		Created: "2025-01-02T03:04:05Z",
		Status:  task.StatusDoing,
		Body:    "body",
		Started: "2025-01-02T03:04:06Z",
	}

	var jsonOut bytes.Buffer
	require.NoError(t, output.WriteExplain(&jsonOut, tk, nil, nil, true))
	var got struct {
		Task task.Task `json:"task"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(jsonOut.String())), &got))
	require.Equal(t, task.StatusDoing, got.Task.Status)

	var text bytes.Buffer
	require.NoError(t, output.WriteExplain(&text, tk, nil, nil, false))
	require.Contains(t, text.String(), "Started: 2025-01-02T03:04:06Z")
	require.NotContains(t, text.String(), "Finished:")
}

func TestWriteListTruncatesLongUnicodeBody(t *testing.T) {
	t.Parallel()
	tasks := []task.Task{{
		ID:      "1",
		Created: "2025-01-02T03:04:05Z",
		Status:  task.StatusTodo,
		Body:    strings.Repeat("界", 70),
	}}

	var out bytes.Buffer
	require.NoError(t, output.WriteList(&out, tasks, false))
	require.Contains(t, out.String(), "…")
}

func TestWriteCountJSON(t *testing.T) {
	t.Parallel()
	tally := map[task.Status]int{
		task.StatusTodo:   2,
		task.StatusDoing:  1,
		task.StatusDone:   3,
		task.StatusFailed: 0,
	}

	var buf bytes.Buffer
	require.NoError(t, output.WriteCountJSON(&buf, tally))

	out := strings.TrimSpace(buf.String())
	require.True(t, strings.HasSuffix(buf.String(), "\n"), "output must be newline-terminated")

	var got map[string]int
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, 2, got["todo"])
	require.Equal(t, 1, got["doing"])
	require.Equal(t, 3, got["done"])
	require.Equal(t, 0, got["failed"])
	require.Equal(t, 0, got["deleted"])
	require.Len(t, got, 5, "must emit exactly five canonical status keys")
}

func TestWriteCountJSONEmptyTally(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, output.WriteCountJSON(&buf, map[task.Status]int{}))

	var got map[string]int
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got))
	require.Equal(t, 0, got["todo"])
	require.Equal(t, 0, got["doing"])
	require.Equal(t, 0, got["done"])
	require.Equal(t, 0, got["failed"])
	require.Equal(t, 0, got["deleted"])
	require.Len(t, got, 5)
}

func TestWriteStatusTextAndJSONUseTodoDoing(t *testing.T) {
	t.Parallel()
	tally := map[task.Status]int{
		task.StatusTodo:  1,
		task.StatusDoing: 1,
		task.StatusDone:  2,
	}
	todo := []task.Task{{
		ID:      "todo-1",
		Created: "2025-01-02T03:04:05Z",
		Status:  task.StatusTodo,
		Body:    "todo body",
	}}
	doing := []task.Task{{
		ID:      "doing-1",
		Created: "2025-01-02T03:04:06Z",
		Status:  task.StatusDoing,
		Body:    "doing body",
		Started: "2025-01-02T03:04:06Z",
	}}
	now := time.Date(2025, 1, 2, 3, 5, 6, 0, time.UTC)
	avg, p50, p90 := 125.0, 60.0, 180.0

	var text bytes.Buffer
	health := task.QueueHealth{
		WindowSeconds: int64((24 * time.Hour) / time.Second),
		TerminalAttemptDurationSeconds: task.DurationDistribution{
			Count: 3,
			Avg:   &avg,
			P50:   &p50,
			P90:   &p90,
		},
	}
	require.NoError(t, output.WriteStatus(&text, tally, todo, doing, nil, health, false, now))
	require.Contains(t, text.String(), "todo: 1")
	require.Contains(t, text.String(), "doing: 1")
	require.Contains(t, text.String(), "Todo:")
	require.Contains(t, text.String(), "Doing:")
	require.Contains(t, text.String(), "todo-1")
	require.Contains(t, text.String(), "doing-1")
	require.Contains(t, text.String(), "age=1m0s")
	require.Contains(t, text.String(), "Health (24h0m0s):")
	require.Contains(t, text.String(), "terminal attempt duration p50: 1m0s")
	require.Contains(t, text.String(), "terminal attempt duration p90: 3m0s")

	var jsonOut bytes.Buffer
	require.NoError(t, output.WriteStatus(&jsonOut, tally, todo, doing, nil, health, true, now))
	var got struct {
		Todo   int              `json:"todo"`
		Doing  int              `json:"doing"`
		Done   int              `json:"done"`
		Health task.QueueHealth `json:"health"`
		Tasks  struct {
			Todo  []task.Task `json:"todo"`
			Doing []struct {
				task.Task
				Claim *task.ClaimDiagnostics `json:"claim"`
			} `json:"doing"`
		} `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(jsonOut.String())), &got))
	require.Equal(t, 1, got.Todo)
	require.Equal(t, 1, got.Doing)
	require.Equal(t, 2, got.Done)
	require.Equal(t, int64(86400), got.Health.WindowSeconds)
	require.Equal(t, 3, got.Health.TerminalAttemptDurationSeconds.Count)
	require.Equal(t, 125.0, *got.Health.TerminalAttemptDurationSeconds.Avg)
	require.Equal(t, 60.0, *got.Health.TerminalAttemptDurationSeconds.P50)
	require.Equal(t, 180.0, *got.Health.TerminalAttemptDurationSeconds.P90)
	require.Len(t, got.Tasks.Todo, 1)
	require.Equal(t, "todo-1", got.Tasks.Todo[0].ID)
	require.Len(t, got.Tasks.Doing, 1)
	require.Equal(t, "doing-1", got.Tasks.Doing[0].ID)
	require.Equal(t, int64(60), got.Tasks.Doing[0].Claim.AgeSeconds)
	require.NotContains(t, jsonOut.String(), `"pending"`)
	require.NotContains(t, jsonOut.String(), `"working"`)
}

func TestWriteListJSONBoundsRowsAndBody(t *testing.T) {
	t.Parallel()

	tasks := make([]task.Task, 101)
	for i := range tasks {
		tasks[i] = task.Task{
			ID:      string(rune('a' + i%26)),
			Created: "2025-01-02T03:04:05Z",
			Status:  task.StatusTodo,
			Body:    strings.Repeat("界", 600),
		}
	}

	var buf bytes.Buffer
	require.NoError(t, output.WriteList(&buf, tasks, true))
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 101)

	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	require.Equal(t, true, first["body_truncated"])
	require.NotContains(t, first, "body_hint")
	require.Less(t, len([]rune(first["body"].(string))), 600)

	var summary map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[100]), &summary))
	require.Equal(t, float64(1), summary["omitted"])
}

func TestWriteExplainBoundsHistoryAndMessages(t *testing.T) {
	t.Parallel()

	events := make([]task.Event, 51)
	attempts := make([]task.Attempt, 51)
	for i := range events {
		events[i] = task.Event{ID: int64(i + 1), TaskID: "1", Type: task.EventFailed, At: "2025-01-02T03:04:05Z", Message: strings.Repeat("e", 1100)}
		attempts[i] = task.Attempt{ID: int64(i + 1), TaskID: "1", Started: "2025-01-02T03:04:05Z", Status: task.StatusFailed, Error: strings.Repeat("a", 1100)}
	}

	var buf bytes.Buffer
	require.NoError(t, output.WriteExplain(&buf, task.Task{ID: "1", Body: strings.Repeat("b", 9000)}, events, attempts, true))

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got))
	require.Equal(t, float64(1), got["events_omitted"])
	require.Equal(t, float64(1), got["attempts_omitted"])
	require.Len(t, got["events"], 50)
	require.Len(t, got["attempts"], 50)
	taskDoc := got["task"].(map[string]any)
	require.Equal(t, true, taskDoc["body_truncated"])
}

func TestWriteOutputPropagatesWriterErrors(t *testing.T) {
	t.Parallel()

	w := errWriter{}
	tk := task.Task{
		ID:      "1",
		Created: "2025-01-02T03:04:05Z",
		Status:  task.StatusFailed,
		Body:    "body",
		Error:   "boom",
	}
	event := task.Event{ID: 1, TaskID: "1", Type: task.EventFailed, At: "2025-01-02T03:05:05Z", Message: "boom"}
	attempt := task.Attempt{ID: 1, TaskID: "1", Started: "2025-01-02T03:04:05Z", Status: task.StatusFailed, Error: "boom"}

	checks := []struct {
		name string
		err  error
	}{
		{name: "list table", err: output.WriteList(w, []task.Task{tk}, false)},
		{name: "list json", err: output.WriteList(w, []task.Task{tk}, true)},
		{name: "count text", err: output.WriteCount(w, map[task.Status]int{})},
		{name: "status text", err: output.WriteStatus(w, map[task.Status]int{}, nil, nil, nil, task.QueueHealth{}, false, time.Now())},
		{name: "status json", err: output.WriteStatus(w, map[task.Status]int{}, nil, nil, nil, task.QueueHealth{}, true, time.Now())},
		{name: "task json", err: output.WriteTaskJSONLine(w, tk, "task")},
		{name: "explain text", err: output.WriteExplain(w, tk, []task.Event{event}, []task.Attempt{attempt}, false)},
		{name: "explain json", err: output.WriteExplain(w, tk, []task.Event{event}, []task.Attempt{attempt}, true)},
	}
	for _, check := range checks {
		require.Error(t, check.err, check.name)
		require.True(t, errors.Is(check.err, errWrite), "%s: %v", check.name, check.err)
	}
}

func TestWriteExplainTextEmptySections(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, output.WriteExplain(&out, task.Task{ID: "1", Body: "body"}, nil, nil, false))
	require.Contains(t, out.String(), "Events:\n  none")
	require.Contains(t, out.String(), "Attempts:\n  none")
}

func TestWriteExplainTextOmittedHistory(t *testing.T) {
	t.Parallel()

	events := make([]task.Event, 51)
	attempts := make([]task.Attempt, 51)
	for i := range events {
		events[i] = task.Event{ID: int64(i + 1), TaskID: "1", Type: task.EventFailed, At: "2025-01-02T03:04:05Z", Message: "boom"}
		attempts[i] = task.Attempt{ID: int64(i + 1), TaskID: "1", Started: "2025-01-02T03:04:05Z", Finished: "2025-01-02T03:05:05Z", Status: task.StatusFailed, Error: "boom", WorkerID: "worker", Agent: "codex"}
	}

	var out bytes.Buffer
	require.NoError(t, output.WriteExplain(&out, task.Task{ID: "1", Body: "body"}, events, attempts, false))
	require.Contains(t, out.String(), "older events omitted")
	require.Contains(t, out.String(), "older attempts omitted")
	require.Contains(t, out.String(), "worker=worker")
	require.Contains(t, out.String(), "agent=codex")
}

var errWrite = errors.New("write failed")

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) {
	return 0, errWrite
}
