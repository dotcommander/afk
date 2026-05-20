package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestRenderCommand_QuotesShellMetacharacters(t *testing.T) {
	t.Parallel()

	body := `say "hello" and ` + "`whoami`" + ` or $(id) and it's done`
	tsk := task.Task{
		ID:   "test-id",
		Body: body,
		CWD:  "/tmp",
	}

	result := renderCommand("echo {{body}}", tsk, "/q")

	expectedQuotedBody := shellQuote(body)
	require.Equal(t, "echo "+expectedQuotedBody, result)

	// Quoted body must start and end with single quote.
	require.True(t, strings.HasPrefix(expectedQuotedBody, "'"), "quoted body must start with single quote")
	require.True(t, strings.HasSuffix(expectedQuotedBody, "'"), "quoted body must end with single quote")

	// Embedded single quote must appear as the 4-char escape sequence.
	require.Contains(t, expectedQuotedBody, `'\''`, "embedded single quote must be escaped as '\\''")

	// The rendered result must not contain an unquoted double quote, backtick, or $(...).
	// Extract the part after "echo " and verify it equals expectedQuotedBody exactly —
	// any shell-active chars are inside the single-quoted span and cannot be interpreted.
	after, found := strings.CutPrefix(result, "echo ")
	require.True(t, found)
	require.Equal(t, expectedQuotedBody, after)
}

func TestFirstHeartbeatErrIgnoresContextErrors(t *testing.T) {
	t.Parallel()

	makeErrs := func(errs ...error) <-chan error {
		ch := make(chan error, len(errs))
		for _, e := range errs {
			ch <- e
		}
		close(ch)
		return ch
	}

	// nil entry is a no-op.
	require.NoError(t, firstHeartbeatErr(makeErrs(nil)))

	// context.Canceled is normal shutdown — not a real error.
	require.NoError(t, firstHeartbeatErr(makeErrs(context.Canceled)))

	// context.DeadlineExceeded is expected when --max-minutes fires — not a real error.
	require.NoError(t, firstHeartbeatErr(makeErrs(context.DeadlineExceeded)))

	// A genuine error must be surfaced.
	genuine := errors.New("db write failed")
	require.ErrorIs(t, firstHeartbeatErr(makeErrs(genuine)), genuine)
}
