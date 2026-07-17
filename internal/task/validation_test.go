package task_test

import (
	"errors"
	"testing"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestValidateBodyRejectsInvalidTasks(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"",
		"pick my nose",
		"continue this",
		"make it better",
	} {
		body := body
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			err := task.ValidateBody(body)
			require.Error(t, err)
			require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)
		})
	}
}

func TestValidateBodyRejectsHumanInTheLoopTasks(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"HITL GATE FIRST: post this question to the user via AskUserQuestion before any code.",
		"WAIT for user answer before proceeding with implementation.",
		"This candidate is HITL-dependent and cannot run unattended.",
	} {
		body := body
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			err := task.ValidateBody(body)
			require.Error(t, err)
			require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)
			require.Contains(t, err.Error(), "human-in-the-loop")
		})
	}
}

func TestValidateBodyRejectsVaguePolishTasks(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"work on polishing the game play",
		"Work on polishing the gameplay.",
	} {
		body := body
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			err := task.ValidateBody(body)
			require.Error(t, err)
			require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)
			require.Contains(t, err.Error(), "vague or non-actionable")
		})
	}
}

func TestValidateAddOptionsRequiresEvidenceForGeneratedTasks(t *testing.T) {
	t.Parallel()

	err := task.ValidateAddOptions(task.AddOptions{
		Body:   "[discovery:repo:file] Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches",
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)

	err = task.ValidateAddOptions(task.AddOptions{
		Body:   "[discovery:repo:file] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches",
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	})
	require.NoError(t, err)
}

func TestValidateAddOptionsRejectsInvalidPriority(t *testing.T) {
	t.Parallel()

	err := task.ValidateAddOptions(task.AddOptions{
		Body:     "valid software task",
		Priority: "hihg",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidPriority), "got %v", err)
	require.Contains(t, err.Error(), "hihg")

	for _, priority := range []string{"", "urgent", "high", "normal", "low", " HIGH "} {
		priority := priority
		t.Run(priority, func(t *testing.T) {
			t.Parallel()
			err := task.ValidateAddOptions(task.AddOptions{
				Body:     "valid software task",
				Priority: task.Priority(priority),
			})
			require.NoError(t, err)
		})
	}
}

func TestValidateAddOptionsCanonicalAvailableAt(t *testing.T) {
	t.Parallel()
	canonical, err := task.CanonicalAvailableAt("2026-07-17T12:00:00-04:00")
	require.NoError(t, err)
	require.Equal(t, "2026-07-17T16:00:00Z", canonical)
	require.NoError(t, task.ValidateAddOptions(task.AddOptions{Body: "valid software task", AvailableAt: canonical}))
	err = task.ValidateAddOptions(task.AddOptions{Body: "valid software task", AvailableAt: "tomorrow"})
	require.ErrorIs(t, err, task.ErrInvalidAvailableAt)
}

func TestValidateAddOptionsRequiresDiscoveryPrefixAndScopeForGeneratedTasks(t *testing.T) {
	t.Parallel()

	err := task.ValidateAddOptions(task.AddOptions{
		Body:   "Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches",
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)

	err = task.ValidateAddOptions(task.AddOptions{
		Body:   "[discovery:repo:file] Evidence: /tmp/repo/file.go:1. Fix /tmp/repo/file.go. Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches",
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)
}

func TestValidateAddOptionsRejectsChurnPhrasesForGeneratedTasks(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"[discovery:repo:cleanup] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Clean up the file. Success: file is cleaned up. Verify with go test ./... Reject-if: evidence no longer matches",
		"[discovery:repo:broad] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Refactor broadly across the package. Success: package is refactored. Verify with go test ./... Reject-if: evidence no longer matches",
		"[discovery:repo:choice] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Implement X or Y. Success: one option is implemented. Verify with go test ./... Reject-if: evidence no longer matches",
	} {
		body := body
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			err := task.ValidateAddOptions(task.AddOptions{
				Body:   body,
				Source: "task-discovery",
				CWD:    "/tmp/repo",
			})
			require.Error(t, err)
			require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)
		})
	}
}

