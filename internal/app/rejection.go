package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	f, err := os.OpenFile(sidecarPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("record rejection: open %s: %w", sidecarPath, err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("record rejection: write: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("record rejection: close: %w", err)
	}
	return nil
}

// ErrRejectionIndexOutOfRange reports an index outside the bounds of the
// current sidecar contents. Indices are 0-based at this layer; the CLI
// converts from 1-based user input.
var ErrRejectionIndexOutOfRange = errors.New("rejection index out of range")

// ErrSidecarDisabled reports that the Service was constructed without a
// sidecar path, so rejection listing/replay is unavailable.
var ErrSidecarDisabled = errors.New("rejection sidecar disabled")

// ReadRejections returns every record currently in the sidecar file in the
// order it was written. Missing file returns (nil, nil) — that is a valid
// empty state. Malformed lines are skipped (best-effort; the sidecar is a
// debugging aid, not a transactional log).
func ReadRejections(path string) ([]RejectionRecord, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read rejection sidecar: %w", err)
	}
	var records []RejectionRecord
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec RejectionRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

// RemoveRejectionAt removes the record at idx (0-based) and rewrites the
// sidecar atomically. Returns the removed record.
func RemoveRejectionAt(path string, idx int) (RejectionRecord, error) {
	records, err := ReadRejections(path)
	if err != nil {
		return RejectionRecord{}, err
	}
	if idx < 0 || idx >= len(records) {
		return RejectionRecord{}, ErrRejectionIndexOutOfRange
	}
	removed := records[idx]
	remaining := append(records[:idx:idx], records[idx+1:]...)
	if err := writeRejections(path, remaining); err != nil {
		return RejectionRecord{}, err
	}
	return removed, nil
}

// writeRejections rewrites the sidecar via temp file + rename. Empty slice
// truncates to an empty file (preserves the path so subsequent appends still
// land in the expected location).
func writeRejections(path string, records []RejectionRecord) error {
	if path == "" {
		return ErrSidecarDisabled
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, rec := range records {
		if err := enc.Encode(rec); err != nil {
			return fmt.Errorf("encode rejection record: %w", err)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rejected-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp sidecar: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp sidecar: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp sidecar: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename temp sidecar: %w", err)
	}
	return nil
}
