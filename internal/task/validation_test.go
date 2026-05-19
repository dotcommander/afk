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

func TestValidateAddOptionsRequiresEvidenceForGeneratedTasks(t *testing.T) {
	t.Parallel()

	err := task.ValidateAddOptions(task.AddOptions{
		Body:   "[discovery:repo:file] Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Verify with go test ./...",
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)

	err = task.ValidateAddOptions(task.AddOptions{
		Body:   "[discovery:repo:file] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Verify with go test ./...",
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	})
	require.NoError(t, err)
}

func TestValidateAddOptionsRequiresDiscoveryPrefixAndScopeForGeneratedTasks(t *testing.T) {
	t.Parallel()

	err := task.ValidateAddOptions(task.AddOptions{
		Body:   "Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go. Verify with go test ./...",
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)

	err = task.ValidateAddOptions(task.AddOptions{
		Body:   "[discovery:repo:file] Evidence: /tmp/repo/file.go:1. Fix /tmp/repo/file.go. Verify with go test ./...",
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)
}

func TestValidateAddOptionsRejectsChurnPhrasesForGeneratedTasks(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"[discovery:repo:cleanup] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Clean up the file. Verify with go test ./...",
		"[discovery:repo:broad] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Refactor broadly across the package. Verify with go test ./...",
		"[discovery:repo:choice] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Implement X or Y. Verify with go test ./...",
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
