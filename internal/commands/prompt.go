package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dotcommander/afk/internal/prompt"
)

type PromptCmd struct {
	Paths      []string `arg:"" optional:""`
	OutputPath string   `name:"output" help:"Write prompt Markdown to path instead of stdout."`
	Task       string   `help:"Generate a focused prompt for one task id."`
	Discover   bool     `help:"Generate task-discovery workflow guidance."`
	Full       bool     `help:"Print the full task-discovery policy with --discover."`
}

func (c *PromptCmd) Run(d *Deps, ctx context.Context) error {
	if len(c.Paths) > 0 {
		if c.Discover {
			return fmt.Errorf("prompt --discover does not accept path arguments")
		}
		return fmt.Errorf("prompt does not accept path arguments")
	}
	if c.Full && !c.Discover {
		return fmt.Errorf("--full requires --discover")
	}
	if c.Task != "" && c.Discover {
		return fmt.Errorf("--task and --discover are mutually exclusive")
	}
	result, err := promptBody(ctx, d, c.OutputPath, c.Task, c.Discover, c.Full)
	if err != nil || result.alreadyWritten {
		return err
	}
	if c.OutputPath == "" {
		_, err := fmt.Fprint(d.Stdout, result.body)
		return err
	}
	return os.WriteFile(c.OutputPath, []byte(result.body), 0o644)
}

type promptBodyResult struct {
	body           string
	alreadyWritten bool
}

// promptBody resolves the prompt Markdown for the requested mode. Discover
// without --output writes directly to d.Stdout; alreadyWritten tells the caller
// not to emit the body again.
func promptBody(ctx context.Context, d *Deps, outputPath, taskID string, discover, discoverFull bool) (promptBodyResult, error) {
	const exe = "afk"
	switch {
	case discover:
		if outputPath == "" {
			return promptBodyResult{alreadyWritten: true}, writeDiscoverPrompt(d, discoverFull)
		}
		var stdout strings.Builder
		promptDeps := *d
		promptDeps.Stdout = &stdout
		if err := writeDiscoverPrompt(&promptDeps, discoverFull); err != nil {
			return promptBodyResult{}, err
		}
		return promptBodyResult{body: stdout.String()}, nil
	case taskID != "":
		data, err := d.Service.Explain(ctx, taskID)
		if err != nil {
			return promptBodyResult{}, err
		}
		return promptBodyResult{body: prompt.Task(exe, data.Task, data.Events, data.Attempts, data.Gates)}, nil
	default:
		return promptBodyResult{body: prompt.Loop(prompt.LoopOptions{
			ExecutablePath: exe,
			SQLitePath:     d.QueuePaths.SQLitePath,
		})}, nil
	}
}
