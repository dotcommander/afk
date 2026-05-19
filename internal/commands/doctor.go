package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/task"
)

func newDoctorCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check queue health and installation basics",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tasks, err := d.Service.List(cmd.Context(), "")
			if err != nil {
				return err
			}
			if err := writeDoctorReport(d.Stdout, d.QueuePaths.SQLitePath, summarizeDoctorTasks(tasks)); err != nil {
				return err
			}
			now := time.Now()
			if d.Now != nil {
				now = d.Now()
			}
			return writeDoctorRejectionSection(d.Stdout, d.Service, app.SidecarPath(d.QueuePaths), now)
		},
	}
}

type doctorSnapshot struct {
	tally        map[task.Status]int
	workingTasks int
}

func summarizeDoctorTasks(tasks []task.Task) doctorSnapshot {
	snapshot := doctorSnapshot{tally: map[task.Status]int{}}
	for _, t := range tasks {
		snapshot.tally[t.Status]++
		if t.Status == task.StatusWorking {
			snapshot.workingTasks++
		}
	}
	return snapshot
}

func writeDoctorReport(w io.Writer, queuePath string, snapshot doctorSnapshot) error {
	if _, err := fmt.Fprintf(w, "queue: %s\n", queuePath); err != nil {
		return err
	}
	if err := writeDoctorDBStatus(w, queuePath); err != nil {
		return err
	}
	if err := writeDoctorTaskCounts(w, snapshot); err != nil {
		return err
	}
	if err := writeDoctorWorkingWarning(w, snapshot.workingTasks); err != nil {
		return err
	}
	return writeDoctorBinaryPrompt(w)
}

func writeDoctorDBStatus(w io.Writer, queuePath string) error {
	if _, err := os.Stat(queuePath); err != nil {
		if os.IsNotExist(err) {
			_, writeErr := fmt.Fprintln(w, "db: missing")
			return writeErr
		}
		return fmt.Errorf("stat queue db: %w", err)
	}
	_, err := fmt.Fprintln(w, "db: ok")
	return err
}

func writeDoctorTaskCounts(w io.Writer, snapshot doctorSnapshot) error {
	_, err := fmt.Fprintf(
		w,
		"pending: %d\nworking: %d\ndone: %d\nfailed: %d\n",
		snapshot.tally[task.StatusPending],
		snapshot.tally[task.StatusWorking],
		snapshot.tally[task.StatusDone],
		snapshot.tally[task.StatusFailed],
	)
	return err
}

func writeDoctorWorkingWarning(w io.Writer, workingTasks int) error {
	if workingTasks == 0 {
		return nil
	}
	_, err := fmt.Fprintf(w, "working tasks: %d (inspect with afk ls --status working --json)\n", workingTasks)
	return err
}

func writeDoctorBinaryPrompt(w io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		exe = "unknown"
	}
	_, err = fmt.Fprintf(w, "binary: %s\nprompt: ok\n", exe)
	return err
}

// writeDoctorRejectionSection appends the rejection sidecar summary to w.
// ErrSidecarDisabled → prints "disabled" and returns nil.
// Other errors from ListRejected → propagated to caller.
func writeDoctorRejectionSection(w io.Writer, svc *app.Service, sidecarPath string, now time.Time) error {
	records, err := svc.ListRejected()
	if err != nil {
		if errors.Is(err, app.ErrSidecarDisabled) {
			_, werr := fmt.Fprintln(w, "Rejection sidecar: disabled")
			return werr
		}
		return err
	}

	if _, err := fmt.Fprintf(w, "Rejection sidecar: %s\n", sidecarPath); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  total: %d\n", len(records)); err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}

	// Count recent (last 7 days) and build histogram.
	cutoff := now.Add(-7 * 24 * time.Hour)
	recentCount := 0
	histogram := map[string]int{}
	for _, rec := range records {
		if rec.Ts.After(cutoff) {
			recentCount++
		}
		key := normalizeRejectionReason(rec.Reason)
		histogram[key]++
	}

	if _, err := fmt.Fprintf(w, "  recent (last 7d): %d\n", recentCount); err != nil {
		return err
	}

	// Sort histogram: descending count, then alphabetical reason.
	type kv struct {
		key   string
		count int
	}
	entries := make([]kv, 0, len(histogram))
	for k, c := range histogram {
		entries = append(entries, kv{k, c})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].key < entries[j].key
	})

	if _, err := fmt.Fprintln(w, "  by reason:"); err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := fmt.Fprintf(w, "    %s: %d\n", e.key, e.count); err != nil {
			return err
		}
	}

	// Most recent record.
	latest := records[len(records)-1]
	excerpt := firstLine(latest.Body)
	if len(excerpt) > 60 {
		excerpt = excerpt[:60]
	}
	_, err = fmt.Fprintf(w, "  most recent: %s — %q\n", latest.Ts.Format(time.RFC3339), excerpt)
	return err
}

// normalizeRejectionReason strips the "invalid task: " prefix for grouping.
func normalizeRejectionReason(reason string) string {
	return strings.TrimPrefix(reason, "invalid task: ")
}
