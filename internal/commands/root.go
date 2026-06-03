package commands

import (
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/store"
)

// Command names and annotation tokens shared across the commands package.
// cmdPrompt is both the prompt command's Use string and the name matched in
// skipStoreInit; skipStoreInitKey/skipStoreInitValue are the annotation pair
// that lets `afk prompt` skip opening the DB — keep them paired and unchanged.
const (
	cmdPrompt          = "prompt"
	skipStoreInitKey   = "skipStoreInit"
	skipStoreInitValue = "true"
	// statusName is both the "status" command name and the "status" flag name.
	statusName = "status"
)

// Deps bundles command dependencies. Constructed once in main(), passed by pointer.
type Deps struct {
	Service    *app.Service
	QueuePaths store.Paths
	Logger     *slog.Logger
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Now        func() time.Time
}

// NewRoot builds the root cobra command. version is exposed via --version.
func NewRoot(d *Deps, version string) *cobra.Command {
	var queuePath string

	root := &cobra.Command{
		Use:           "afk",
		Short:         "AFK task queue",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := resolveQueuePaths(queuePath)
			if err != nil {
				return err
			}
			d.QueuePaths = paths
			if skipStoreInit(cmd) {
				return nil
			}
			sqliteStore, err := store.NewSQLite(cmd.Context(), paths)
			if err != nil {
				return err
			}
			d.Service = app.NewService(sqliteStore, d.Now, app.WithSidecarPath(app.SidecarPath(paths)))
			return nil
		},
	}

	root.PersistentFlags().StringVar(&queuePath, "queue", "", "queue database path (overrides AFK_QUEUE env)")
	root.SetHelpCommand(&cobra.Command{
		Use:    "__help [command]",
		Hidden: true,
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Root().Help()
		},
	})

	root.AddCommand(
		newAddCmd(d),
		newTasksCmd(d),
		newTaskCmd(d),
		newStatusCmd(d),
		newFindCmd(d),
		newTakeCmd(d),
		newSetCmd(d),
		newRetryCmd(d),
		newGateCmd(d),
		newRelateCmd(d),
		newSnapshotCmd(d),
		newServeCmd(d),
		newRequeueStaleCmd(d),
		newHeartbeatCmd(d),
		newPromptCmd(d),
		newLoopCmd(d),
		newGoalCmd(d),
	)

	return root
}

func resolveQueuePaths(flagPath string) (store.Paths, error) {
	path := flagPath
	if path == "" {
		path = os.Getenv("AFK_QUEUE")
	}
	if path == "" {
		var err error
		path, err = store.DefaultPath()
		if err != nil {
			return store.Paths{}, err
		}
	}
	return store.ResolvePaths(path), nil
}

func skipStoreInit(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[skipStoreInitKey] != skipStoreInitValue {
			continue
		}
		if c.Name() == cmdPrompt {
			return promptSkipsStoreInit(c)
		}
		return true
	}
	return false
}

func promptSkipsStoreInit(c *cobra.Command) bool {
	discoverFlag := c.Flags().Lookup("discover")
	if discoverFlag != nil && discoverFlag.Value.String() == "true" {
		return true
	}
	taskFlag := c.Flags().Lookup("task")
	if taskFlag != nil && taskFlag.Value.String() != "" {
		return false
	}
	return true
}
