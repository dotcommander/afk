package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

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
			return writeDoctorReport(d.Stdout, d.QueuePaths.SQLitePath, summarizeDoctorTasks(tasks))
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
