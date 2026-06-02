package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/dotcommander/afk/internal/prompt"
	"github.com/spf13/cobra"
)

func newPromptCmd(d *Deps) *cobra.Command {
	var outputPath string
	var taskID string
	var discover bool
	var discoverFull bool

	cmd := &cobra.Command{
		Use:   cmdPrompt,
		Short: "Generate loop prompt Markdown",
		Annotations: map[string]string{
			skipStoreInitKey: skipStoreInitValue,
		},
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				cmd.SilenceUsage = true
				if discover {
					return fmt.Errorf("prompt --discover does not accept path arguments")
				}
				return fmt.Errorf("prompt does not accept path arguments")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if discoverFull && !discover {
				cmd.SilenceUsage = true
				return fmt.Errorf("--full requires --discover")
			}
			if taskID != "" && discover {
				cmd.SilenceUsage = true
				return fmt.Errorf("--task and --discover are mutually exclusive")
			}
			body, done, err := promptBody(cmd, d, outputPath, taskID, discover, discoverFull)
			if err != nil || done {
				return err
			}
			if outputPath == "" {
				_, err := fmt.Fprint(d.Stdout, body)
				return err
			}
			return os.WriteFile(outputPath, []byte(body), 0o644)
		},
	}
	cmd.Flags().StringVar(&outputPath, "output", "", "write prompt Markdown to path instead of stdout")
	cmd.Flags().StringVar(&taskID, "task", "", "generate a focused prompt for one task id")
	cmd.Flags().BoolVar(&discover, "discover", false, "generate task-discovery workflow guidance")
	cmd.Flags().BoolVar(&discoverFull, "full", false, "print the full task-discovery policy with --discover")
	return cmd
}

// promptBody resolves the prompt Markdown for the requested mode. When done is
// true the body was already written to d.Stdout (discover with no --output) and
// the caller must not emit it again.
func promptBody(cmd *cobra.Command, d *Deps, outputPath, taskID string, discover, discoverFull bool) (body string, done bool, err error) {
	const exe = "afk"
	switch {
	case discover:
		if outputPath == "" {
			return "", true, writeDiscoverPrompt(d, discoverFull)
		}
		var stdout strings.Builder
		promptDeps := *d
		promptDeps.Stdout = &stdout
		if err := writeDiscoverPrompt(&promptDeps, discoverFull); err != nil {
			return "", false, err
		}
		return stdout.String(), false, nil
	case taskID != "":
		data, err := d.Service.Explain(cmd.Context(), taskID)
		if err != nil {
			return "", false, err
		}
		return prompt.Task(exe, data.Task, data.Events, data.Attempts, data.Gates), false, nil
	default:
		return prompt.Loop(prompt.LoopOptions{
			ExecutablePath: exe,
			SQLitePath:     d.QueuePaths.SQLitePath,
		}), false, nil
	}
}
