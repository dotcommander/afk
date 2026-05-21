// Package commands — afk import: bulk-insert tasks from a JSON document
// on stdin (Phase 1 Decision 4 of .work/specs/afk-spec-command.md).
package commands

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/spf13/cobra"
)

func newImportCmd(d *Deps) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Bulk-insert tasks from a JSON document on stdin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var doc task.ImportDoc
			if err := json.NewDecoder(d.Stdin).Decode(&doc); err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("import: decode: %w", err)}
			}
			importFn := d.Service.Import
			if dryRun {
				importFn = d.Service.ValidateImport
			}
			results, err := importFn(cmd.Context(), doc)
			if err != nil {
				return mapImportErr(err)
			}
			for _, r := range results {
				if err := output.WriteJSONLine(d.Stdout, r, "import"); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate and resolve an import batch without writing tasks")
	return cmd
}

func mapImportErr(err error) error {
	var dup *app.ErrDuplicateSpec
	switch {
	case errors.As(err, &dup):
		return &ExitError{Code: 3, Err: err}
	case errors.Is(err, store.ErrDependencyCycle):
		return &ExitError{Code: 2, Err: err}
	default:
		return &ExitError{Code: 1, Err: err}
	}
}