func TestValidateAddOptionsGeneratedCandidateTags(t *testing.T) {
	t.Parallel()

	for _, tag := range []string{"candidate", "needs-validation", "discovery"} {
		tag := tag
		t.Run(tag, func(t *testing.T) {
			t.Parallel()
			err := task.ValidateAddOptions(task.AddOptions{
				Body: "[discovery:repo:file] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches",
				Tags: []string{" " + tag + " "},
			})
			require.NoError(t, err)
		})
	}
}

func TestValidateAddOptionsNamedErrors(t *testing.T) {
	t.Parallel()

	const validBody = "[discovery:repo:file] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches"

	cases := []struct {
		name     string
		opts     task.AddOptions
		target   error
		contains string
	}{
		{
			name: "missing discovery prefix",
			opts: task.AddOptions{
				Body:   "Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches",
				Source: "task-discovery",
				CWD:    "/tmp/repo",
			},
			target:   task.ErrMissingDiscoveryPrefix,
			contains: "must start with [discovery:",
		},
		{
			name: "missing verify",
			opts: task.AddOptions{
				Body:   "[discovery:repo:file] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Success: focused issue is fixed.",
				Source: "task-discovery",
				CWD:    "/tmp/repo",
			},
			target:   task.ErrMissingVerify,
			contains: "verification command",
		},
		{
			name: "missing success",
			opts: task.AddOptions{
				Body:   "[discovery:repo:file] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Verify with go test ./... Reject-if: evidence no longer matches",
				Source: "task-discovery",
				CWD:    "/tmp/repo",
			},
			target:   task.ErrMissingSuccess,
			contains: "success criteria",
		},
		{
			name: "missing evidence",
			opts: task.AddOptions{
				Body:   "[discovery:repo:file] Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches",
				Source: "task-discovery",
				CWD:    "/tmp/repo",
			},
			target:   task.ErrMissingEvidence,
			contains: "must include evidence",
		},
		{
			name: "missing scope",
			opts: task.AddOptions{
				Body:   "[discovery:repo:file] Evidence: /tmp/repo/file.go:1. Fix /tmp/repo/file.go. Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches",
				Source: "task-discovery",
				CWD:    "/tmp/repo",
			},
			target:   task.ErrMissingScope,
			contains: "must include scope",
		},
		{
			name: "missing cwd",
			opts: task.AddOptions{
				Body:   "[discovery:repo:file] Evidence: file.go:1. Scope: file.go. Fix file.go. Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches",
				Source: "task-discovery",
			},
			target:   task.ErrMissingCwd,
			contains: "cwd metadata or an absolute path",
		},
		{
			name: "missing reject-if",
			opts: task.AddOptions{
				Body:   "[discovery:repo:file] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Success: focused issue is fixed. Verify with go test ./...",
				Source: "task-discovery",
				CWD:    "/tmp/repo",
			},
			target:   task.ErrMissingRejectIf,
			contains: "reject-if criteria",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := task.ValidateAddOptions(tc.opts)
			require.Error(t, err)
			require.True(t, errors.Is(err, task.ErrInvalidTask), "errors.Is ErrInvalidTask failed: %v", err)
			require.True(t, errors.Is(err, tc.target), "errors.Is %v failed: %v", tc.target, err)
			require.Contains(t, err.Error(), "invalid task:", "missing prefix: %v", err)
			require.Contains(t, err.Error(), tc.contains, "missing reason substring: %v", err)
		})
	}

	require.NoError(t, task.ValidateAddOptions(task.AddOptions{
		Body:   validBody,
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	}))
}

