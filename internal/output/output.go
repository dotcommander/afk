// Package output renders tasks for the CLI.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

const (
	// DefaultListBodyRunes is the default JSON body limit for task-list previews.
	DefaultListBodyRunes = maxListBodyRunes

	maxListTasks       = 100
	maxListBodyRunes   = 500
	maxDetailBodyRunes = 8000
	maxHistoryItems    = 50
	maxMessageRunes    = 1000
)

type boundedTask struct {
	task.Task
	Claim          *task.ClaimDiagnostics `json:"claim,omitempty"`
	BodyTruncated  bool                   `json:"body_truncated,omitzero"`
	ErrorTruncated bool                   `json:"error_truncated,omitzero"`
	BodyHint       string                 `json:"body_hint,omitzero"`
}

type listSummary struct {
	Omitted int    `json:"omitted"`
	Limit   int    `json:"limit"`
	Reason  string `json:"reason"`
}

// WriteList renders a bounded task list as either JSONL or an aligned table.
func WriteList(w io.Writer, tasks []task.Task, asJSON bool) error {
	return WriteListWithBodyLimit(w, tasks, asJSON, maxListBodyRunes)
}

// WriteListFull renders a task list without truncating task bodies.
func WriteListFull(w io.Writer, tasks []task.Task, asJSON bool) error {
	return WriteListWithBodyLimit(w, tasks, asJSON, 0)
}

// WriteListWithBodyLimit renders a task list with caller-selected JSON body
// bounds. A bodyLimit of 0 leaves task bodies intact.
func WriteListWithBodyLimit(w io.Writer, tasks []task.Task, asJSON bool, bodyLimit int) error {
	return WriteListWithBodyLimitHint(w, tasks, asJSON, bodyLimit, "")
}

// WriteListWithBodyLimitHint renders a task list and adds bodyHint to JSON
// objects whose body was truncated.
func WriteListWithBodyLimitHint(w io.Writer, tasks []task.Task, asJSON bool, bodyLimit int, bodyHint string) error {
	if len(tasks) == 0 {
		return nil
	}
	visible, omitted := limitTasks(tasks)
	if asJSON {
		return writeListJSON(w, visible, omitted, bodyLimit, bodyHint)
	}
	return writeListTable(w, visible, omitted)
}

func writeListJSON(w io.Writer, tasks []task.Task, omitted int, bodyLimit int, bodyHint string) error {
	for _, t := range tasks {
		if err := WriteBoundTaskJSONLineWithHint(w, t, bodyLimit, bodyHint, "list"); err != nil {
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

func writeTaskDetail(w io.Writer, t task.Task) error {
	if _, err := fmt.Fprintf(w, "ID: %s\nStatus: %s\nCreated: %s\nBody: %s\n", t.ID, t.Status, t.Created, truncate(t.Body, maxDetailBodyRunes)); err != nil {
		return fmt.Errorf("task detail: write: %w", err)
	}
	for _, field := range showFields(t) {
		if field.value == "" {
			continue
		}
		if _, err := fmt.Fprintf(w, "%s: %s\n", field.name, field.value); err != nil {
			return fmt.Errorf("task detail: write: %w", err)
		}
	}
	if len(t.Dependencies) > 0 {
		if _, err := fmt.Fprintln(w, "Dependencies:"); err != nil {
			return fmt.Errorf("task detail: write: %w", err)
		}
		for _, dep := range t.Dependencies {
			if _, err := fmt.Fprintf(w, "  %s (%s)\n", dep.DependsOnID, relationDisplay(dep.Type)); err != nil {
				return fmt.Errorf("task detail: write: %w", err)
			}
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
		{name: "Priority", value: string(t.Priority)},
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

// WriteTaskJSONLine renders one task as bounded JSON.
func WriteTaskJSONLine(w io.Writer, t task.Task, op string) error {
	return WriteBoundTaskJSONLine(w, t, maxDetailBodyRunes, op)
}

// WriteBoundTaskJSONLine renders one task as JSON with bounded body/error fields.
func WriteBoundTaskJSONLine(w io.Writer, t task.Task, bodyLimit int, op string) error {
	return WriteJSONLine(w, boundTask(t, bodyLimit), op)
}

// WriteBoundTaskJSONLineWithHint renders one task as bounded JSON, adding
// bodyHint only when body truncation occurs.
func WriteBoundTaskJSONLineWithHint(w io.Writer, t task.Task, bodyLimit int, bodyHint string, op string) error {
	return WriteJSONLine(w, boundTaskWithHint(t, bodyLimit, bodyHint), op)
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
	return boundTaskWithHint(t, bodyLimit, "")
}

// relationDisplay resolves a dependency's relation type for display. Empty
// (legacy rows) renders as the default blocks relation so output is never blank.
func relationDisplay(rt task.RelationType) task.RelationType {
	if rt == "" {
		return task.RelationBlocks
	}
	return rt
}

func boundTaskWithHint(t task.Task, bodyLimit int, bodyHint string) boundedTask {
	body, bodyTruncated := truncateWithStatus(t.Body, bodyLimit)
	errorText, errorTruncated := truncateWithStatus(t.Error, maxMessageRunes)
	t.Body = body
	t.Error = errorText
	if len(t.Dependencies) > 0 {
		deps := make([]task.Dependency, len(t.Dependencies))
		copy(deps, t.Dependencies)
		for i := range deps {
			deps[i].Type = relationDisplay(deps[i].Type)
		}
		t.Dependencies = deps
	}
	if !bodyTruncated {
		bodyHint = ""
	}
	return boundedTask{Task: t, BodyTruncated: bodyTruncated, ErrorTruncated: errorTruncated, BodyHint: bodyHint}
}

func boundTaskWithClaim(t task.Task, bodyLimit int, now time.Time, unleasedStaleAfter time.Duration) boundedTask {
	bounded := boundTask(t, bodyLimit)
	if diag, ok := task.ClaimDiagnosticsFor(t, now, unleasedStaleAfter); ok {
		bounded.Claim = diag
	}
	return bounded
}

func boundTasks(tasks []task.Task, bodyLimit int) []boundedTask {
	return boundTasksWithHint(tasks, bodyLimit, "")
}

func boundTasksWithHint(tasks []task.Task, bodyLimit int, bodyHint string) []boundedTask {
	bounded := make([]boundedTask, 0, len(tasks))
	for _, t := range tasks {
		bounded = append(bounded, boundTaskWithHint(t, bodyLimit, bodyHint))
	}
	return bounded
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
	if limit <= 0 {
		return s, false
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s, false
	}
	return string(runes[:limit]) + "…", true
}
