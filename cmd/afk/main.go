// Package main is the entry point for the afk CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dotcommander/afk/internal/commands"
)

var version = "dev" // set via -ldflags "-X main.version=..." at release time

func main() {
	err := run()
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "afk:", err)
	var ee *commands.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.Code)
	}
	os.Exit(1)
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d := &commands.Deps{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Now:    time.Now,
	}
	return commands.NewRoot(d, version).ExecuteContext(ctx)
}
