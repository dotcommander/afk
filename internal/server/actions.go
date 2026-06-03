package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/task"
)

// setTaskInput is the JSON body for PATCH /api/tasks/{id}.
// An empty / absent body is valid and produces the zero value.
// Note, Error, and Reason are all accepted as the "message" field
// for backwards compatibility with existing clients.
type setTaskInput struct {
	Status string `json:"status"`
	Note   string `json:"note"`
	Error  string `json:"error"`
	Reason string `json:"reason"`
}

// decodeSetTask reads and decodes an optional JSON body into setTaskInput.
// EOF (empty body) produces the zero value without error.
func decodeSetTask(r *http.Request) (setTaskInput, error) {
	var in setTaskInput
	if err := decodeJSONBody(r, &in); err != nil {
		return setTaskInput{}, fmt.Errorf("decode body: %w", err)
	}
	return in, nil
}

func decodeJSONBody(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 64*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("body must contain exactly one JSON value")
	}
	return nil
}

// handleSetTask serves PATCH /api/tasks/{id} with {"status":"done","note":"..."}.
func (s *Server) handleSetTask(w http.ResponseWriter, r *http.Request) {
	id, ok := resolveID(w, r)
	if !ok {
		return
	}

	in, err := decodeSetTask(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	status, ok := task.ParseStatus(in.Status)
	if !ok {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%w: %q", task.ErrInvalidStatus, in.Status))
		return
	}
	message := in.Note
	if message == "" {
		message = in.Error
	}
	if message == "" {
		message = in.Reason
	}

	if err := s.svc.SetStatus(r.Context(), id, status, message); err != nil {
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

// handleCreate serves POST /api/tasks — enqueues a new todo task via the
// same validated path as `afk add`. Invalid task content → 400.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in createInput
	if err := decodeJSONBody(r, &in); err != nil {
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
// handleTake serves POST /api/take. Pass ?dry_run=true to preview ready tasks.
func (s *Server) handleTake(w http.ResponseWriter, r *http.Request) {
	dryRun, err := strconv.ParseBool(defaultString(r.URL.Query().Get("dry_run"), "false"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("parse dry_run: %w", err))
		return
	}
	if dryRun {
		tasks, err := s.svc.Ready(r.Context())
		writeResult(w, tasks, err)
		return
	}
	lease, err := parseLease(r.URL.Query().Get("lease"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	task, err := s.svc.Take(r.Context(), lease, r.URL.Query().Get("worker"), "")
	writeResult(w, task, err)
}

func parseLease(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	lease, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse lease: %w", err)
	}
	if lease <= 0 {
		return 0, fmt.Errorf("parse lease: duration must be positive")
	}
	return lease, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
