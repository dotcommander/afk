package app_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

// BenchmarkServiceStatus measures Service.Status over a ~1k-row queue. The
// pre-SQL implementation scanned every row twice (List + Go iteration); the
// SQL-aggregated path uses Counts + ActiveLists and should win by a large
// margin on the same fixture.
func BenchmarkServiceStatus(b *testing.B) {
	svc := newBenchService(b, 1000)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Status(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkServiceStatusLegacy reproduces the pre-SQL Status path (List +
// Go iteration) on the same fixture so the SQL-aggregated win is visible
// in one go test invocation.
func BenchmarkServiceStatusLegacy(b *testing.B) {
	s, ctx := newBenchStore(b, 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tasks, err := s.List(ctx)
		if err != nil {
			b.Fatal(err)
		}
		counts := make(map[task.Status]int)
		var todo, doing []task.Task
		for _, t := range tasks {
			status := task.NormalizeStatus(t.Status)
			counts[status]++
			switch status {
			case task.StatusTodo:
				todo = append(todo, t)
			case task.StatusDoing:
				doing = append(doing, t)
			}
		}
		_ = counts
		_ = todo
		_ = doing
	}
}

// newBenchStore seeds a fresh store with the same long-tail-of-done shape
// used by Service benchmarks, returning the store and a fresh context.
func newBenchStore(b *testing.B, n int) (*store.SQLiteStore, context.Context) {
	b.Helper()
	s, err := store.NewSQLite(context.Background(), store.Paths{
		SQLitePath: filepath.Join(b.TempDir(), "tasks.sqlite"),
	})
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, s.Close()) })
	svc := app.NewService(s, func() time.Time { return fixed })
	ctx := context.Background()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id, err := svc.Add(ctx, "bench task body sufficient length to look real")
		require.NoError(b, err)
		ids = append(ids, id)
	}
	for i := 0; i < n-10; i++ {
		require.NoError(b, svc.SetStatus(ctx, ids[i], task.StatusDone, ""))
	}
	return s, ctx
}

// BenchmarkServiceCount exercises the per-status tally path.
func BenchmarkServiceCount(b *testing.B) {
	svc := newBenchService(b, 1000)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Count(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// newBenchService seeds a queue with n tasks where the vast majority are
// already done — the realistic operational shape (a long history of done
// rows, a small active head). Status() is dominated by the active head;
// pre-SQL scanned all n rows, SQL aggregation scans only the active head.
func newBenchService(b *testing.B, n int) *app.Service {
	b.Helper()
	s, err := store.NewSQLite(context.Background(), store.Paths{
		SQLitePath: filepath.Join(b.TempDir(), "tasks.sqlite"),
	})
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, s.Close()) })
	svc := app.NewService(s, func() time.Time { return fixed })
	ctx := context.Background()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id, err := svc.Add(ctx, "bench task body sufficient length to look real")
		require.NoError(b, err)
		ids = append(ids, id)
	}
	// Mark all but the last 10 as done so the active head stays small.
	for i := 0; i < n-10; i++ {
		require.NoError(b, svc.SetStatus(ctx, ids[i], task.StatusDone, ""))
	}
	return svc
}
