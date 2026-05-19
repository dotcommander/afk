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

var absolutePathRE = regexp.MustCompile(`(?:^|\s)/[^\s]+`)
var discoveryPrefixRE = regexp.MustCompile(`^\[discovery:[a-zA-Z0-9._-]+:[a-zA-Z0-9._-]+\]`)

// ValidateAddOptions checks whether a new task is safe and concrete enough for
// AFK. Discovery-generated tasks get stricter checks because they may be
// auto-enqueued without a human reviewing each body first.
func ValidateAddOptions(opts AddOptions) error {
	if err := ValidateBody(opts.Body); err != nil {
		return err
	}
	if !isGeneratedCandidate(opts.Source, opts.Tags) {
		return nil
	}
	if !discoveryPrefixRE.MatchString(strings.TrimSpace(opts.Body)) {
		return invalid("generated task must start with [discovery:<repo>:<topic>]")
	}
	if !containsFold(opts.Body, "verify") && !containsFold(opts.Body, "verification") {
		return invalid("generated task must include a verification command")
	}
	if !containsFold(opts.Body, "evidence:") {
		return invalid("generated task must include evidence")
	}
	if !containsFold(opts.Body, "scope:") {
		return invalid("generated task must include scope")
	}
	if containsGeneratedChurnPhrase(opts.Body) {
		return invalid("generated task is too vague or churn-prone")
	}
	if opts.CWD == "" && !hasAbsolutePath(opts.Body) {
		return invalid("generated task must include cwd metadata or an absolute path")
	}
	return nil
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

func containsGeneratedChurnPhrase(body string) bool {
	normalized := normalizeBody(body)
	for _, phrase := range invalidGeneratedPhrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
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
