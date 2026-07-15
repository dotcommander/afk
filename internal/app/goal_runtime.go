package app

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strconv"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
)

const goalOutputTailLimit = 1 << 20

func (s *Service) prepareGoalInvocation(ctx context.Context, member *task.Task) (task.GoalGroup, int64, bool, bool, error) {
	if member.GroupID == "" {
		return task.GoalGroup{}, 0, false, false, nil
	}
	goal, attemptID, limited, err := s.store.PrepareGoalInvocation(ctx, member.ID, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return task.GoalGroup{}, 0, false, false, nil
	}
	if err != nil {
		return task.GoalGroup{}, 0, false, false, err
	}
	return goal, attemptID, true, limited, nil
}

type tailWriter struct {
	dst   io.Writer
	limit int
	buf   []byte
}

func newTailWriter(dst io.Writer, limit int) *tailWriter {
	return &tailWriter{dst: dst, limit: limit, buf: make([]byte, 0, limit)}
}

func (w *tailWriter) Write(p []byte) (int, error) {
	n := len(p)
	var err error
	if w.dst != nil {
		n, err = w.dst.Write(p)
	}
	w.append(p[:n])
	return n, err
}

func (w *tailWriter) append(p []byte) {
	if len(p) >= w.limit {
		w.buf = append(w.buf[:0], p[len(p)-w.limit:]...)
		return
	}
	overflow := len(w.buf) + len(p) - w.limit
	if overflow > 0 {
		copy(w.buf, w.buf[overflow:])
		w.buf = w.buf[:len(w.buf)-overflow]
	}
	w.buf = append(w.buf, p...)
}

func (w *tailWriter) Bytes() []byte { return w.buf }

func parseGoalTokens(pattern string, stdout, stderr []byte) (int64, bool) {
	re, err := regexp.Compile(pattern)
	if err != nil || re.NumSubexp() != 1 {
		return 0, false
	}
	if tokens, ok := lastParseableTokenMatch(re, stdout); ok {
		return tokens, true
	}
	return lastParseableTokenMatch(re, stderr)
}

func lastParseableTokenMatch(re *regexp.Regexp, output []byte) (int64, bool) {
	matches := re.FindAllSubmatch(output, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		value, err := strconv.ParseInt(string(matches[i][1]), 10, 64)
		if err == nil && value >= 0 {
			return value, true
		}
	}
	return 0, false
}
