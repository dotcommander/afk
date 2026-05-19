// Package output renders tasks for the CLI.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/dotcommander/afk/internal/task"
)

const (
	maxListTasks       = 100
	maxListBodyRunes   = 500
	maxDetailBodyRunes = 8000
	maxHistoryItems    = 50
	maxMessageRunes    = 1000
)

type boundedTask struct {
	task.Task
	BodyTruncated  bool `json:"body_truncated,omitzero"`
	ErrorTruncated bool `json:"error_truncated,omitzero"`
}

type listSummary struct {
	Omitted int    `json:"omitted"`
	Limit   int    `json:"limit"`
	Reason  string `json:"reason"`
}

type explainDoc struct {
	Task            boundedTask    `json:"task"`
	Events          []task.Event   `json:"events"`
	Attempts        []task.Attempt `json:"attempts"`
	EventsOmitted   int            `json:"events_omitted,omitzero"`
	AttemptsOmitted int            `json:"attempts_omitted,omitzero"`
}

// WriteList renders a bounded task list as either JSONL or an aligned table.
func WriteList(w io.Writer, tasks []task.Task, asJSON bool) error {
	if len(tasks) == 0 {
		return nil
	}
	visible, omitted := limitTasks(tasks)
	if asJSON {
		return writeListJSON(w, visible, omitted)
	}
	return writeListTable(w, visible, omitted)
}

func writeListJSON(w io.Writer, tasks []task.Task, omitted int) error {
	for _, t := range tasks {
		if err := WriteBoundTaskJSONLine(w, t, maxListBodyRunes, "list"); err != nil {
			return err
		}
	}
	if omitted == 0 {
		return nil
	}
	return WriteJSONLine(w, listSummary{
		Omitted: omitted,
		Limit:   maxListTasks,
		Reason:  "output limit",
	}, "list")
}

func writeListTable(w io.Writer, tasks []task.Task, omitted int) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tCREATED\tBODY") //nolint:errcheck // tabwriter buffers; errors surface at Flush
	for _, t := range tasks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.ID, t.Status, t.Created, truncate(t.Body, 60)) //nolint:errcheck // tabwriter buffers; errors surface at Flush
	}
	if omitted > 0 {
		fmt.Fprintf(tw, "...\t...\t...\t%d more tasks omitted by output limit\n", omitted) //nolint:errcheck // tabwriter buffers; errors surface at Flush
	}
	return tw.Flush()
}

// WriteShow renders a single task.
func WriteShow(w io.Writer, t task.Task, asJSON bool) error {
	if asJSON {
		return WriteTaskJSONLine(w, t, "show")
	}
	if _, err := fmt.Fprintf(w, "ID: %s\nStatus: %s\nCreated: %s\nBody: %s\n", t.ID, t.Status, t.Created, truncate(t.Body, maxDetailBodyRunes)); err != nil {
		return fmt.Errorf("show: write: %w", err)
	}
	for _, field := range showFields(t) {
		if field.value == "" {
			continue
		}
		if _, err := fmt.Fprintf(w, "%s: %s\n", field.name, field.value); err != nil {
			return fmt.Errorf("show: write: %w", err)
		}
	}
	return nil
}

type showField struct {
	name  string
	value string
}

func showFields(t task.Task) []showField {
	return []showField{
		{name: "Priority", value: t.Priority},
		{name: "Tags", value: strings.Join(t.Tags, ", ")},
		{name: "CWD", value: t.CWD},
		{name: "Source", value: t.Source},
		{name: "Agent", value: t.Agent},
		{name: "Group", value: t.GroupID},
		{name: "Resource", value: t.ResourceKey},
		{name: "Started", value: t.Started},
		{name: "Finished", value: t.Finished},
		{name: "Error", value: truncate(t.Error, maxMessageRunes)},
	}
}

