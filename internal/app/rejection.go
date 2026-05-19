package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
)

// RejectionRecord is one line in the rejected.jsonl sidecar.
type RejectionRecord struct {
	Ts     time.Time `json:"ts"`
	Reason string    `json:"reason"`
	Body   string    `json:"body"`
	Tags   []string  `json:"tags,omitempty"`
	Source string    `json:"source,omitempty"`
	CWD    string    `json:"cwd,omitempty"`
	Agent  string    `json:"agent,omitempty"`
	Group  string    `json:"group,omitempty"`
}

// SidecarPath returns the rejected.jsonl path next to the SQLite DB.
func SidecarPath(paths store.Paths) string {
	if paths.SQLitePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(paths.SQLitePath), "rejected.jsonl")
}

// RecordRejection appends one RejectionRecord to sidecarPath as a single
// JSONL line. A single Write under PIPE_BUF (4096 bytes on POSIX) to an
// O_APPEND fd is atomic, so concurrent appenders cannot interleave bytes.
// Records exceeding 4096 bytes are truncated at the Body field to preserve
// atomicity; the truncation is marked with a "...[truncated]" suffix.
func RecordRejection(sidecarPath string, opts task.AddOptions, reason error, now time.Time) error {
	if sidecarPath == "" {
		return fmt.Errorf("record rejection: empty sidecar path")
	}
	rec := RejectionRecord{
		Ts:     now.UTC(),
		Reason: reason.Error(),
		Body:   opts.Body,
		Tags:   opts.Tags,
		Source: opts.Source,
		CWD:    opts.CWD,
		Agent:  opts.Agent,
		Group:  opts.GroupID,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("record rejection: marshal: %w", err)
	}
	line = append(line, '\n')
	if len(line) > 4096 {
		over := len(line) - 4096 + len("...[truncated]")
		if over < len(rec.Body) {
			rec.Body = rec.Body[:len(rec.Body)-over] + "...[truncated]"
		} else {
			rec.Body = "...[truncated]"
		}
		line, err = json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("record rejection: marshal truncated: %w", err)
		}
		line = append(line, '\n')
	}
	if err := os.MkdirAll(filepath.Dir(sidecarPath), 0o750); err != nil {
		return fmt.Errorf("record rejection: mkdir: %w", err)
	}
	f, err := os.OpenFile(sidecarPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("record rejection: open %s: %w", sidecarPath, err)
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("record rejection: write: %w", err)
	}
	return nil
}
