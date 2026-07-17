package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
)

type SetCmd struct {
	ID        string   `arg:"" required:""`
	Status    string   `arg:"" required:""`
	NoteArgs  []string `arg:"" optional:""`
	JSON      bool     `help:"Emit JSON output."`
	Summary   bool     `help:"Emit JSON output with queue counts."`
	Note      string   `help:"Status note text."`
	NoteFile  string   `name:"note-file" help:"Read status note from file, or '-' for stdin."`
	Stage     *string  `help:"Set the free-form pipeline stage label (omit to leave unchanged)."`
	Worker    string   `help:"Worker id that owns the claim; fences a terminal set against a stale lease."`
	Force     bool     `help:"Allow done/failed without a completion note."`
	RequestID string   `name:"request-id" help:"Idempotency key for this mutation."`
}

func (c *SetCmd) Run(d *Deps, ctx context.Context) error {
	return runSet(ctx, append([]string{c.ID, c.Status}, c.NoteArgs...), d, setCommandOptions{stage: c.Stage, noteFlag: c.Note, noteFile: c.NoteFile, asJSON: c.JSON, summary: c.Summary, force: c.Force, workerID: c.Worker, requestID: c.RequestID})
}

type setCommandOptions struct {
	stage     *string
	noteFlag  string
	noteFile  string
	asJSON    bool
	summary   bool
	force     bool
	workerID  string
	requestID string
}

// runSet executes the `set` command: parse status, resolve the note, enforce
// the completion-note rule, apply the status (with optional stage), then emit
// either JSON/summary or a plain confirmation line. Extracted from newSetCmd's
// RunE closure to keep cognitive complexity within the linter threshold.
func runSet(ctx context.Context, args []string, d *Deps, opts setCommandOptions) error {
	status, note, stagePtr, err := prepareSet(args, d, opts)
	if err != nil {
		return err
	}
	if opts.workerID != "" {
		updated, err := d.Service.SetStatusWithStageWorkerTask(ctx, args[0], status, note, stagePtr, opts.force, opts.workerID)
		if err != nil {
			return err
		}
		return writeSetResult(ctx, d, opts, updated, status, note)
	}
	updated, err := applySetMutation(ctx, d, opts, args[0], status, note, stagePtr)
	if err != nil {
		return err
	}
	return writeSetResult(ctx, d, opts, updated, status, note)
}

func prepareSet(args []string, d *Deps, opts setCommandOptions) (task.Status, string, *string, error) {
	status, ok := task.ParseStatus(args[1])
	if !ok {
		return "", "", nil, fmt.Errorf("%w: %q", task.ErrInvalidStatus, args[1])
	}
	note, err := resolveSetNote(d, args[2:], opts.noteFlag, opts.noteFile)
	if err != nil {
		return "", "", nil, err
	}
	if !opts.force && (status == task.StatusDone || status == task.StatusFailed) && strings.TrimSpace(note) == "" {
		return "", "", nil, fmt.Errorf("%w", task.ErrMissingCompletionNote)
	}
	if opts.requestID != "" && opts.summary {
		return "", "", nil, fmt.Errorf("--request-id cannot be combined with --summary")
	}
	if opts.requestID != "" && opts.workerID != "" {
		return "", "", nil, fmt.Errorf("--request-id cannot be combined with --worker")
	}
	if opts.stage != nil {
		return status, note, opts.stage, nil
	}
	return status, note, nil, nil
}

func writeSetResult(ctx context.Context, d *Deps, opts setCommandOptions, updated task.Task, status task.Status, note string) error {
	if opts.asJSON || opts.summary {
		result := setResultFromTask(updated, note)
		if opts.summary {
			snapshot, err := d.Service.Status(ctx)
			if err != nil {
				return err
			}
			result.Queue = queueResultFromCounts(snapshot.Counts)
		}
		return output.WriteJSONLine(d.Stdout, result, "set")
	}
	_, err := fmt.Fprintf(d.Stdout, "set %s %s\n", updated.ID, status)
	return err
}

func applySetMutation(ctx context.Context, d *Deps, opts setCommandOptions, id string, status task.Status, note string, stage *string) (task.Task, error) {
	switch {
	case opts.requestID != "":
		updated, _, err := d.Service.SetStatusWithRequest(ctx, "afk-cli", opts.requestID, id, status, note, stage, opts.force)
		return updated, err
	case opts.force:
		if err := d.Service.SetStatusWithStageForce(ctx, id, status, note, stage); err != nil {
			return task.Task{}, err
		}
	default:
		if err := d.Service.SetStatusWithStage(ctx, id, status, note, stage); err != nil {
			return task.Task{}, err
		}
	}
	return d.Service.Show(ctx, id)
}

type RetryCmd struct {
	ID          string `arg:"" required:""`
	Reason      string `help:"One-line reason the task is ready to retry."`
	Disposition string `help:"Retry disposition: manual opens an attempt now; deferred returns it to todo." default:"manual"`
	AvailableAt string `name:"available-at" help:"RFC3339 eligibility time required for a deferred retry."`
	JSON        bool   `help:"Emit JSON output."`
}

func (c *RetryCmd) Run(d *Deps, ctx context.Context) error {
	disposition, err := task.ParseRetryDisposition(c.Disposition)
	if err != nil {
		return err
	}
	canonicalAvailableAt, err := task.ValidateRetryDisposition(disposition, c.AvailableAt)
	if err != nil {
		return err
	}
	note := retryNote(c.Reason)
	if disposition == task.RetryDispositionDeferred {
		note = deferredRetryNote(c.Reason, canonicalAvailableAt)
	}
	updated, err := d.Service.Retry(ctx, c.ID, disposition, canonicalAvailableAt, note)
	if err != nil {
		return err
	}
	if c.JSON {
		return output.WriteJSONLine(d.Stdout, setResult{
			ID:          c.ID,
			Status:      updated.Status,
			Note:        note,
			Disposition: disposition,
			AvailableAt: updated.AvailableAt,
		}, "retry")
	}
	if disposition == task.RetryDispositionDeferred {
		_, err = fmt.Fprintf(d.Stdout, "retry %s todo available_at=%s\n", c.ID, updated.AvailableAt)
		return err
	}
	_, err = fmt.Fprintf(d.Stdout, "retry %s doing\n", c.ID)
	return err
}

func retryNote(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "retrying"
	}
	return "retrying: " + reason
}

func deferredRetryNote(reason, availableAt string) string {
	note := "retry deferred until " + availableAt
	reason = strings.TrimSpace(reason)
	if reason != "" {
		note += ": " + reason
	}
	return note
}

type setResult struct {
	ID          string                `json:"id"`
	Status      task.Status           `json:"status"`
	Title       string                `json:"title,omitzero"`
	Note        string                `json:"note,omitzero"`
	Disposition task.RetryDisposition `json:"disposition,omitzero"`
	AvailableAt string                `json:"available_at,omitzero"`
	Queue       *output.QueueCounts   `json:"queue,omitempty"`
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
		defer func() { _ = f.Close() }()
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

func queueResultFromCounts(counts map[task.Status]int) *output.QueueCounts {
	q := output.NewQueueCounts(counts)
	return &q
}
