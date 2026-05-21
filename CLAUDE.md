# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Workspace-level Go rules live in `/path/to/project/go/src/CLAUDE.md` (library choices, code style, lint thresholds, release workflow). This file only covers what is specific to `afk`.

## What this is

`afk` is a local SQLite-backed task queue for coding agents and shell workflows. A task has a lifecycle (`pending → working → done/failed`), optional dependencies (`--blocked-by`/`--after`), manual blocks, resource locks, and leases with heartbeat/recovery. A built-in worker loop (`afk run`) claims ready tasks and shells out a templated command per task.

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

Run a single package's tests: `go test ./internal/runner/ -run TestRun_ -race -count=1`.

No Makefile. The committed `afk` binary at the repo root is a build artifact, not a script.

## Architecture

### Command tree (cobra)

Registered in `internal/commands/root.go` (`func NewRoot`). Categories:

- **Inspection**: `ls`, `show`, `next`, `ready`, `why`, `explain`
- **Scheduling**: `deps {add,rm,ls}`, `block`, `unblock`
- **Lifecycle**: `add`, `done`, `fail`, `reset`, `retry`, `edit`, `rm`, `prune`
- **Worker**: `pop`, `run`, `heartbeat`, `requeue-stale`
- **Meta**: `prompt`, `doctor`
- **Web**: `serve`

Global persistent flag `--queue <path>` (also `AFK_QUEUE` env var). Resolution order: flag → env → `~/.claude/queue/tasks.sqlite`.

### Package layout

```
cmd/afk/                    main() → run(ctx); signal.NotifyContext; builds Deps and calls commands.NewRoot
internal/app/               Service (use cases) over the Store interface; ExplainData, ReadinessData, NotReadyReason
  service_readiness.go      why / not-ready reasons feature
  service_helpers.go        unexported leaf helpers
internal/commands/          Thin cobra wrappers; Deps{} passed by pointer
  add_options.go            input/option-building helpers for 'afk add'
internal/output/            Human and JSON/JSONL rendering
internal/prompt/            Generates Claude Code /loop instruction Markdown
internal/runner/            Worker loop: claim → exec template → heartbeat → mark failed if subprocess exits while working
internal/store/             SQLite persistence; Paths, ResolvePaths, NewSQLite; schema DDL inline
  sqlite_schema.go          schema DDL, migration, busy-retry helpers
  sqlite_scan.go            row scan/encode helpers
internal/server/            HTTP dashboard server; routes, handlers, and go:embed'd web/index.html SPA
internal/task/              Domain types (Task, Status, Event, Attempt, Dependency, Block); no I/O
```

The `Store` interface is defined in `internal/app/store.go` (package `app`, not package `store`), and `internal/store/sqlite.go` provides the concrete `SQLiteStore` implementation, verified by a compile-time `var _ Store = (*store.SQLiteStore)(nil)` assertion in `internal/app/store.go`.

### Storage

modernc.org/sqlite (pure Go — no CGO). Six tables created idempotently at `NewSQLite()` open: `tasks`, `metadata`, `task_events`, `task_attempts`, `task_dependencies`, `task_blocks`. Schema lives inline in `internal/store/sqlite_schema.go` (the `CREATE TABLE` statements). No goose / no migration framework.

SQLite is the only queue backend. A `--queue`/`AFK_QUEUE` path with a non-`.sqlite` extension (including a stale `.jsonl` path) is normalized to a sibling `.sqlite` database; it is never read as JSONL.

## Non-obvious gotchas

- **`afk prompt` does not open the DB** unless `--task <id>` is given. Controlled by the `skipStoreInit` cobra annotation helper in `internal/commands/root.go` (`func skipStoreInit`). Don't accidentally remove that — it's what lets `/loop` call `afk prompt` cheaply.
- **`afk run --workers > 1` is rejected** by the `Workers != 1` guard in `internal/runner/runner.go`. The field is reserved for a future parallel implementation; do not silently allow N>1.
- **Runner exec template vars**: `{{id}}`, `{{cwd}}`, `{{body}}`, `{{queue}}`. The runner also sets `AFK_QUEUE`, `AFK_TASK_ID`, `AFK_TASK_BODY`, and `AFK_TASK_CWD` in the subprocess environment — `AFK_QUEUE` so nested `afk` calls share the queue; the `AFK_TASK_*` vars so worker commands can read task fields from the environment without parsing template placeholders.
- **Worker contract**: the command run by `afk run` must explicitly call `afk done <id>` or `afk fail <id>`. If the subprocess exits while the task is still `working`, the runner marks it failed.
- **Readiness has one authority**: `store.Ready` (SQL) is the single source of truth for whether a task is ready. `ready`, `next`, `pop`, and `run` consult it directly; `why` consults it for the ready/not-ready verdict and additionally derives human-readable *reasons* via `app.notReadyReasons` — the reasons layer explains a "not ready" verdict, it does not decide it. Change the readiness predicate only in `store.Ready`.
- **No viper.** Queue path comes from flag/env/default — there is no config file layer. Don't add one without a reason.
- **`afk serve` binds `127.0.0.1` by default** (loopback only — task bodies may be sensitive); supplying a non-loopback `--addr` prints a warning to stderr. The front-end is a single `go:embed`'d `web/index.html` (no build step required); opens a browser tab by default (`--open=false` to suppress).

## Conventions

- `cmd/afk/main.go` stays small (~37 lines): delegates to `run(ctx context.Context) error`, lets `defer` fire before `os.Exit`. Don't grow it.
- `internal/commands/*` are thin cobra wrappers; business logic belongs in `internal/app/` or `internal/runner/`.
- Domain types in `internal/task/` have no I/O — keep it that way.
- Linting matches workspace defaults (`.golangci.yml` v2, `default: none` with explicit enables). Test files are exempt from `dupl`, `goconst`, `funlen`, `gocognit`, `gosec`.
