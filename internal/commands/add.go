// Package commands wires Kong command structs to AFK application actions.
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
)

type AddCmd struct {
	Body        []string `arg:"" required:""`
	Tags        []string `name:"tag" sep:"none" help:"Task tag (repeatable)."`
	Priority    string   `help:"Task priority: urgent, high, normal, or low."`
	CWD         string   `help:"Task working directory context (defaults to current directory)."`
	NoCWD       bool     `name:"no-cwd" help:"Do not record current working directory context."`
	Source      string   `help:"Task source (defaults to cli)."`
	Agent       string   `help:"Preferred agent."`
	GroupID     string   `name:"group" help:"Task group id."`
	ResourceKey string   `name:"resource" help:"Task resource key (defaults to repo:<git-root>; use none to disable)."`
	Stage       string   `help:"Free-form pipeline stage label."`
	BlockedBy   string   `name:"blocked-by" help:"Task id this task is blocked by, or none."`
	JSON        bool     `help:"Emit JSON output."`
	DryRun      bool     `name:"dry-run" help:"Validate without adding a task."`
	Diagnose    bool     `help:"Run all validation checks and report every failure (read-only)."`
	Force       bool     `help:"Bypass validation rejection. Requires AFK_ALLOW_FORCE=1 in environment."`
	RequestID   string   `name:"request-id" help:"Idempotency key for this mutation."`
}

func (c *AddCmd) Run(d *Deps, ctx context.Context) error {
	return runAddCommand(ctx, d, addCommandInput{args: c.Body, tags: c.Tags, priority: c.Priority, cwd: c.CWD, noCWD: c.NoCWD, source: c.Source, agent: c.Agent, groupID: c.GroupID, resourceKey: c.ResourceKey, stage: c.Stage, blockedBy: c.BlockedBy, requestID: c.RequestID}, addCommandMode{asJSON: c.JSON, dryRun: c.DryRun, diagnose: c.Diagnose, force: c.Force})
}

type addCommandInput struct {
	args        []string
	tags        []string
	priority    string
	cwd         string
	noCWD       bool
	source      string
	agent       string
	groupID     string
	resourceKey string
	stage       string
	blockedBy   string
	requestID   string
}

type addCommandMode struct {
	asJSON   bool
	dryRun   bool
	diagnose bool
	force    bool
}

func runAddCommand(ctx context.Context, d *Deps, input addCommandInput, mode addCommandMode) error {
	opts, dependsOnID, err := buildAddCommandOptions(input)
	if err != nil {
		return err
	}
	if mode.force && mode.diagnose {
		return errors.New("--force and --diagnose are mutually exclusive")
	}
	if mode.force && input.requestID != "" {
		return errors.New("--force and --request-id are mutually exclusive")
	}
	if mode.diagnose {
		return runAddDiagnose(ctx, d, opts)
	}
	if dependsOnID != "" {
		if _, err := d.Service.Show(ctx, dependsOnID); err != nil {
			return err
		}
	}
	if mode.dryRun {
		if err := task.ValidateAddOptions(opts); err != nil {
			return addValidationError(ctx, opts, err)
		}
		return writeAddDryRunResult(d, mode.asJSON)
	}
	if mode.force {
		return runAddForce(ctx, d, opts, dependsOnID, mode.asJSON)
	}
	return runAddNormalRequested(ctx, d, opts, dependsOnID, input.requestID, mode.asJSON)
}

func runAddForce(ctx context.Context, d *Deps, opts task.AddOptions, dependsOnID string, asJSON bool) error {
	if v := os.Getenv("AFK_ALLOW_FORCE"); v != "1" {
		return fmt.Errorf("--force requires AFK_ALLOW_FORCE=1 in environment (current: %q)", v)
	}
	if _, err := fmt.Fprintln(d.Stderr, "warning: --force bypassing validation"); err != nil {
		return err
	}
	id, err := d.Service.AddWithOptionsForceBlockedBy(ctx, opts, dependsOnID)
	if err != nil {
		return err
	}
	return writeAddResult(d, id, asJSON)
}

func runAddNormal(ctx context.Context, d *Deps, opts task.AddOptions, dependsOnID string, asJSON bool) error {
	return runAddNormalRequested(ctx, d, opts, dependsOnID, "", asJSON)
}

func runAddNormalRequested(ctx context.Context, d *Deps, opts task.AddOptions, dependsOnID, requestID string, asJSON bool) error {
	actor := opts.Agent
	if actor == "" {
		actor = "afk-cli"
	}
	id, _, err := d.Service.AddWithRequest(ctx, actor, requestID, opts, dependsOnID)
	if err != nil {
		return addValidationError(ctx, opts, err)
	}
	if err := writeAddTTYConfirmation(d.Stderr, id, opts, asJSON); err != nil {
		return err
	}
	return writeAddResult(d, id, asJSON)
}

func writeAddDryRunResult(d *Deps, asJSON bool) error {
	if asJSON {
		return output.WriteJSONLine(d.Stdout, struct {
			Valid bool `json:"valid"`
		}{Valid: true}, "add dry-run")
	}
	_, err := fmt.Fprintln(d.Stdout, "valid")
	return err
}

func writeAddResult(d *Deps, id string, asJSON bool) error {
	if asJSON {
		return output.WriteJSONLine(d.Stdout, struct {
			ID string `json:"id"`
		}{ID: id}, "add")
	}
	_, err := fmt.Fprintln(d.Stdout, id)
	return err
}

func writeAddTTYConfirmation(w io.Writer, id string, opts task.AddOptions, asJSON bool) error {
	if asJSON || !isTerminalWriter(w) {
		return nil
	}
	fields := []string{
		"id=" + id,
		"status=todo",
		fieldIfSet("cwd", opts.CWD),
		fieldIfSet("resource", opts.ResourceKey),
		fieldIfSet("source", opts.Source),
	}
	if len(opts.Tags) > 0 {
		fields = append(fields, "tags="+strings.Join(opts.Tags, ","))
	}
	_, err := fmt.Fprintln(w, "added "+strings.Join(nonEmpty(fields), " "))
	return err
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func fieldIfSet(name, value string) string {
	if value == "" {
		return ""
	}
	return name + "=" + value
}

func addValidationError(ctx context.Context, opts task.AddOptions, err error) error {
	if errors.Is(err, task.ErrInvalidPriority) {
		return err
	}
	if errors.Is(err, task.ErrInvalidTask) {
		if task.IsGeneratedCandidate(opts.Source, opts.Tags) {
			return fmt.Errorf("%w; generated/discovery tasks need the full discovery shape, run with --diagnose to see every missing field or remove --source task-discovery/--tag discovery", err)
		}
	}
	return err
}

// runAddDiagnose runs ValidateAddOptionsAll and reports every failure on its
// own stderr line. Read-only: never calls Service.AddWithOptions and so never
// writes a rejection sidecar entry or inserts a row.
func runAddDiagnose(ctx context.Context, d *Deps, opts task.AddOptions) error {
	err := task.ValidateAddOptionsAll(opts)
	if err == nil {
		_, werr := fmt.Fprintln(d.Stdout, "task validates")
		return werr
	}
	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) {
		for _, e := range joined.Unwrap() {
			if _, werr := fmt.Fprintln(d.Stderr, e.Error()); werr != nil {
				return werr
			}
		}
	} else {
		if _, werr := fmt.Fprintln(d.Stderr, err.Error()); werr != nil {
			return werr
		}
	}
	return err
}
