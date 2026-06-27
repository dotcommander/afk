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
// All canonical status keys are always present (zero if missing) so consumers
// can rely on a fixed shape. The "total" field is omitted from this envelope to
// preserve the prior wire shape.
func WriteCountJSON(w io.Writer, tally map[task.Status]int) error {
	doc := struct {
		Todo    int `json:"todo"`
		Doing   int `json:"doing"`
		Done    int `json:"done"`
		Failed  int `json:"failed"`
		Deleted int `json:"deleted"`
	}{
		Todo:    tally[task.StatusTodo],
		Doing:   tally[task.StatusDoing],
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

// QueueCounts is the canonical per-status tally shared by every JSON envelope
// in this package and by callers in internal/commands. Embed it (anonymous
// embedding) so JSON inlines the fields at the parent's top level — wire
// shape stays identical to the previous hand-written field lists. Adding a
// future status means editing this struct once instead of N parallel
// definitions.
type QueueCounts struct {
	Todo    int `json:"todo"`
	Doing   int `json:"doing"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Deleted int `json:"deleted"`
	Total   int `json:"total"`
}

// NewQueueCounts builds a QueueCounts from a status tally. Total is the sum
// of every map entry, so callers do not need to maintain it separately.
func NewQueueCounts(tally map[task.Status]int) QueueCounts {
	total := 0
	for _, n := range tally {
		total += n
	}
	return QueueCounts{
		Todo:    tally[task.StatusTodo],
		Doing:   tally[task.StatusDoing],
		Done:    tally[task.StatusDone],
		Failed:  tally[task.StatusFailed],
		Deleted: tally[task.StatusDeleted],
		Total:   total,
	}
}

type statusDoc struct {
	QueueCounts
	Tasks   statusTasksJSON    `json:"tasks"`
	Blocked *[]blockedTaskJSON `json:"blocked,omitempty"`
}

type statusData struct {
	tally   map[task.Status]int
	todo    []task.Task
	doing   []task.Task
	blocked []task.BlockedTask
	now     time.Time
}

type takeSummaryQueue struct {
	QueueCounts
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
	data := statusData{
		tally:   tally,
		todo:    todo,
		doing:   doing,
		blocked: blocked,
		now:     now,
	}
	if asJSON {
		return writeStatusJSON(w, data)
	}
	return writeStatusText(w, data)
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
			QueueCounts:    NewQueueCounts(tally),
			ReadyRemaining: readyCount,
		},
	}, "take preview")
}

func writeTakeSummaryWithLimit(w io.Writer, claimed task.Task, tally map[task.Status]int, readyRemaining int, bodyLimit int) error {
	return WriteJSONLine(w, takeSummaryDoc{
		Claimed: true,
		Task:    boundTask(claimed, bodyLimit),
		Queue: takeSummaryQueue{
			QueueCounts:    NewQueueCounts(tally),
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

func writeStatusJSON(w io.Writer, data statusData) error {
	var blockedDoc *[]blockedTaskJSON
	if data.blocked != nil {
		value := blockedListJSON(data.blocked)
		blockedDoc = &value
	}
	return WriteJSONLine(w, statusDoc{
		QueueCounts: NewQueueCounts(data.tally),
		Tasks: statusTasksJSON{
			Todo:  statusListJSON(data.todo),
			Doing: statusDoingListJSON(data.doing, data.now),
		},
		Blocked: blockedDoc,
	}, "status")
}

func writeStatusText(w io.Writer, data statusData) error {
	if err := WriteCount(w, data.tally); err != nil {
		return err
	}
	if err := writeStatusSection(w, "Todo:", data.todo, data.now, false); err != nil {
		return err
	}
	if err := writeStatusSection(w, "Doing:", data.doing, data.now, true); err != nil {
		return err
	}
	if data.blocked != nil {
		return writeBlockedSection(w, data.blocked)
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
