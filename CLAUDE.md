# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Workspace-level Go rules live in `/path/to/project/go/src/CLAUDE.md` (library choices, code style, lint thresholds, release workflow). This file only covers what is specific to `afk`.

## What this is

`afk` is a local SQLite-backed task queue for coding agents and shell workflows. A task has a lifecycle (`todo → doing → done/failed/deleted`), optional dependencies (`--blocked-by`), resource locks, and leases. Process supervision is external: workers use `afk take`, execute the task, then `afk set`.

`afk prompt` emits Markdown that Claude Code's `/loop` re-runs on an interval — that is the intended pattern for driving an agent off the queue. See `README.md`.

Module path: `github.com/dotcommander/afk`. Go 1.26.

## Build, test, install

```sh
go build -o afk ./cmd/afk
mkdir -p ~/go/bin
rm -f ~/go/bin/afk
install -m 0755 afk ~/go/bin/afk
```

Verification (per workspace rules — pipe long output through `tail`):

```sh
go build ./...
go test ./... | tail -50
go vet ./...
golangci-lint run ./... | tail -50
```

Run a focused package test when needed, for example `go test ./internal/commands -run TestPrompt -count=1`.

No Makefile. The committed `afk` binary at the repo root is a build artifact, not a script.

## Architecture

### Command tree (cobra)

Registered in `internal/commands/root.go` (`func NewRoot`). Categories:

- **Inspection**: `tasks`, `task`, `status`, `find`, `snapshot`
- **Scheduling**: `add --blocked-by`, `take --dry-run`
- **Lifecycle**: `add`, `set`
- **Worker**: `take`, hidden `heartbeat`, hidden `requeue-stale`
- **Meta**: `prompt`
- **Web**: `serve`

Global persistent flag `--queue <path>` (also `AFK_QUEUE` env var). Resolution order: flag → env → `~/.claude/queue/tasks.sqlite`.

### Package layout

```
cmd/afk/                    main() → run(ctx); signal.NotifyContext; builds Deps and calls commands.NewRoot
internal/app/               Service (use cases) over the Store interface; ExplainData
  service_helpers.go        unexported leaf helpers
internal/commands/          Thin cobra wrappers; Deps{} passed by pointer
  add_options.go            input/option-building helpers for 'afk add'
internal/output/            Human and JSON/JSONL rendering
internal/prompt/            Generates Claude Code /loop instruction Markdown
internal/runner/            Legacy internal runner package; keep only for compatibility/internal tests
internal/store/             SQLite persistence; Paths, ResolvePaths, NewSQLite; schema DDL inline
  sqlite_schema.go          schema DDL, migration, busy-retry helpers
  sqlite_scan.go            row scan/encode helpers
internal/server/            HTTP dashboard server; routes, handlers, and go:embed'd web/index.html SPA
internal/task/              Domain types (Task, Status, Event, Attempt, Dependency, Block); no I/O
```

The `Store` interface is defined in `internal/app/store.go` (package `app`, not package `store`), and `internal/store/sqlite.go` provides the concrete `SQLiteStore` implementation, verified by a compile-time `var _ Store = (*store.SQLiteStore)(nil)` assertion in `internal/app/store.go`.

### Storage

modernc.org/sqlite (pure Go — no CGO). Five tables created idempotently at `NewSQLite()` open: `tasks`, `metadata`, `task_events`, `task_attempts`, `task_dependencies`. Schema lives inline in `internal/store/sqlite_schema.go` (the `CREATE TABLE` statements). No goose / no migration framework.

SQLite is the only queue backend. A `--queue`/`AFK_QUEUE` path with a non-`.sqlite` extension (including a stale `.jsonl` path) is normalized to a sibling `.sqlite` database; it is never read as JSONL.

## Non-obvious gotchas

- **`afk prompt` does not open the DB** unless `--task <id>` is given. Controlled by the `skipStoreInit` cobra annotation helper in `internal/commands/root.go` (`func skipStoreInit`). Don't accidentally remove that — it's what lets `/loop` call `afk prompt` cheaply.
- **`afk run` is not public.** Use `afk take --dry-run` for readiness previews and external loops for execution.
- **Worker contract**: preview with `afk take --dry-run --json --full` when triaging; claim with `afk take`, then explicitly finish with `afk set <id> done --note <evidence>` or `afk set <id> failed --note <reason>`. Use `--summary` when a receipt should include queue counts.
- **Targeted retry**: inspect the task, then use `afk retry <id> --reason "<reason>"` or `afk set <id> doing --note "retrying: <reason>"` to open a fresh attempt before doing retry work.
- **Readiness has one authority**: `store.Ready` (SQL) is the single source of truth for whether a task is ready. `take` and `take --dry-run` consult it directly. Change the readiness predicate only in `store.Ready`.
- **No viper.** Queue path comes from flag/env/default — there is no config file layer. Don't add one without a reason.
- **`afk serve` binds `127.0.0.1` by default** (loopback only — task bodies may be sensitive); supplying a non-loopback `--addr` prints a warning to stderr. The front-end is a single `go:embed`'d `web/index.html` (no build step required); opens a browser tab by default (`--open=false` to suppress).

## Conventions

- `cmd/afk/main.go` stays small (~37 lines): delegates to `run(ctx context.Context) error`, lets `defer` fire before `os.Exit`. Don't grow it.
- `internal/commands/*` are thin cobra wrappers; business logic belongs in `internal/app/`. Do not add new public behavior through the legacy `internal/runner/` package.
- Domain types in `internal/task/` have no I/O — keep it that way.
- Linting matches workspace defaults (`.golangci.yml` v2, `default: none` with explicit enables). Test files are exempt from `dupl`, `goconst`, `funlen`, `gocognit`, `gosec`.
