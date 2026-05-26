package commands

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
)

func newSetCmd(d *Deps) *cobra.Command {
	var asJSON bool
	var summary bool
	var noteFlag string
	var noteFile string

	cmd := &cobra.Command{
		Use:   "set <id> <status> [note...]",
		Short: "Set task status",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			status, ok := task.ParseStatus(args[1])
			if !ok {
				return fmt.Errorf("%w: %q", task.ErrInvalidStatus, args[1])
			}
			note, err := resolveSetNote(d, args[2:], noteFlag, noteFile)
			if err != nil {
				return err
			}
			if err := d.Service.SetStatus(cmd.Context(), args[0], status, note); err != nil {
				return err
			}
			updated, err := d.Service.Show(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if asJSON || summary {
				result := setResultFromTask(updated, note)
				if summary {
					snapshot, err := d.Service.Status(cmd.Context())
					if err != nil {
						return err
					}
					result.Queue = queueResultFromCounts(snapshot.Counts)
				}
				return output.WriteJSONLine(d.Stdout, result, "set")
			}
			_, err = fmt.Fprintf(d.Stdout, "set %s %s\n", args[0], status)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	cmd.Flags().BoolVar(&summary, "summary", false, "emit JSON output with queue counts")
	cmd.Flags().StringVar(&noteFlag, "note", "", "status note text")
	cmd.Flags().StringVar(&noteFile, "note-file", "", "read status note from file, or '-' for stdin")
	return cmd
}

func newRetryCmd(d *Deps) *cobra.Command {
	var asJSON bool
	var reason string

	cmd := &cobra.Command{
		Use:   "retry <id>",
		Short: "Open a new attempt for a failed task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			note := retryNote(reason)
			if err := d.Service.SetStatus(cmd.Context(), args[0], task.StatusDoing, note); err != nil {
				return err
			}
			if asJSON {
				return output.WriteJSONLine(d.Stdout, setResult{
					ID:     args[0],
					Status: task.StatusDoing,
					Note:   note,
				}, "retry")
			}
			_, err := fmt.Fprintf(d.Stdout, "retry %s doing\n", args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "one-line reason the task is ready to retry")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return cmd
}

func retryNote(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "retrying"
	}
	return "retrying: " + reason
}

type setResult struct {
	ID     string       `json:"id"`
	Status task.Status  `json:"status"`
	Title  string       `json:"title,omitzero"`
	Note   string       `json:"note,omitzero"`
	Queue  *queueResult `json:"queue,omitempty"`
}

type queueResult struct {
	Todo    int `json:"todo"`
	Doing   int `json:"doing"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Deleted int `json:"deleted"`
	Total   int `json:"total"`
}

const maxNoteBytes = 64 * 1024

func resolveSetNote(d *Deps, args []string, noteFlag, noteFile string) (string, error) {
	sources := 0
	if len(args) > 0 {
		sources++
	}
	if noteFlag != "" {
		sources++
	}
	if noteFile != "" {
		sources++
	}
	if sources > 1 {
		return "", fmt.Errorf("set note: use only one of positional note, --note, or --note-file")
	}
	switch {
	case noteFile != "":
		return readNoteFile(d, noteFile)
	case noteFlag != "":
		return noteFlag, nil
	default:
		return strings.Join(args, " "), nil
	}
}

func readNoteFile(d *Deps, path string) (string, error) {
	var r io.Reader
	if path == "-" {
		r = d.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("set note: open %s: %w", path, err)
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxNoteBytes+1))
	if err != nil {
		return "", fmt.Errorf("set note: read: %w", err)
	}
	if len(data) > maxNoteBytes {
		return "", fmt.Errorf("set note: note exceeds %d bytes", maxNoteBytes)
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func setResultFromTask(t task.Task, note string) setResult {
	return setResult{
		ID:     t.ID,
		Status: t.Status,
		Title:  taskTitle(t.Body),
		Note:   note,
	}
}

func taskTitle(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	for _, sep := range []string{"\n", ".", ";"} {
		if idx := strings.Index(body, sep); idx >= 0 {
			body = body[:idx]
		}
	}
	fields := strings.Fields(body)
	const maxTitleWords = 8
	if len(fields) > maxTitleWords {
		fields = fields[:maxTitleWords]
	}
	title := strings.Join(fields, " ")
	const maxTitleRunes = 80
	runes := []rune(title)
	if len(runes) > maxTitleRunes {
		title = string(runes[:maxTitleRunes]) + "..."
	}
	return title
}

func queueResultFromCounts(counts map[task.Status]int) *queueResult {
	q := &queueResult{
		Todo:    counts[task.StatusTodo],
		Doing:   counts[task.StatusDoing],
		Done:    counts[task.StatusDone],
		Failed:  counts[task.StatusFailed],
		Deleted: counts[task.StatusDeleted],
	}
	q.Total = q.Todo + q.Doing + q.Done + q.Failed + q.Deleted
	return q
}
