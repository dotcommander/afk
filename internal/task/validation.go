package task

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrInvalidTask reports a task body that AFK should not schedule or execute.
var ErrInvalidTask = errors.New("invalid task")

// Named generated-candidate rejection reasons. Each wraps ErrInvalidTask so
// existing callers using errors.Is(err, ErrInvalidTask) continue to match.
var (
	ErrMissingDiscoveryPrefix = errors.New("generated task must start with [discovery:<kind>:<topic>]")
	ErrMissingSuccess         = errors.New("generated task must include success criteria")
	ErrMissingVerify          = errors.New("generated task must include a verification command")
	ErrMissingEvidence        = errors.New("generated task must include evidence")
	ErrMissingScope           = errors.New("generated task must include scope")
	ErrMissingRejectIf        = errors.New("generated task must include reject-if criteria")
	ErrMissingCwd             = errors.New("generated task must include cwd metadata or an absolute path")
	ErrInvalidPriority        = errors.New("priority must be urgent, high, normal, or low")
)

// ChurnPhraseError reports that a generated task body contains a phrase known
// to produce vague or churn-prone work. It wraps ErrInvalidTask via Is so that
// errors.Is(err, ErrInvalidTask) returns true.
type ChurnPhraseError struct {
	Phrase string
}

func (e *ChurnPhraseError) Error() string {
	return fmt.Sprintf("contains churn phrase: %q", e.Phrase)
}

// Is reports whether target is ErrInvalidTask.
func (e *ChurnPhraseError) Is(target error) bool {
	return target == ErrInvalidTask
}

var absolutePathRE = regexp.MustCompile(`(?:^|\s)/[^\s]+`)
var discoveryPrefixRE = regexp.MustCompile(`^\[discovery:[a-zA-Z0-9._-]+:[a-zA-Z0-9._-]+\]`)

// ValidateAddOptions checks whether a new task is safe and concrete enough for
// AFK. Discovery-generated tasks get stricter checks because they may be
// auto-enqueued without a human reviewing each body first. Returns the first
// failure (fail-fast). For a complete report, use ValidateAddOptionsAll.
func ValidateAddOptions(opts AddOptions) error {
	if err := ValidateBody(opts.Body); err != nil {
		return err
	}
	if err := ValidatePriority(opts.Priority); err != nil {
		return err
	}
	if !isGeneratedCandidate(opts.Source, opts.Tags) {
		return nil
	}
	if reasons := generatedCandidateChecks(opts); len(reasons) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalidTask, reasons[0])
	}
	return nil
}

// ValidateAddOptionsAll runs every check that ValidateAddOptions performs and
// returns errors.Join of every failure. Useful for diagnostic mode where the
// caller wants a complete report instead of fail-fast. ValidateBody still
// short-circuits because an empty/non-actionable body makes later checks
// meaningless. Each joined error satisfies errors.Is(err, ErrInvalidTask) and
// errors.Is(err, ErrMissing*) for the specific reason that fired.
func ValidateAddOptionsAll(opts AddOptions) error {
	if err := ValidateBody(opts.Body); err != nil {
		return err
	}
	if err := ValidatePriority(opts.Priority); err != nil {
		return err
	}
	if !isGeneratedCandidate(opts.Source, opts.Tags) {
		return nil
	}
	reasons := generatedCandidateChecks(opts)
	if len(reasons) == 0 {
		return nil
	}
	wrapped := make([]error, len(reasons))
	for i, reason := range reasons {
		wrapped[i] = fmt.Errorf("%w: %w", ErrInvalidTask, reason)
	}
	return errors.Join(wrapped...)
}

// ValidatePriority rejects misspelled priority values instead of letting the
// scheduler silently treat them as normal priority.
func ValidatePriority(priority string) error {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "", "urgent", "high", "normal", "low":
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidPriority, priority)
	}
}

// generatedCandidateChecks runs every generated-candidate validation check in
// order and returns the underlying reason errors. Callers wrap each in
// ErrInvalidTask. Ordering matches the historical ValidateAddOptions sequence.
func generatedCandidateChecks(opts AddOptions) []error {
	var failures []error
	if !discoveryPrefixRE.MatchString(strings.TrimSpace(opts.Body)) {
		failures = append(failures, ErrMissingDiscoveryPrefix)
	}
	if !containsFold(opts.Body, "success:") {
		failures = append(failures, ErrMissingSuccess)
	}
	if !containsFold(opts.Body, "verify") && !containsFold(opts.Body, "verification") {
		failures = append(failures, ErrMissingVerify)
	}
	if !containsFold(opts.Body, "evidence:") {
		failures = append(failures, ErrMissingEvidence)
	}
	if !containsFold(opts.Body, "scope:") {
		failures = append(failures, ErrMissingScope)
	}
	if !containsFold(opts.Body, "reject-if:") {
		failures = append(failures, ErrMissingRejectIf)
	}
	if phrase := firstGeneratedChurnPhrase(opts.Body); phrase != "" {
		failures = append(failures, &ChurnPhraseError{Phrase: phrase})
	}
	if opts.CWD == "" && !hasAbsolutePath(opts.Body) {
		failures = append(failures, ErrMissingCwd)
	}
	return failures
}

// ValidateImportTask checks an import task before it can be persisted.
func ValidateImportTask(it ImportTask) error {
	return ValidateAddOptions(AddOptions{
		Body:        it.Body,
		Tags:        it.Tags,
		CWD:         it.CWD,
		Source:      it.Source,
		ResourceKey: it.ResourceKey,
	})
}

// ValidateBody rejects task bodies that are empty, non-actionable placeholders,
// or physical/personal-service requests rather than software work.
func ValidateBody(body string) error {
	normalized := normalizeBody(body)
	if normalized == "" {
		return invalid("empty body")
	}
	for _, phrase := range invalidExactBodies {
		if normalized == phrase {
			return invalid("not actionable software work")
		}
	}
	for _, phrase := range invalidBodyPhrases {
		if strings.Contains(normalized, phrase) {
			return invalid("physical or personal-service request")
		}
	}
	return nil
}

func invalid(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidTask, reason)
}

func normalizeBody(body string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(body))), " ")
}

func isGeneratedCandidate(source string, tags []string) bool {
	if strings.EqualFold(source, "task-discovery") {
		return true
	}
	for _, tag := range tags {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "candidate", "needs-validation", "discovery":
			return true
		}
	}
	return false
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func firstGeneratedChurnPhrase(body string) string {
	normalized := normalizeBody(body)
	for _, phrase := range invalidGeneratedPhrases {
		if strings.Contains(normalized, phrase) {
			return phrase
		}
	}
	return ""
}

var invalidExactBodies = []string{
	"continue this",
	"fix the thing",
	"make it better",
	"do something",
	"whatever",
	"pick my nose",
}

var invalidBodyPhrases = []string{
	"pick my nose",
	"brush my teeth",
	"wash my hair",
}

var invalidGeneratedPhrases = []string{
	"and/or",
	"clean up",
	"cleanup",
	"etc.",
	"improve overall",
	"investigate broadly",
	"make better",
	"refactor broadly",
	"x or y",
}

func hasAbsolutePath(s string) bool {
	if absolutePathRE.MatchString(s) {
		return true
	}
	for _, field := range strings.Fields(s) {
		if filepath.IsAbs(field) {
			return true
		}
	}
	return false
}
