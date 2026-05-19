// Package queue manages a JSONL-backed task queue for the afk CLI.
package queue

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	defaultRelPath  = ".claude/queue/tasks.jsonl"
	scannerBufBytes = 1 << 20 // 1 MB max line
)

// DefaultPath returns the queue path the /qadd producer writes to.
// Resolved as $HOME/.claude/queue/tasks.jsonl.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("queue: resolve home dir: %w", err)
	}
	return filepath.Join(home, defaultRelPath), nil
}

// Queue owns a single JSONL file. Zero value is not usable — construct via New.
type Queue struct {
	path string
}

// New constructs a Queue for the given file path.
func New(path string) *Queue { return &Queue{path: path} }

// Path returns the queue file path.
func (q *Queue) Path() string { return q.path }

// Load reads and parses the entire file. Returns nil, nil if file is absent.
// Skips blank lines. Wraps parse errors with the offending line number.
//
// ctx accepted for future cancellation; current implementation is synchronous.
func (q *Queue) Load(ctx context.Context) ([]Task, error) {
	_ = ctx
	f, err := os.Open(q.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("queue: open %s: %w", q.path, err)
	}
	defer f.Close() //nolint:errcheck // read-only file; close error on read path is not actionable

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), scannerBufBytes)

	var tasks []Task
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Bytes()
		if len(trim(raw)) == 0 {
			continue
		}
		var t Task
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("queue: parse line %d: %w", lineNo, err)
		}
		tasks = append(tasks, t)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("queue: scan %s: %w", q.path, err)
	}
	return tasks, nil
}

// Save atomically rewrites the file with the given tasks.
// Creates parent directory if missing. Empty slice writes an empty file.
//
// ctx accepted for future cancellation; current implementation is synchronous.
func (q *Queue) Save(ctx context.Context, tasks []Task) error {
	_ = ctx
	dir := filepath.Dir(q.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("queue: mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".tasks-*.jsonl.tmp")
	if err != nil {
		return fmt.Errorf("queue: create temp: %w", err)
	}
	tmpPath := tmp.Name()

	// On any error after creation, attempt cleanup.
	defer func() {
		_ = os.Remove(tmpPath) // no-op if rename succeeded
	}()

	if err := writeTasks(tmp, tasks); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("queue: sync %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("queue: close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, q.path); err != nil {
		return fmt.Errorf("queue: rename %s -> %s: %w", tmpPath, q.path, err)
	}
	return nil
}

func writeTasks(w io.Writer, tasks []Task) error {
	for i, t := range tasks {
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("queue: marshal task %d (id=%s): %w", i, t.ID, err)
		}
		if _, err := w.Write(b); err != nil {
			return fmt.Errorf("queue: write: %w", err)
		}
		if _, err := w.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("queue: write newline: %w", err)
		}
	}
	return nil
}

// trim returns b with leading/trailing ASCII whitespace removed (no UTF-8 ops needed for blank-line check).
func trim(b []byte) []byte {
	start := 0
	end := len(b)
	for start < end && isSpace(b[start]) {
		start++
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
