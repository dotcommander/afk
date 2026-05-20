package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/prompt"
)

func newPromptCmd(d *Deps) *cobra.Command {
	var outputPath string
	var taskID string

	cmd := &cobra.Command{
		Use:   "prompt",
		Short: "Generate loop prompt Markdown",
		Annotations: map[string]string{
			"skipStoreInit": "true",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				exe = "afk"
			}
			var body string
			if taskID != "" {
				data, err := d.Service.Explain(cmd.Context(), taskID)
				if err != nil {
					return err
				}
				body = prompt.Task(exe, data.Task, data.Events, data.Attempts)
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
	return cmd
}
