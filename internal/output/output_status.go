package output

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

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
		Pending: tally[task.StatusTodo],
		Working: tally[task.StatusDoing],
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

type blockedTaskJSON struct {
	Task     boundedTask    `json:"task"`
	Blockers []task.Blocker `json:"blockers"`
}

// queueCounts is the canonical per-status tally shared by every JSON envelope
// in this package. Embed it (anonymous embedding) so JSON inlines the fields
// at the parent's top level — wire shape stays identical to the previous
// hand-written field lists. Adding a future status means editing this struct
// once instead of N parallel definitions.
type queueCounts struct {
	Todo    int `json:"todo"`
	Doing   int `json:"doing"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Deleted int `json:"deleted"`
	Total   int `json:"total"`
}

func newQueueCounts(tally map[task.Status]int) queueCounts {
	total := 0
	for _, n := range tally {
		total += n
	}
	return queueCounts{
		Todo:    tally[task.StatusTodo],
		Doing:   tally[task.StatusDoing],
		Done:    tally[task.StatusDone],
		Failed:  tally[task.StatusFailed],
		Deleted: tally[task.StatusDeleted],
		Total:   total,
	}
}

type statusDoc struct {
	queueCounts
	Tasks   statusTasksJSON    `json:"tasks"`
	Blocked *[]blockedTaskJSON `json:"blocked,omitempty"`
}

type takeSummaryQueue struct {
	queueCounts
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

const unleasedStaleAfter = time.Hour

// WriteStatus renders a queue snapshot: per-status tallies plus todo/doing task
// lists, with optional dependency blocker details.
func WriteStatus(w io.Writer, tally map[task.Status]int, todo, doing []task.Task, blocked []task.BlockedTask, asJSON bool, now time.Time) error {
	if asJSON {
		return writeStatusJSON(w, tally, todo, doing, blocked, now)
	}
	return writeStatusText(w, tally, todo, doing, blocked, now)
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
	return WriteJSONLine(w, takePreviewDoc{
		Claimed: false,
		Tasks:   boundTasksWithHint(ready, bodyLimit, bodyHint),
		Queue: takeSummaryQueue{
			queueCounts:    newQueueCounts(tally),
			ReadyRemaining: readyCount,
		},
	}, "take preview")
}

func writeTakeSummaryWithLimit(w io.Writer, claimed task.Task, tally map[task.Status]int, readyRemaining int, bodyLimit int) error {
	return WriteJSONLine(w, takeSummaryDoc{
		Claimed: true,
		Task:    boundTask(claimed, bodyLimit),
		Queue: takeSummaryQueue{
			queueCounts:    newQueueCounts(tally),
			ReadyRemaining: readyRemaining,
		},
	}, "take summary")
}

func statusListJSON(tasks []task.Task) []boundedTask {
	return boundTasks(tasks, maxListBodyRunes)
}

func statusDoingListJSON(tasks []task.Task, now time.Time) []boundedTask {
	bounded := make([]boundedTask, 0, len(tasks))
	for _, t := range tasks {
		bounded = append(bounded, boundTaskWithClaim(t, maxListBodyRunes, now, unleasedStaleAfter))
	}
	return bounded
}

func blockedListJSON(blocked []task.BlockedTask) []blockedTaskJSON {
	out := make([]blockedTaskJSON, 0, len(blocked))
	for _, item := range blocked {
		out = append(out, blockedTaskJSON{
			Task:     boundTask(item.Task, maxListBodyRunes),
			Blockers: item.Blockers,
		})
	}
	return out
}

func writeStatusJSON(w io.Writer, tally map[task.Status]int, todo, doing []task.Task, blocked []task.BlockedTask, now time.Time) error {
	var blockedDoc *[]blockedTaskJSON
	if blocked != nil {
		value := blockedListJSON(blocked)
		blockedDoc = &value
	}
	return WriteJSONLine(w, statusDoc{
		queueCounts: newQueueCounts(tally),
		Tasks: statusTasksJSON{
			Todo:  statusListJSON(todo),
			Doing: statusDoingListJSON(doing, now),
		},
		Blocked: blockedDoc,
	}, "status")
}

func writeStatusText(w io.Writer, tally map[task.Status]int, todo, doing []task.Task, blocked []task.BlockedTask, now time.Time) error {
	if err := WriteCount(w, tally); err != nil {
		return err
	}
	if err := writeStatusSection(w, "Todo:", todo, now, false); err != nil {
		return err
	}
	if err := writeStatusSection(w, "Doing:", doing, now, true); err != nil {
		return err
	}
	if blocked != nil {
		return writeBlockedSection(w, blocked)
	}
	return nil
}

func writeStatusSection(w io.Writer, title string, tasks []task.Task, now time.Time, includeClaim bool) error {
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
		claim := ""
		if includeClaim {
			claim = claimText(t, now)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", t.ID, t.Created, truncate(t.Body, 60), claim) //nolint:errcheck // tabwriter buffers; errors surface at Flush
	}
	return tw.Flush()
}

func claimText(t task.Task, now time.Time) string {
	diag, ok := task.ClaimDiagnosticsFor(t, now, unleasedStaleAfter)
	if !ok {
		return ""
	}
	parts := []string{fmt.Sprintf("age=%s", (time.Duration(diag.AgeSeconds) * time.Second).Round(time.Second))}
	if diag.Stale {
		parts = append(parts, "stale="+diag.Reason)
	}
	return strings.Join(parts, " ")
}

func writeBlockedSection(w io.Writer, blocked []task.BlockedTask) error {
	if _, err := fmt.Fprintln(w, "\nBlocked:"); err != nil {
		return fmt.Errorf("status: write: %w", err)
	}
	if len(blocked) == 0 {
		if _, err := fmt.Fprintln(w, "  none"); err != nil {
			return fmt.Errorf("status: write: %w", err)
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, item := range blocked {
		fmt.Fprintf(tw, "  %s\tblocked by %s\t%s\n", item.Task.ID, blockerText(item.Blockers), truncate(item.Task.Body, 60)) //nolint:errcheck // tabwriter buffers; errors surface at Flush
	}
	return tw.Flush()
}

func blockerText(blockers []task.Blocker) string {
	parts := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		if blocker.Missing {
			parts = append(parts, blocker.ID+"(missing)")
			continue
		}
		parts = append(parts, blocker.ID+"("+string(blocker.Status)+")")
	}
	return strings.Join(parts, ",")
}
