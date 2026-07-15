package app

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func TestParseGoalTokensUsesLastStdoutThenStderr(t *testing.T) {
	t.Parallel()
	pattern := `tokens=(\d+)`
	tokens, ok := parseGoalTokens(pattern, []byte("tokens=1\ntokens=23\n"), []byte("tokens=99\n"))
	if !ok || tokens != 23 {
		t.Fatalf("parseGoalTokens stdout = (%d, %t), want (23, true)", tokens, ok)
	}
	tokens, ok = parseGoalTokens(pattern, []byte("tokens=overflow999999999999999999999999"), []byte("tokens=41\n"))
	if !ok || tokens != 41 {
		t.Fatalf("parseGoalTokens stderr fallback = (%d, %t), want (41, true)", tokens, ok)
	}
	_, ok = parseGoalTokens(pattern, []byte("tokens=999999999999999999999999"), nil)
	if ok {
		t.Fatal("overflowing usage must be unavailable")
	}
}

func TestTailWriterForwardsAllAndRetainsBoundedTail(t *testing.T) {
	t.Parallel()
	var forwarded bytes.Buffer
	w := newTailWriter(&forwarded, 8)
	input := "prefix-tokens=42"
	n, err := w.Write([]byte(input))
	if err != nil || n != len(input) {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, len(input))
	}
	if forwarded.String() != input {
		t.Fatalf("forwarded = %q, want %q", forwarded.String(), input)
	}
	if got := string(w.Bytes()); got != "okens=42" {
		t.Fatalf("tail = %q, want %q", got, "okens=42")
	}

	large := newTailWriter(nil, goalOutputTailLimit)
	payload := strings.Repeat("x", goalOutputTailLimit) + "tokens=" + strconv.Itoa(77)
	if _, err := large.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if len(large.Bytes()) != goalOutputTailLimit {
		t.Fatalf("tail length = %d, want %d", len(large.Bytes()), goalOutputTailLimit)
	}
	if tokens, ok := parseGoalTokens(`tokens=(\d+)`, large.Bytes(), nil); !ok || tokens != 77 {
		t.Fatalf("large tail parse = (%d, %t), want (77, true)", tokens, ok)
	}
}
