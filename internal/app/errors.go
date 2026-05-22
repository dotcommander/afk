// Package app implements afk use cases independent of CLI presentation.
package app

import "github.com/dotcommander/afk/internal/store"

// ErrNotFound is returned when a task id is absent.
var ErrNotFound = store.ErrNotFound
