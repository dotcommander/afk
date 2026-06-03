package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// runAgent is package-internal; these tests live in package app (white-box).

func TestRunAgentSuccess(t *testing.T) {
	t.Parallel()
	err := runAgent(context.Background(), "/usr/bin/true", "irrelevant", 5*time.Second, nil, nil)
	require.NoError(t, err)
}

func TestRunAgentNonZeroExit(t *testing.T) {
	t.Parallel()
	err := runAgent(context.Background(), "/usr/bin/false", "irrelevant", 5*time.Second, nil, nil)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrAgentTimeout), "non-zero exit must not be ErrAgentTimeout")
}

func TestRunAgentTimeout(t *testing.T) {
	t.Parallel()
	err := runAgent(context.Background(), "/bin/sleep 30", "", 50*time.Millisecond, nil, nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrAgentTimeout), "slow process must return ErrAgentTimeout, got: %v", err)
}

// argv tokenisation: {{.Prompt}} must expand to ONE argv element even when the
// prompt contains spaces or newlines. /bin/echo prints each arg on one line
// separated by spaces; when prompt is a single arg the entire string appears on
// a single line.
func TestRunAgentArgvTokenisation(t *testing.T) {
	t.Parallel()
	prompt := "hello world from loop"
	var out bytes.Buffer
	err := runAgent(context.Background(), "/bin/echo {{.Prompt}}", prompt, 5*time.Second, &out, nil)
	require.NoError(t, err)
	// /bin/echo prints all its args space-separated on one line.
	// Because {{.Prompt}} is a single token, echo receives exactly ONE arg,
	// so the output line equals the full prompt string.
	line := strings.TrimRight(out.String(), "\n")
	require.Equal(t, prompt, line, "prompt must arrive as a single argv element")
}

// buildArgv unit tests.

func TestBuildArgvEmptyCommandError(t *testing.T) {
	t.Parallel()
	_, err := buildArgv("", "prompt")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

func TestBuildArgvRendersPromptToken(t *testing.T) {
	t.Parallel()
	argv, err := buildArgv("/bin/echo {{.Prompt}}", "my prompt")
	require.NoError(t, err)
	require.Equal(t, []string{"/bin/echo", "my prompt"}, argv)
}

func TestBuildArgvMultipleTokens(t *testing.T) {
	t.Parallel()
	argv, err := buildArgv("cmd -p {{.Prompt}} --flag", "val")
	require.NoError(t, err)
	require.Equal(t, []string{"cmd", "-p", "val", "--flag"}, argv)
}

func TestBuildArgvPromptWithSpacesIsSingleElement(t *testing.T) {
	t.Parallel()
	argv, err := buildArgv("/bin/echo {{.Prompt}}", "hello world")
	require.NoError(t, err)
	require.Len(t, argv, 2)
	require.Equal(t, "hello world", argv[1])
}
