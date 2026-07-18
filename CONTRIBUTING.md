# Contributing to afk

Thanks for your interest in `afk`, a local SQLite task queue for coding agents.

## Development

Requires Go 1.26+.

```sh
go build ./...
go vet ./...
go test -race ./...
```

A change is ready to send when all three pass.

## Layout

- `cmd/afk/main.go` — thin entry point; keep it small.
- `internal/commands/` — thin CLI wrappers over the service layer.
- `internal/app/` — business logic (the `Service` use cases over the `Store` interface).
- `internal/store/` — SQLite persistence (`SQLiteStore` implements `app.Store`).
- `internal/task/` — domain types; no I/O.
- `docs/` — user-facing documentation.

## Style

Follow standard Go style and the workspace conventions: `log/slog` for logging, `fmt.Errorf` with `%w` for wrapping, `errors.Join` for aggregation, Kong for the CLI. Files over ~300 lines are a tripwire — extract before growing further. Every DB and HTTP call propagates the caller's `ctx`; do not reach for `context.Background()` inside domain code.

## Tests

- `t.Parallel()` on all tests and subtests.
- Unit tests under 1s; gate slow or integration tests behind `testing.Short()`.
- Prefer `testing/synctest` over `time.Sleep` in time-dependent tests.

## Submitting changes

1. For non-trivial work, open an issue first to align on scope.
2. Branch from `main`.
3. Keep commits focused with `type(scope): description` subjects (feat, fix, refactor, docs, test, chore).
4. Open a pull request against `main`.

Security-sensitive reports: see [SECURITY.md](SECURITY.md).
