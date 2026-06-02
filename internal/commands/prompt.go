package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/prompt"
)

func newPromptCmd(d *Deps) *cobra.Command {
	var outputPath string
	var taskID string
	var discover bool
	var discoverFull bool

	cmd := &cobra.Command{
		Use:   "prompt",
		Short: "Generate loop prompt Markdown",
		Annotations: map[string]string{
			"skipStoreInit": "true",
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
		RunE: func(cmd *cobra.Command, args []string) error {
			exe := "afk"
			var body string
			if discoverFull && !discover {
				cmd.SilenceUsage = true
				return fmt.Errorf("--full requires --discover")
			}
			if taskID != "" && discover {
				cmd.SilenceUsage = true
				return fmt.Errorf("--task and --discover are mutually exclusive")
			}
			if discover {
				if outputPath == "" {
					return writeDiscoverPrompt(d, discoverFull)
				}
				var stdout strings.Builder
				promptDeps := *d
				promptDeps.Stdout = &stdout
				if err := writeDiscoverPrompt(&promptDeps, discoverFull); err != nil {
					return err
				}
				body = stdout.String()
			} else if taskID != "" {
				data, err := d.Service.Explain(cmd.Context(), taskID)
				if err != nil {
					return err
				}
				body = prompt.Task(exe, data.Task, data.Events, data.Attempts, data.Gates)
			} else {
				body = prompt.Loop(prompt.LoopOptions{
					ExecutablePath: exe,
					SQLitePath:     d.QueuePaths.SQLitePath,
				})
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
