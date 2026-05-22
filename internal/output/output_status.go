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

type takePreviewDoc struct {
	Claimed bool             `json:"claimed"`
	Tasks   []boundedTask    `json:"tasks"`
	Queue   takeSummaryQueue `json:"queue"`
}

type takeSummaryDoc struct {
	Claimed bool             `json:"claimed"`
	Task    boundedTask      `json:"task"`
	Queue   takeSummaryQueue `json:"queue"`
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
	return writeTakeSummaryWithLimit(w, claimed, tally, readyRemaining, maxDetailBodyRunes)
}

// WriteTakeSummaryFull renders a claimed task envelope without truncating the body.
func WriteTakeSummaryFull(w io.Writer, claimed task.Task, tally map[task.Status]int, readyRemaining int) error {
	return writeTakeSummaryWithLimit(w, claimed, tally, readyRemaining, 0)
}

// WriteTakePreview renders a dry-run envelope for ready tasks.
func WriteTakePreview(w io.Writer, ready []task.Task, tally map[task.Status]int, readyCount int, bodyLimit int, bodyHint string) error {
	total := 0
	for _, n := range tally {
		total += n
	}
	return WriteJSONLine(w, takePreviewDoc{
		Claimed: false,
		Tasks:   boundTasksWithHint(ready, bodyLimit, bodyHint),
		Queue: takeSummaryQueue{
			Todo:           tally[task.StatusPending],
			Doing:          tally[task.StatusWorking],
			Done:           tally[task.StatusDone],
			Failed:         tally[task.StatusFailed],
			Deleted:        tally[task.StatusDeleted],
			Total:          total,
			ReadyRemaining: readyCount,
		},
	}, "take preview")
}

func writeTakeSummaryWithLimit(w io.Writer, claimed task.Task, tally map[task.Status]int, readyRemaining int, bodyLimit int) error {
	total := 0
	for _, n := range tally {
		total += n
	}
	return WriteJSONLine(w, takeSummaryDoc{
		Claimed: true,
		Task:    boundTask(claimed, bodyLimit),
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
	return boundTasks(tasks, maxListBodyRunes)
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
