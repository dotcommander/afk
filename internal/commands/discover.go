package commands

import (
	_ "embed"
	"fmt"
)

//go:embed discover_concise.md
var discoverConciseText string

//go:embed discover_stub.md
var discoverFullText string

func writeDiscoverPrompt(d *Deps, full bool) error {
	body := discoverConciseText
	if full {
		body = discoverFullText
	}
	_, err := fmt.Fprint(d.Stdout, body)
	return err
}
