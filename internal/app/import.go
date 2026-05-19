// Package app — Service.Import: validate, dedupe, and bulk-insert an
// ImportDoc as a single batch of tasks plus their slug-resolved
// dependency edges. See internal/task/import.go for the wire types and
// .work/specs/afk-spec-command.md Decision 3 for the algorithm.
package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dotcommander/afk/internal/task"
)

const specTagPrefix = "spec:"

// Import validates an ImportDoc, dedupes against existing spec:<slug>
// tags, assigns IDs, resolves intra-batch blocked_by edges, and writes
// the whole batch in a single store transaction. Returns one
// ImportResult per task in input order.
//
// Errors:
//   - empty doc → fmt.Errorf("import: no tasks") (exit 1)
//   - validation (missing slug/body, dup slug in batch, unknown
//     blocked_by slug) → fmt.Errorf (exit 1)
//   - spec:<slug> already present in queue → *ErrDuplicateSpec (exit 3)
func (s *Service) Import(ctx context.Context, doc task.ImportDoc) ([]task.ImportResult, error) {
	if len(doc.Tasks) == 0 {
		return nil, fmt.Errorf("import: no tasks")
	}
	if err := validateImportBatch(doc.Tasks); err != nil {
		return nil, err
	}
	existing, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	if slug, ok := findSpecTagConflict(existing, doc.Tasks); ok {
		return nil, &ErrDuplicateSpec{Slug: slug}
	}

	base := strconv.FormatInt(s.now().UTC().Unix(), 10)
	created := formatTime(s.now())
	tasks := make([]task.Task, len(doc.Tasks))
	slugToID := make(map[string]string, len(doc.Tasks))
	accum := make([]task.Task, 0, len(existing)+len(doc.Tasks))
	accum = append(accum, existing...)
	for i, it := range doc.Tasks {
		id := uniqueID(accum, base)
		t := buildImportTask(it, id, created)
		tasks[i] = t
		slugToID[it.Slug] = id
		accum = append(accum, t)
	}

	deps, err := resolveDeps(doc.Tasks, slugToID, created)
	if err != nil {
		return nil, err
	}
	if err := s.store.BulkAdd(ctx, tasks, deps); err != nil {
		return nil, err
	}

	results := make([]task.ImportResult, len(tasks))
	for i := range tasks {
		results[i] = task.ImportResult{Slug: doc.Tasks[i].Slug, ID: tasks[i].ID}
	}
	return results, nil
}

// validateImportBatch enforces per-task and batch-level invariants.
// Missing slug/body and duplicate slugs within the batch are reported
// as exit-1 validation errors.
func validateImportBatch(items []task.ImportTask) error {
	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		if it.Slug == "" {
			return fmt.Errorf("import: task with empty slug")
		}
		if it.Body == "" {
			return fmt.Errorf("import: task %q: empty body", it.Slug)
		}
		if !hasBodySection(it.Body, "Success:") {
			return fmt.Errorf("import: task %q: missing Success section", it.Slug)
		}
		if !hasBodySection(it.Body, "Verify:") {
			return fmt.Errorf("import: task %q: missing Verify section", it.Slug)
		}
		if _, dup := seen[it.Slug]; dup {
			return fmt.Errorf("import: duplicate slug %q in batch", it.Slug)
		}
		seen[it.Slug] = struct{}{}
	}
	return nil
}

func hasBodySection(body, section string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == section {
			return true
		}
	}
	return false
}

// findSpecTagConflict scans existing tasks for any spec:<X> tag whose
// suffix matches any spec:<X> tag in the incoming batch. Returns the
// conflicting slug (the substring after "spec:") and true on hit.
func findSpecTagConflict(existing []task.Task, items []task.ImportTask) (string, bool) {
	incoming := make(map[string]struct{})
	for _, it := range items {
		for _, tag := range it.Tags {
			if slug, ok := strings.CutPrefix(tag, specTagPrefix); ok {
				incoming[slug] = struct{}{}
			}
		}
	}
	if len(incoming) == 0 {
		return "", false
	}
	for _, t := range existing {
		for _, tag := range t.Tags {
			slug, ok := strings.CutPrefix(tag, specTagPrefix)
			if !ok {
				continue
			}
			if _, hit := incoming[slug]; hit {
				return slug, true
			}
		}
	}
	return "", false
}

// buildImportTask converts an ImportTask plus assigned id/created into
// a persisted task.Task. Mirrors the field copy in AddWithOptions.
func buildImportTask(it task.ImportTask, id, created string) task.Task {
	return task.Task{
		ID:          id,
		Created:     created,
		Status:      task.StatusPending,
		Body:        it.Body,
		Priority:    it.Priority,
		Tags:        it.Tags,
		CWD:         it.CWD,
		Source:      it.Source,
		Agent:       it.Agent,
		GroupID:     it.GroupID,
		ResourceKey: it.ResourceKey,
	}
}

// resolveDeps walks each ImportTask's BlockedBy slugs, resolves them
// against the batch's slug→ID map, and returns Dependency rows. An
// unknown slug is an exit-1 validation error.
func resolveDeps(items []task.ImportTask, slugToID map[string]string, created string) ([]task.Dependency, error) {
	var deps []task.Dependency
	for _, it := range items {
		for _, depSlug := range it.BlockedBy {
			depID, ok := slugToID[depSlug]
			if !ok {
				return nil, fmt.Errorf("import: task %q: blocked_by %q: unknown slug", it.Slug, depSlug)
			}
			deps = append(deps, task.Dependency{
				TaskID:      slugToID[it.Slug],
				DependsOnID: depID,
				Created:     created,
			})
		}
	}
	return deps, nil
}