// WriteCount renders per-status tallies in canonical order.
func WriteCount(w io.Writer, tally map[task.Status]int) error {
	for _, s := range task.OrderedStatuses() {
		if _, err := fmt.Fprintf(w, "%s: %d\n", s, tally[s]); err != nil {
			return fmt.Errorf("count: write: %w", err)
		}
	}
	return nil
}

// WriteCountJSON renders per-status tallies as a single JSON object on one line.
// All four canonical status keys are always present (zero if missing) so consumers
// can rely on a fixed shape.
func WriteCountJSON(w io.Writer, tally map[task.Status]int) error {
	doc := struct {
		Pending int `json:"pending"`
		Working int `json:"working"`
		Done    int `json:"done"`
		Failed  int `json:"failed"`
	}{
		Pending: tally[task.StatusPending],
		Working: tally[task.StatusWorking],
		Done:    tally[task.StatusDone],
		Failed:  tally[task.StatusFailed],
	}
	return WriteJSONLine(w, doc, "count")
}

// WriteDependencies renders blocked-by dependencies.
func WriteDependencies(w io.Writer, deps []task.Dependency, asJSON bool) error {
	if asJSON {
		return WriteJSONLine(w, deps, "dependencies")
	}
	if len(deps) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TASK\tBLOCKED_BY\tCREATED") //nolint:errcheck // tabwriter buffers; errors surface at Flush
	for _, dep := range deps {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", dep.TaskID, dep.DependsOnID, dep.Created) //nolint:errcheck // tabwriter buffers; errors surface at Flush
	}
	return tw.Flush()
}

// WriteExplain renders a task and its durable lifecycle history.
func WriteExplain(w io.Writer, t task.Task, events []task.Event, attempts []task.Attempt, asJSON bool) error {
	visibleEvents, omittedEvents := limitEvents(events)
	visibleAttempts, omittedAttempts := limitAttempts(attempts)
	if asJSON {
		return writeExplainJSON(w, t, visibleEvents, visibleAttempts, omittedEvents, omittedAttempts)
	}
	return writeExplainText(w, t, visibleEvents, visibleAttempts, omittedEvents, omittedAttempts)
}

func writeExplainJSON(w io.Writer, t task.Task, events []task.Event, attempts []task.Attempt, omittedEvents, omittedAttempts int) error {
	return WriteJSONLine(w, explainDoc{
		Task:            boundTask(t, maxDetailBodyRunes),
		Events:          boundEvents(events),
		Attempts:        boundAttempts(attempts),
		EventsOmitted:   omittedEvents,
		AttemptsOmitted: omittedAttempts,
	}, "explain")
}

func writeExplainText(w io.Writer, t task.Task, events []task.Event, attempts []task.Attempt, omittedEvents, omittedAttempts int) error {
	if err := WriteShow(w, t, false); err != nil {
		return err
	}
	if err := writeExplainEvents(w, events, omittedEvents); err != nil {
		return err
	}
	return writeExplainAttempts(w, attempts, omittedAttempts)
}

