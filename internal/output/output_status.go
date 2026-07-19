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

type statusData struct {
	tally   map[task.Status]int
	todo    []task.Task
	doing   []task.Task
	blocked []task.BlockedTask
	health  task.QueueHealth
	now     time.Time
}

const unleasedStaleAfter = time.Hour

// WriteStatus renders a queue snapshot: per-status tallies plus todo/doing task
// lists, with optional dependency blocker details.
func WriteStatus(w io.Writer, tally map[task.Status]int, todo, doing []task.Task, blocked []task.BlockedTask, health task.QueueHealth, asJSON bool, now time.Time) error {
	data := statusData{
		tally:   tally,
		todo:    todo,
		doing:   doing,
		blocked: blocked,
		health:  health,
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
		if err := writeBlockedSection(w, data.blocked); err != nil {
			return err
		}
	}
	return writeHealthSection(w, data.health)
}

func writeHealthSection(w io.Writer, health task.QueueHealth) error {
	const notAvailable = "n/a"
	oldestReady := notAvailable
	if health.OldestReadyAgeSeconds != nil {
		oldestReady = (time.Duration(*health.OldestReadyAgeSeconds) * time.Second).String()
	}
	oldestActive := notAvailable
	if health.OldestActiveAgeSeconds != nil {
		oldestActive = (time.Duration(*health.OldestActiveAgeSeconds) * time.Second).String()
	}
	failureRate := notAvailable
	if health.TerminalFailureRate != nil {
		failureRate = fmt.Sprintf("%.1f%%", *health.TerminalFailureRate*100)
	}
	_, err := fmt.Fprintf(w, "\nHealth (%s):\n  oldest ready: %s\n  oldest active: %s\n  stale requeues: %d\n  retry attempts: %d\n  terminal failure rate: %s (%d/%d)\n",
		(time.Duration(health.WindowSeconds) * time.Second).String(), oldestReady, oldestActive,
		health.StaleRequeues, health.RetryAttempts, failureRate, health.TerminalFailures, health.TerminalAttempts)
	if err != nil {
		return fmt.Errorf("status health: write: %w", err)
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
