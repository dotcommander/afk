package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/task"
)

// actionInput is the optional JSON body for mutation endpoints.
// An empty / absent body is valid and produces the zero value.
type actionInput struct {
	Note     string   `json:"note"`
	Error    string   `json:"error"`
	Reason   string   `json:"reason"`
	Statuses []string `json:"statuses"`
}

// defaultPruneStatuses matches the afk prune CLI default (--status "done,failed").
var defaultPruneStatuses = []task.Status{task.StatusDone, task.StatusFailed}

// decodeInput reads and decodes an optional JSON body.
// EOF (empty body) produces the zero actionInput without error.
func decodeInput(r *http.Request) (actionInput, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, 64*1024)
	var in actionInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		return actionInput{}, fmt.Errorf("decode body: %w", err)
	}
	return in, nil
}

// actionFn is the type stored in the dispatch table.
type actionFn func(ctx context.Context, id string, in actionInput) error

// handleAction serves POST /api/tasks/{id}/{action}.
// It looks up the action in a data-driven dispatch table, calls the matching
// service mutator, then re-fetches and returns the updated task as JSON.
func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	id, ok := resolveID(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")

	// Dispatch table: action name → service mutator call.
	// Asymmetries (which field each action reads from actionInput) are explicit
	// here as data rather than scattered across per-action handlers.
	dispatch := map[string]actionFn{
		"done": func(ctx context.Context, id string, in actionInput) error {
			return s.svc.Done(ctx, id, in.Note)
		},
		"fail": func(ctx context.Context, id string, in actionInput) error {
			msg := in.Error
			if msg == "" {
				msg = in.Reason
			}
			return s.svc.Fail(ctx, id, msg)
		},
		"retry": func(ctx context.Context, id string, _ actionInput) error {
			return s.svc.Retry(ctx, id)
		},
		"reset": func(ctx context.Context, id string, _ actionInput) error {
			return s.svc.Reset(ctx, id)
		},
		"unblock": func(ctx context.Context, id string, _ actionInput) error {
			return s.svc.Unblock(ctx, id)
		},
		"promote": func(ctx context.Context, id string, _ actionInput) error {
			return s.svc.Promote(ctx, id)
		},
	}

	fn, known := dispatch[action]
	if !known {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown action %q; valid: done, fail, retry, reset, unblock, promote", action))
		return
	}

	in, err := decodeInput(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	if err := fn(r.Context(), id, in); err != nil {
		writeResult(w, nil, err)
		return
	}

	t, err := s.svc.Show(r.Context(), id)
	writeResult(w, t, err)
}

// createInput is the JSON body for POST /api/tasks.
type createInput struct {
	Body string `json:"body"`
	CWD  string `json:"cwd"`
}

// handleCreate serves POST /api/tasks — enqueues a new pending task via the
// same validated path as `afk add`. Invalid task content → 400.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(nil, r.Body, 64*1024)
	var in createInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	cwd := in.CWD
	if cwd != "" {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("resolve cwd: %w", err))
			return
		}
		cwd = abs
	}
	defaults := app.InferAddDefaults(cwd)
	id, err := s.svc.AddWithOptions(r.Context(), task.AddOptions{
		Body:        in.Body,
		Tags:        defaultTags(defaults),
		CWD:         cwd,
		Source:      "web",
		ResourceKey: defaults.ResourceKey,
	})
	if err != nil {
		if errors.Is(err, task.ErrInvalidTask) {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func defaultTags(defaults app.AddDefaults) []string {
	if defaults.RepoTag == "" {
		return nil
	}
	return []string{defaults.RepoTag}
}

// handlePrune serves POST /api/prune.
// Accepts an optional JSON body with "statuses"; defaults to done+failed.
func (s *Server) handlePrune(w http.ResponseWriter, r *http.Request) {
	in, err := decodeInput(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	statuses := defaultPruneStatuses
	if len(in.Statuses) > 0 {
		statuses = make([]task.Status, len(in.Statuses))
		for i, s := range in.Statuses {
			status := task.Status(s)
			if !task.ValidStatus(status) {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("%w: %q", task.ErrInvalidStatus, s))
				return
			}
			statuses[i] = status
		}
	}

	n, err := s.svc.Prune(r.Context(), statuses)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pruned": n})
}
