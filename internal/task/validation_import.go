package task

import (
	"fmt"
	"strings"
)

// ValidateImportTask checks an import task before it can be persisted.
func ValidateImportTask(it ImportTask) error {
	opts := AddOptions{
		Body:        it.Body,
		Priority:    it.Priority,
		Tags:        it.Tags,
		CWD:         it.CWD,
		Source:      it.Source,
		ResourceKey: it.ResourceKey,
	}
	if err := ValidateAddOptions(opts); err != nil {
		return err
	}
	if !isPlannerGeneratedImport(it) {
		return nil
	}
	if reasons := plannerImportChecks(opts); len(reasons) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalidTask, reasons[0])
	}
	return nil
}

func isPlannerGeneratedImport(it ImportTask) bool {
	// isGeneratedCandidate already covers source "task-discovery" and tags
	// "discovery"/"candidate"/"needs-validation"; this function adds only the
	// planner-specific signals.
	if isGeneratedCandidate(it.Source, it.Tags) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(it.Source)) {
	case "bulk-afk-planner", "spec-planner", "planner":
		return true
	}
	for _, tag := range it.Tags {
		normalized := strings.ToLower(strings.TrimSpace(tag))
		if strings.HasPrefix(normalized, SpecTagPrefix) {
			return true
		}
		switch normalized {
		case "planner", "generated", "bulk-afk":
			return true
		}
	}
	return false
}

// plannerImportChecks runs the subset of generated-task checks that apply to
// planner-imported tasks: evidence, scope, reject-if, churn-phrase, cwd.
func plannerImportChecks(opts AddOptions) []error {
	return evidenceScopeChecks(opts)
}
