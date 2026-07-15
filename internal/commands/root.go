package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/store"
)

const (
	cmdPrompt  = "prompt"
	statusName = "status"
	cmdTasks   = "tasks"
)

type Deps struct {
	Service    *app.Service
	QueuePaths store.Paths
	Logger     *slog.Logger
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Now        func() time.Time
}

type CLI struct {
	Queue   string           `help:"Queue database path (overrides AFK_QUEUE env)."`
	Version kong.VersionFlag `name:"version" short:"v" help:"Print version information and quit."`

	Add          AddCmd          `cmd:"" help:"Append a new todo task."`
	Tasks        TasksCmd        `cmd:"" help:"List tasks."`
	Task         TaskCmd         `cmd:"" help:"Show task state, events, and attempts."`
	Status       StatusCmd       `cmd:"" help:"Print queue status."`
	Find         FindCmd         `cmd:"" help:"Search tasks."`
	Take         TakeCmd         `cmd:"" help:"Claim the first ready task.\n\nAgent loop:\n  afk take --worker <name> --lease 60m --summary\n  # execute exactly one returned task\n  afk set <id> done --note \"<verification>\"\n  # or\n  afk set <id> failed --note \"<one-line reason>\"\n\nUse --dry-run to preview ready tasks without claiming. If preview output shows body_truncated=true, add --full to inspect complete task bodies."`
	Set          SetCmd          `cmd:"" help:"Set task status."`
	Retry        RetryCmd        `cmd:"" help:"Open a new attempt for a failed task."`
	Gate         GateCmd         `cmd:"" help:"Manage task gates."`
	Relate       RelateCmd       `cmd:"" help:"Add a typed relation between two tasks."`
	Snapshot     SnapshotCmd     `cmd:"" help:"Export a read-only queue evidence snapshot."`
	Serve        ServeCmd        `cmd:"" help:"Start the afk web dashboard."`
	RequeueStale RequeueStaleCmd `cmd:"" hidden:""`
	Reap         ReapCmd         `cmd:"" help:"Reset stale doing tasks to todo (cron-driven reaper)."`
	Heartbeat    HeartbeatCmd    `cmd:"" hidden:""`
	Prompt       PromptCmd       `cmd:"" help:"Generate loop prompt Markdown."`
	Loop         LoopCmd         `cmd:"" help:"Autonomous worker-driver: claim tasks and run an agent command per task."`
	Goal         GoalCmd         `cmd:"" help:"Compile an objective into an approved task contract and queue it."`
	Import       ImportCmd       `cmd:"" help:"Import operational state from retired systems."`
	Checkpoint   CheckpointCmd   `cmd:"" help:"Manage task progress checkpoints."`
	Artifact     ArtifactCmd     `cmd:"" help:"Manage task artifacts."`
	Help         HiddenHelpCmd   `cmd:"" name:"__help" hidden:""`
}

type HiddenHelpCmd struct {
	Command []string `arg:"" optional:""`
}

func (c *HiddenHelpCmd) Run(parsed *kong.Context) error {
	return parsed.PrintUsage(false)
}

// Execute parses and dispatches one CLI invocation. Metadata paths remain free
// of queue initialization because the store is opened only after Kong selects a
// runnable command.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, d *Deps, version string) error {
	if unknown := unknownRootCommand(args); unknown != "" {
		return fmt.Errorf("unknown command %q for \"afk\"", unknown)
	}
	args = normalizeMetadataArgs(args)
	args = normalizeGoalHelpArgs(args)
	cli := &CLI{}
	exited := false
	parser, err := kong.New(cli,
		kong.Name("afk"),
		kong.Description("AFK task queue"),
		kong.Vars{"version": "afk version " + version},
		kong.Writers(stdout, stderr),
		kong.ConfigureHelp(kong.HelpOptions{Compact: false, NoExpandSubcommands: true}),
		kong.Help(afkHelp),
		kong.Exit(func(int) { exited = true }),
		kong.Bind(d),
		kong.Bind(cli),
		kong.BindTo(ctx, (*context.Context)(nil)),
	)
	if err != nil {
		return err
	}
	parsed, err := parser.Parse(args)
	if exited {
		return nil
	}
	if err != nil {
		return err
	}
	d.Stdout, d.Stderr = stdout, stderr
	paths, err := resolveQueuePaths(cli.Queue)
	if err != nil {
		return err
	}
	d.QueuePaths = paths
	if d.Service == nil && parsed.Command() != "__help <command>" && !promptSkipsStoreInit(cli, parsed.Command()) {
		sqliteStore, err := store.NewSQLite(ctx, paths)
		if err != nil {
			return err
		}
		d.Service = app.NewService(sqliteStore, d.Now, app.WithSidecarPath(app.SidecarPath(paths)))
	}
	return parsed.Run()
}

func normalizeGoalHelpArgs(args []string) []string {
	for i, arg := range args {
		if arg != "goal" || i+1 >= len(args) || (args[i+1] != "--help" && args[i+1] != "-h") {
			continue
		}
		out := append([]string{}, args[:i+1]...)
		out = append(out, "create")
		return append(out, args[i+1:]...)
	}
	return args
}

func afkHelp(options kong.HelpOptions, ctx *kong.Context) error {
	var rendered bytes.Buffer
	stdout := ctx.Stdout
	ctx.Stdout = &rendered
	err := kong.DefaultHelpPrinter(options, ctx)
	ctx.Stdout = stdout
	if err != nil {
		return err
	}
	help := strings.ReplaceAll(rendered.String(), "afk goal create", "afk goal")
	help = strings.ReplaceAll(help, "goal <command>", "goal <objective>")
	help = strings.ReplaceAll(help, "\nArguments:\n  [<extra> ...]\n", "")
	help = strings.ReplaceAll(help, " [<extra> ...]", "")
	_, err = io.WriteString(stdout, help)
	return err
}

func normalizeMetadataArgs(args []string) []string {
	if len(args) == 0 {
		return []string{"--help"}
	}
	onlyQueue := true
	for _, arg := range args {
		if arg == "__help" {
			return []string{"--help"}
		}
		if arg != "--queue" && !strings.HasPrefix(arg, "--queue=") {
			onlyQueue = false
		}
	}
	if onlyQueue || (len(args) == 2 && args[0] == "--queue") {
		return append(args, "--help")
	}
	return args
}

func unknownRootCommand(args []string) string {
	known := map[string]bool{"add": true, "tasks": true, "task": true, "status": true, "find": true, "take": true, "set": true, "retry": true, "gate": true, "relate": true, "snapshot": true, "serve": true, "requeue-stale": true, "reap": true, "heartbeat": true, "prompt": true, "loop": true, "goal": true, "import": true, "checkpoint": true, "artifact": true, "__help": true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--queue" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "--queue=") || arg == "--version" || arg == "-h" || arg == "--help" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if !known[arg] {
			return arg
		}
		return ""
	}
	return ""
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

func promptSkipsStoreInit(cli *CLI, command string) bool {
	if !strings.HasPrefix(command, cmdPrompt) {
		return false
	}
	return cli.Prompt.Discover || cli.Prompt.Task == ""
}
