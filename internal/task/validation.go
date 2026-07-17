package task

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
	ErrInvalidAvailableAt     = errors.New("available-at must be an RFC3339 timestamp")
)

// ErrMissingCompletionNote reports that a terminal `set` transition (done or
// failed) was attempted without completion evidence. It is a CLI usage error,
// independent of ErrInvalidTask. Bypass with --force.
var ErrMissingCompletionNote = errors.New("done/failed requires a note as completion evidence (use --force to override)")

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
	if _, err := CanonicalAvailableAt(opts.AvailableAt); err != nil {
		return err
	}
	if !IsGeneratedCandidate(opts.Source, opts.Tags) {
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
	if _, err := CanonicalAvailableAt(opts.AvailableAt); err != nil {
		return err
	}
	if !IsGeneratedCandidate(opts.Source, opts.Tags) {
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
// scheduler silently treat them as normal priority. Delegates to ParsePriority
// so the recognized set has a single source of truth.
func ValidatePriority(priority Priority) error {
	_, err := ParsePriority(string(priority))
	return err
}

// CanonicalAvailableAt validates an optional eligibility timestamp and
// normalizes it to UTC for deterministic SQLite ordering comparisons.
func CanonicalAvailableAt(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidAvailableAt, value)
	}
	return parsed.UTC().Format(time.RFC3339), nil
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
	return append(failures, evidenceScopeChecks(opts)...)
}

// evidenceScopeChecks runs the validation checks shared by every generated and
// planner-imported task: evidence, scope, reject-if criteria, churn-phrase
// rejection, and the missing-cwd check. Order is significant — callers consume
// reasons[0] as the first-failure identity.
func evidenceScopeChecks(opts AddOptions) []error {
	var failures []error
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

// ValidateBody rejects task bodies that are empty, non-actionable placeholders,
// or physical/personal-service requests rather than software work.
func ValidateBody(body string) error {
	normalized := normalizeBody(body)
	if normalized == "" {
		return invalid("empty body")
	}
	cfg := validationCfg()
	for _, phrase := range cfg.ExactBodies {
		if normalized == phrase {
			return invalid("not actionable software work")
		}
	}
	checks := []struct {
		phrases []string
		reason  string
	}{
		{cfg.AutonomyPhrases, "requires human-in-the-loop decision"},
		{cfg.VagueSoftware, "vague or non-actionable software work"},
		{cfg.BodyPhrases, "physical or personal-service request"},
	}
	for _, check := range checks {
		for _, phrase := range check.phrases {
			if strings.Contains(normalized, phrase) {
				return invalid(check.reason)
			}
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

// IsGeneratedCandidate reports whether a task with the given source/tags is a
// machine-generated discovery candidate (source "task-discovery", or a
// candidate/needs-validation/discovery tag).
func IsGeneratedCandidate(source string, tags []string) bool {
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
	for _, phrase := range validationCfg().GeneratedPhrases {
		if strings.Contains(normalized, phrase) {
			return phrase
		}
	}
	return ""
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
