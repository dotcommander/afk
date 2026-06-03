package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func (s *SQLiteStore) execWithBusyRetry(ctx context.Context, query string, args ...any) error {
	return retrySQLiteBusy(ctx, func(ctx context.Context) error {
		_, err := s.db.ExecContext(ctx, query, args...)
		return err
	})
}

func retrySQLiteBusy(ctx context.Context, fn func(context.Context) error) error {
	var err error
	for {
		err = fn(ctx)
		if !isSQLiteBusy(err) {
			return err
		}
		if waitErr := waitSQLiteBusyRetry(ctx); waitErr != nil {
			return err
		}
	}
}

func waitSQLiteBusyRetry(ctx context.Context) error {
	timer := time.NewTimer(sqliteBusyRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// isDuplicateColumn reports whether err is the "duplicate column name"
// failure raised when an ALTER TABLE ADD COLUMN re-applies a column that a
// prior migration already added. Mirrors the isSQLiteBusy idiom: match a
// typed *sqlite.Error and gate on Code() first. "duplicate column name" maps
// to the generic SQLITE_ERROR result code (no distinct extended code), so the
// message substring check runs only as a last resort against the typed error.
func isDuplicateColumn(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	if se.Code() != sqlite3.SQLITE_ERROR {
		return false
	}
	return strings.Contains(se.Error(), "duplicate column name")
}

// isSQLiteBusy reports whether err is a SQLite contention error that the
// retry loop should back off on. Uses errors.As against *sqlite.Error so the
// check is robust to wrapped errors and message-string locale drift across
// driver versions.
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	switch se.Code() {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	default:
		return false
	}
}
