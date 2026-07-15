package commands

import (
	"context"
	"fmt"
	"github.com/dotcommander/afk/internal/server"
	"net"
)

var loopbackHosts = map[string]bool{"": true, "localhost": true, "127.0.0.1": true, "::1": true}

type ServeCmd struct {
	Addr  string   `default:"127.0.0.1:1969" help:"Address to listen on."`
	Open  bool     `default:"true" help:"Open the dashboard in a browser on start."`
	Extra []string `arg:"" optional:"" hidden:""`
}

func (c *ServeCmd) Run(d *Deps, ctx context.Context) error {
	host, _, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return fmt.Errorf("serve: invalid addr %q: %w", c.Addr, err)
	}
	if !loopbackHosts[host] {
		if _, err = fmt.Fprintf(d.Stderr, "warning: dashboard exposes task bodies on non-loopback address %s\n", c.Addr); err != nil {
			return fmt.Errorf("serve: write warning: %w", err)
		}
	}
	if _, err = fmt.Fprintf(d.Stdout, "afk dashboard: http://%s/\n", c.Addr); err != nil {
		return fmt.Errorf("serve: write banner: %w", err)
	}
	return server.New(d.Service, d.Logger, c.Addr, c.Open).Run(ctx)
}
