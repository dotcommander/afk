// Package main is the entry point for the afk CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/dotcommander/afk/internal/commands"
)

var version = "dev" // set via -ldflags "-X main.version=..." at release time

func main() {
	err := run()
	os.Exit(exitCode(err))
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, "afk:", err)
	return 1
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	d := &commands.Deps{
		Logger: slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Stdin:  os.Stdin,
		Stdout: stdout,
		Stderr: stderr,
		Now:    time.Now,
	}
	return commands.Execute(ctx, args, stdout, stderr, d, resolvedVersion())
}

// resolvedVersion returns the ldflags-injected version when set; otherwise it
// derives the version from the build info so a `go install
// github.com/dotcommander/afk/cmd/afk@vX.Y.Z` build reports its real version
// without ldflags. A local `go build` still reports "dev".
func resolvedVersion() string {
	if version != "dev" && version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}
