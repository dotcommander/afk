// Package app implements afk use cases independent of CLI presentation.
package app

import (
	"fmt"

	"github.com/dotcommander/afk/internal/store"
)

// ErrNotFound is returned when a task id is absent.
var ErrNotFound = store.ErrNotFound

// ErrDuplicateSpec is returned when a spec import is attempted but a task
// already exists in the queue carrying the same spec:<slug> tag. Callers
// detect it via errors.As and surface it as exit code 3.
type ErrDuplicateSpec struct{ Slug string }

func (e *ErrDuplicateSpec) Error() string {
	return fmt.Sprintf("spec tag conflict: spec:%s already in queue", e.Slug)
}
