package commands

import (
	_ "embed"
	"fmt"
)

//go:embed discover_stub.md
var discoverStubText string

func writeDiscoverPrompt(d *Deps) error {
	_, err := fmt.Fprint(d.Stdout, discoverStubText)
	return err
}
