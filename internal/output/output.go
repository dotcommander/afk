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

// WriteList renders a task list as either JSONL or an aligned table.
func WriteList(w io.Writer, tasks []task.Task, asJSON bool) error {
	if len(tasks) == 0 {
		return nil
	}
	if asJSON {
		for _, t := range tasks {
			if err := WriteJSONLine(w, t, "list"); err != nil {
				return err
			}
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tCREATED\tBODY") //nolint:errcheck // tabwriter buffers; errors surface at Flush
	for _, t := range tasks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.ID, t.Status, t.Created, truncate(t.Body, 60)) //nolint:errcheck // tabwriter buffers; errors surface at Flush
	}
	return tw.Flush()
}

// WriteShow renders a single task.
func WriteShow(w io.Writer, t task.Task, asJSON bool) error {
	if asJSON {
		return WriteJSONLine(w, t, "show")
	}
	if _, err := fmt.Fprintf(w, "ID: %s\nStatus: %s\nCreated: %s\nBody: %s\n", t.ID, t.Status, t.Created, t.Body); err != nil {
		return fmt.Errorf("show: write: %w", err)
	}
	if t.Priority != "" {
		if _, err := fmt.Fprintf(w, "Priority: %s\n", t.Priority); err != nil {
			return fmt.Errorf("show: write: %w", err)
		}
	}
	if len(t.Tags) > 0 {
		if _, err := fmt.Fprintf(w, "Tags: %s\n", strings.Join(t.Tags, ", ")); err != nil {
			return fmt.Errorf("show: write: %w", err)
		}
	}
	if t.CWD != "" {
		if _, err := fmt.Fprintf(w, "CWD: %s\n", t.CWD); err != nil {
			return fmt.Errorf("show: write: %w", err)
		}
	}
	if t.Source != "" {
		if _, err := fmt.Fprintf(w, "Source: %s\n", t.Source); err != nil {
			return fmt.Errorf("show: write: %w", err)
		}
	}
	if t.Agent != "" {
		if _, err := fmt.Fprintf(w, "Agent: %s\n", t.Agent); err != nil {
			return fmt.Errorf("show: write: %w", err)
		}
	}
	if t.GroupID != "" {
		if _, err := fmt.Fprintf(w, "Group: %s\n", t.GroupID); err != nil {
			return fmt.Errorf("show: write: %w", err)
		}
	}
	if t.ResourceKey != "" {
		if _, err := fmt.Fprintf(w, "Resource: %s\n", t.ResourceKey); err != nil {
			return fmt.Errorf("show: write: %w", err)
		}
	}
	if t.Started != "" {
		if _, err := fmt.Fprintf(w, "Started: %s\n", t.Started); err != nil {
			return fmt.Errorf("show: write: %w", err)
		}
	}
	if t.Finished != "" {
		if _, err := fmt.Fprintf(w, "Finished: %s\n", t.Finished); err != nil {
			return fmt.Errorf("show: write: %w", err)
		}
	}
	if t.Error != "" {
		if _, err := fmt.Fprintf(w, "Error: %s\n", t.Error); err != nil {
			return fmt.Errorf("show: write: %w", err)
		}
	}
	return nil
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
	if asJSON {
		return WriteJSONLine(w, struct {
			Task     task.Task      `json:"task"`
			Events   []task.Event   `json:"events"`
			Attempts []task.Attempt `json:"attempts"`
		}{Task: t, Events: events, Attempts: attempts}, "explain")
	}
	if err := WriteShow(w, t, false); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\nEvents:"); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	if len(events) == 0 {
		if _, err := fmt.Fprintln(w, "  none"); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
	}
	for _, event := range events {
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
	}
	if _, err := fmt.Fprintln(w, "\nAttempts:"); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	if len(attempts) == 0 {
		if _, err := fmt.Fprintln(w, "  none"); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
	}
	for _, attempt := range attempts {
		if _, err := fmt.Fprintf(w, "  #%d  %s", attempt.ID, attempt.Status); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
		if attempt.Started != "" {
			if _, err := fmt.Fprintf(w, "  started=%s", attempt.Started); err != nil {
				return fmt.Errorf("explain: write: %w", err)
			}
		}
		if attempt.Finished != "" {
			if _, err := fmt.Fprintf(w, "  finished=%s", attempt.Finished); err != nil {
				return fmt.Errorf("explain: write: %w", err)
			}
		}
		if attempt.Error != "" {
			if _, err := fmt.Fprintf(w, "  error=%s", attempt.Error); err != nil {
				return fmt.Errorf("explain: write: %w", err)
			}
		}
		if attempt.WorkerID != "" {
			if _, err := fmt.Fprintf(w, "  worker=%s", attempt.WorkerID); err != nil {
				return fmt.Errorf("explain: write: %w", err)
			}
		}
		if attempt.Agent != "" {
			if _, err := fmt.Fprintf(w, "  agent=%s", attempt.Agent); err != nil {
				return fmt.Errorf("explain: write: %w", err)
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
	}
	return nil
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

// truncate shortens s to limit runes, appending "…" if truncated. UTF-8 safe.
func truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
