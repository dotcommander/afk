package output

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/dotcommander/afk/internal/task"
)

// This file renders status output. WriteCount is the shared per-status tally
// section; WriteStatus adds todo and doing task lists.

// WriteCount renders per-status tallies in canonical order.
func WriteCount(w io.Writer, tally map[task.Status]int) error {
	for _, s := range task.OrderedStatuses() {
		if _, err := fmt.Fprintf(w, "%s: %d\n", s, tally[s]); err != nil {
			return fmt.Errorf("status counts: write: %w", err)
		}
	}
	return nil
}

// WriteCountJSON renders per-status tallies as a single JSON object on one line.
// All four canonical status keys are always present (zero if missing) so consumers
// can rely on a fixed shape.
func WriteCountJSON(w io.Writer, tally map[task.Status]int) error {
	doc := struct {
		Pending int `json:"todo"`
		Working int `json:"doing"`
		Done    int `json:"done"`
		Failed  int `json:"failed"`
		Deleted int `json:"deleted"`
	}{
		Pending: tally[task.StatusPending],
		Working: tally[task.StatusWorking],
		Done:    tally[task.StatusDone],
		Failed:  tally[task.StatusFailed],
		Deleted: tally[task.StatusDeleted],
	}
	return WriteJSONLine(w, doc, "status counts")
}

type statusTasksJSON struct {
	Todo  []boundedTask `json:"todo"`
	Doing []boundedTask `json:"doing"`
}

type statusDoc struct {
	Todo    int             `json:"todo"`
	Doing   int             `json:"doing"`
	Done    int             `json:"done"`
	Failed  int             `json:"failed"`
	Deleted int             `json:"deleted"`
	Total   int             `json:"total"`
	Tasks   statusTasksJSON `json:"tasks"`
}

type takeSummaryQueue struct {
	Todo           int `json:"todo"`
	Doing          int `json:"doing"`
	Done           int `json:"done"`
	Failed         int `json:"failed"`
	Deleted        int `json:"deleted"`
	Total          int `json:"total"`
	ReadyRemaining int `json:"ready_remaining"`
}

type takeSummaryDoc struct {
	Task  boundedTask      `json:"task"`
	Queue takeSummaryQueue `json:"queue"`
}

// WriteStatus renders a queue snapshot: per-status tallies plus the todo and
// doing task lists.
func WriteStatus(w io.Writer, tally map[task.Status]int, todo, doing []task.Task, asJSON bool) error {
	if asJSON {
		return writeStatusJSON(w, tally, todo, doing)
	}
	return writeStatusText(w, tally, todo, doing)
}

// WriteTakeSummary renders a claimed task with queue counts after the claim.
func WriteTakeSummary(w io.Writer, claimed task.Task, tally map[task.Status]int, readyRemaining int) error {
	total := 0
	for _, n := range tally {
		total += n
	}
	return WriteJSONLine(w, takeSummaryDoc{
		Task: boundTask(claimed, maxDetailBodyRunes),
		Queue: takeSummaryQueue{
			Todo:           tally[task.StatusPending],
			Doing:          tally[task.StatusWorking],
			Done:           tally[task.StatusDone],
			Failed:         tally[task.StatusFailed],
			Deleted:        tally[task.StatusDeleted],
			Total:          total,
			ReadyRemaining: readyRemaining,
		},
	}, "take summary")
}

func statusListJSON(tasks []task.Task) []boundedTask {
	bounded := make([]boundedTask, 0, len(tasks))
	for _, t := range tasks {
		bounded = append(bounded, boundTask(t, maxListBodyRunes))
	}
	return bounded
}

func writeStatusJSON(w io.Writer, tally map[task.Status]int, todo, doing []task.Task) error {
	total := 0
	for _, n := range tally {
		total += n
	}
	return WriteJSONLine(w, statusDoc{
		Todo:    tally[task.StatusPending],
		Doing:   tally[task.StatusWorking],
		Done:    tally[task.StatusDone],
		Failed:  tally[task.StatusFailed],
		Deleted: tally[task.StatusDeleted],
		Total:   total,
		Tasks: statusTasksJSON{
			Todo:  statusListJSON(todo),
			Doing: statusListJSON(doing),
		},
	}, "status")
}

func writeStatusText(w io.Writer, tally map[task.Status]int, todo, doing []task.Task) error {
	if err := WriteCount(w, tally); err != nil {
		return err
	}
	if err := writeStatusSection(w, "Todo:", todo); err != nil {
		return err
	}
	return writeStatusSection(w, "Doing:", doing)
}

func writeStatusSection(w io.Writer, title string, tasks []task.Task) error {
	if _, err := fmt.Fprintf(w, "\n%s\n", title); err != nil {
		return fmt.Errorf("status: write: %w", err)
	}
	if len(tasks) == 0 {
		if _, err := fmt.Fprintln(w, "  none"); err != nil {
			return fmt.Errorf("status: write: %w", err)
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, t := range tasks {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", t.ID, t.Created, truncate(t.Body, 60)) //nolint:errcheck // tabwriter buffers; errors surface at Flush
	}
	return tw.Flush()
}
