package commands

import (
	"fmt"
	"net"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/server"
)

// loopbackHosts is the set of host values that are considered safe/local.
var loopbackHosts = map[string]bool{
	"":          true,
	"localhost": true,
	"127.0.0.1": true,
	"::1":       true,
}

func newServeCmd(d *Deps) *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the afk web dashboard",
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return fmt.Errorf("serve: invalid addr %q: %w", addr, err)
			}
			if !loopbackHosts[host] {
				if _, err := fmt.Fprintf(d.Stderr, "warning: dashboard exposes task bodies on non-loopback address %s\n", addr); err != nil {
					return fmt.Errorf("serve: write warning: %w", err)
				}
			}
			if _, err := fmt.Fprintf(d.Stdout, "afk dashboard: http://%s/\n", addr); err != nil {
				return fmt.Errorf("serve: write banner: %w", err)
			}
			srv := server.New(d.Service, d.Logger, addr)
			return srv.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:1969", "address to listen on")
	return cmd
}