func writeExplainEvents(w io.Writer, events []task.Event, omitted int) error {
	return writeExplainSection(w, "Events:", len(events), omitted, "events", func() error {
		for _, event := range boundEvents(events) {
			if err := writeExplainEvent(w, event); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeExplainEvent(w io.Writer, event task.Event) error {
	if _, err := fmt.Fprintf(w, "  %s  %s", event.At, event.Type); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	if event.Message != "" {
		if _, err := fmt.Fprintf(w, "  %s", event.Message); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	return nil
}

func writeExplainAttempts(w io.Writer, attempts []task.Attempt, omitted int) error {
	return writeExplainSection(w, "Attempts:", len(attempts), omitted, "attempts", func() error {
		for _, attempt := range boundAttempts(attempts) {
			if err := writeExplainAttempt(w, attempt); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeExplainSection(w io.Writer, title string, count, omitted int, name string, writeRows func() error) error {
	if _, err := fmt.Fprintf(w, "\n%s\n", title); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	if count == 0 {
		if _, err := fmt.Fprintln(w, "  none"); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
	}
	if omitted > 0 {
		if _, err := fmt.Fprintf(w, "  ... %d older %s omitted by output limit\n", omitted, name); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
	}
	return writeRows()
}

func writeExplainAttempt(w io.Writer, attempt task.Attempt) error {
	if _, err := fmt.Fprintf(w, "  #%d  %s", attempt.ID, attempt.Status); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	fields := []string{
		fieldIfSet("started", attempt.Started),
		fieldIfSet("finished", attempt.Finished),
		fieldIfSet("error", attempt.Error),
		fieldIfSet("worker", attempt.WorkerID),
		fieldIfSet("agent", attempt.Agent),
	}
	for _, field := range fields {
		if field == "" {
			continue
		}
		if _, err := fmt.Fprintf(w, "  %s", field); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	return nil
}

func fieldIfSet(name, value string) string {
	if value == "" {
		return ""
	}
	return name + "=" + value
}

// WriteTaskJSONLine renders one task as bounded JSON.
func WriteTaskJSONLine(w io.Writer, t task.Task, op string) error {
	return WriteBoundTaskJSONLine(w, t, maxDetailBodyRunes, op)
}

// WriteBoundTaskJSONLine renders one task as JSON with bounded body/error fields.
func WriteBoundTaskJSONLine(w io.Writer, t task.Task, bodyLimit int, op string) error {
	return WriteJSONLine(w, boundTask(t, bodyLimit), op)
}

// WriteJSONLine renders v as one JSON line.
func WriteJSONLine(w io.Writer, v any, op string) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%s: marshal: %w", op, err)
	}
	if _, err := fmt.Fprintln(w, string(b)); err != nil {
		return fmt.Errorf("%s: write: %w", op, err)
	}
	return nil
}

func limitTasks(tasks []task.Task) ([]task.Task, int) {
	if len(tasks) <= maxListTasks {
		return tasks, 0
	}
	return tasks[:maxListTasks], len(tasks) - maxListTasks
}

func limitEvents(events []task.Event) ([]task.Event, int) {
	if len(events) <= maxHistoryItems {
		return events, 0
	}
	return events[len(events)-maxHistoryItems:], len(events) - maxHistoryItems
}

func limitAttempts(attempts []task.Attempt) ([]task.Attempt, int) {
	if len(attempts) <= maxHistoryItems {
		return attempts, 0
	}
	return attempts[len(attempts)-maxHistoryItems:], len(attempts) - maxHistoryItems
}

func boundTask(t task.Task, bodyLimit int) boundedTask {
	body, bodyTruncated := truncateWithStatus(t.Body, bodyLimit)
	errorText, errorTruncated := truncateWithStatus(t.Error, maxMessageRunes)
	t.Body = body
	t.Error = errorText
	return boundedTask{Task: t, BodyTruncated: bodyTruncated, ErrorTruncated: errorTruncated}
}

func boundEvents(events []task.Event) []task.Event {
	bounded := make([]task.Event, len(events))
	copy(bounded, events)
	for i := range bounded {
		bounded[i].Message = truncate(bounded[i].Message, maxMessageRunes)
	}
	return bounded
}

func boundAttempts(attempts []task.Attempt) []task.Attempt {
	bounded := make([]task.Attempt, len(attempts))
	copy(bounded, attempts)
	for i := range bounded {
		bounded[i].Error = truncate(bounded[i].Error, maxMessageRunes)
	}
	return bounded
}

// truncate shortens s to limit runes, appending "…" if truncated. UTF-8 safe.
func truncate(s string, limit int) string {
	truncated, _ := truncateWithStatus(s, limit)
	return truncated
}

func truncateWithStatus(s string, limit int) (string, bool) {
	runes := []rune(s)
	if len(runes) <= limit {
		return s, false
	}
	return string(runes[:limit]) + "…", true
}
