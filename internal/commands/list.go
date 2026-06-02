package commands

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
)

func newTasksCmd(d *Deps) *cobra.Command {
	var status string
	var stage string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "tasks",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tasks, err := d.Service.List(cmd.Context(), status)
			if err != nil {
				return err
			}
			tasks = filterTasksByStage(tasks, stage)
			return output.WriteList(d.Stdout, tasks, asJSON)
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&stage, "stage", "", "filter by pipeline stage")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSONL output")
	return cmd
}

// filterTasksByStage returns tasks whose Stage equals stage. An empty stage
// returns the input unchanged (no filter applied).
func filterTasksByStage(tasks []task.Task, stage string) []task.Task {
	if stage == "" {
		return tasks
	}
	out := make([]task.Task, 0, len(tasks))
	for _, t := range tasks {
		if t.Stage == stage {
			out = append(out, t)
		}
	}
	return out
}

func newFindCmd(d *Deps) *cobra.Command {
	var status string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "find <query>",
		Short: "Search tasks",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tasks, err := d.Service.Find(cmd.Context(), args[0], status)
			if err != nil {
				return err
			}
			return output.WriteList(d.Stdout, tasks, asJSON)
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSONL output")
	return cmd
}

func newJSONByIDCmd(use, short, jsonUsage string, run func(context.Context, string, bool) error) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), args[0], asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, jsonUsage)
	return cmd
}

func newStatusCmd(d *Deps) *cobra.Command {
	var asJSON bool
	var summary bool
	var includeBlocked bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print queue status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			snapshot, err := d.Service.Status(cmd.Context())
			if err != nil {
				return err
			}
			if summary {
				if asJSON {
					return output.WriteCountJSON(d.Stdout, snapshot.Counts)
				}
				return output.WriteCount(d.Stdout, snapshot.Counts)
			}
			var blocked []task.BlockedTask
			if includeBlocked {
				blocked, err = d.Service.Blocked(cmd.Context())
				if err != nil {
					return err
				}
				if blocked == nil {
					blocked = []task.BlockedTask{}
				}
			}
			return output.WriteStatus(d.Stdout, snapshot.Counts, snapshot.Todo, snapshot.Doing, blocked, asJSON, d.Now())
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	cmd.Flags().BoolVar(&summary, "summary", false, "emit counts only")
	cmd.Flags().BoolVar(&includeBlocked, "blocked", false, "include dependency-blocked todo task details")
	return cmd
}

func newTaskCmd(d *Deps) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "task <id>",
		Short: "Show task state, events, and attempts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := d.Service.Explain(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			gates, err := d.Service.Gates(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return output.WriteExplainWithGates(d.Stdout, data.Task, gates, data.Events, data.Attempts, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return cmd
}