func TestValidateAddOptionsChurnPhraseError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		body   string
		phrase string
	}{
		{
			name:   "cleanup",
			body:   "[discovery:repo:cleanup] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Clean up the file. Success: file is cleaned up. Verify with go test ./... Reject-if: evidence no longer matches",
			phrase: "clean up",
		},
		{
			name:   "refactor broadly",
			body:   "[discovery:repo:broad] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Refactor broadly across the package. Success: package is refactored. Verify with go test ./... Reject-if: evidence no longer matches",
			phrase: "refactor broadly",
		},
		{
			name:   "x or y",
			body:   "[discovery:repo:choice] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Implement X or Y. Success: one option is implemented. Verify with go test ./... Reject-if: evidence no longer matches",
			phrase: "x or y",
		},
		{
			name:   "polish",
			body:   "[discovery:repo:polish] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Polish the UI. Success: UI is polished. Verify with go test ./... Reject-if: evidence no longer matches",
			phrase: "polish",
		},
		{
			name:   "nice to have",
			body:   "[discovery:repo:nice] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Add a nice to have helper. Success: helper exists. Verify with go test ./... Reject-if: evidence no longer matches",
			phrase: "nice to have",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := task.ValidateAddOptions(task.AddOptions{
				Body:   tc.body,
				Source: "task-discovery",
				CWD:    "/tmp/repo",
			})
			require.Error(t, err)
			require.True(t, errors.Is(err, task.ErrInvalidTask), "errors.Is ErrInvalidTask failed: %v", err)

			var churn *task.ChurnPhraseError
			require.True(t, errors.As(err, &churn), "errors.As ChurnPhraseError failed: %v", err)
			require.Equal(t, tc.phrase, churn.Phrase)
			require.Contains(t, err.Error(), "invalid task:", "missing prefix: %v", err)
			require.Contains(t, err.Error(), "churn phrase:", "missing churn label: %v", err)
			require.Contains(t, err.Error(), tc.phrase, "missing matched phrase: %v", err)
		})
	}
}

func TestValidateAddOptionsAllReturnsNilForValidGeneratedBody(t *testing.T) {
	t.Parallel()

	err := task.ValidateAddOptionsAll(task.AddOptions{
		Body:   "[discovery:repo:file] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches",
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	})
	require.NoError(t, err)
}

func TestValidateAddOptionsAllReturnsNilForNonGeneratedBody(t *testing.T) {
	t.Parallel()

	err := task.ValidateAddOptionsAll(task.AddOptions{
		Body: "just a normal task body",
	})
	require.NoError(t, err)
}

func TestValidateAddOptionsAllJoinsEveryFailure(t *testing.T) {
	t.Parallel()

	err := task.ValidateAddOptionsAll(task.AddOptions{
		Body:   "Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go.",
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask))
	require.True(t, errors.Is(err, task.ErrMissingDiscoveryPrefix))
	require.True(t, errors.Is(err, task.ErrMissingSuccess))
	require.True(t, errors.Is(err, task.ErrMissingVerify))
	require.True(t, errors.Is(err, task.ErrMissingEvidence))
	require.True(t, errors.Is(err, task.ErrMissingRejectIf))
	require.False(t, errors.Is(err, task.ErrMissingScope))
	require.False(t, errors.Is(err, task.ErrMissingCwd))

	var joined interface{ Unwrap() []error }
	require.True(t, errors.As(err, &joined))
	require.Len(t, joined.Unwrap(), 5)
}

func TestValidateAddOptionsAllShortCircuitsOnInvalidBody(t *testing.T) {
	t.Parallel()

	err := task.ValidateAddOptionsAll(task.AddOptions{
		Body:   "",
		Source: "task-discovery",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask))

	var joined interface{ Unwrap() []error }
	require.False(t, errors.As(err, &joined))
}

func TestChurnPhraseErrorMatchesInvalidTask(t *testing.T) {
	t.Parallel()

	err := &task.ChurnPhraseError{Phrase: "clean up"}
	require.True(t, errors.Is(err, task.ErrInvalidTask))
	require.False(t, errors.Is(err, task.ErrInvalidPriority))
}
