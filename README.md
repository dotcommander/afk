# afk

CLI for CRUD operations on `~/.claude/queue/tasks.sqlite` — the SQLite queue consumed by the `/loop`-driven background task worker.

On first use, `afk` imports existing tasks from `~/.claude/queue/tasks.jsonl` into SQLite when the database is empty. JSONL remains an import format only; new writes go to SQLite.

## Commands

| Command | Behavior |
|---|---|
| `afk add <body>` | Append a new pending task; prints the new id. |
| `afk ls [--status STATUS] [--json]` | List tasks, optionally filtered. |
| `afk show <id> [--json]` | Show one task. |
| `afk count` | Tally tasks per status. |
| `afk next` | Show the task `afk pop` would claim (no mutation). |
| `afk explain <id> [--json]` | Show task metadata, events, and attempts. |
| `afk prompt [--output PATH]` | Generate loop instruction Markdown for the current queue configuration. |
| `afk prompt --task <id>` | Generate a focused prompt for one queued task. |
| `afk edit <id> <new-body>` | Update the body of a task. |
| `afk done <id>` | Mark task as done. |
| `afk fail <id> <reason>` | Mark task as failed with a reason. |
| `afk retry <id>` | Reset a failed task to pending while preserving attempt history. |
| `afk reset <id>` | Set status back to pending; clear started/finished/error. |
| `afk rm <id>` | Remove a task. |
| `afk prune [--status LIST]` | Remove tasks by status (default: done,failed). |
| `afk pop [--lease DURATION]` | Atomically claim the first pending task and print it as JSON. |
| `afk requeue-stale [--older-than DURATION]` | Reset stale working tasks to pending. |
| `afk doctor` | Check queue health and installation basics. |

## Configuration

Queue database path resolution order:
1. `--queue <path>` flag
2. `AFK_QUEUE` env var
3. Default: `~/.claude/queue/tasks.sqlite`

For migration compatibility, a path ending in `.jsonl` is treated as the legacy import path and stored next to a `.sqlite` database with the same basename. For example, `--queue /tmp/tasks.jsonl` writes to `/tmp/tasks.sqlite` and imports `/tmp/tasks.jsonl` once.

## Task metadata

`afk add` records the current working directory by default so workers have context for relative paths and underspecified task bodies.

```sh
afk add --tag repo:afk --priority high "add tests for prompt generation"
afk add --cwd /path/to/repo --source roadmap.md "implement the next checklist item"
afk add --no-cwd "context-free task"
```

Supported metadata flags:

- `--tag VALUE` repeatable task tags.
- `--priority VALUE` scheduling/reporting priority metadata.
- `--cwd PATH` working-directory context; defaults to the invocation directory.
- `--no-cwd` disables cwd capture.
- `--source VALUE` origin such as `cli`, `roadmap.md`, `todo-scan`, or `go-test`.
- `--agent VALUE` preferred worker profile.
- `--group VALUE` grouping key for related tasks.
- `--resource VALUE` resource key such as a repo/path lock target.

SQLite also records lifecycle events and attempts for task transitions. The current CLI uses this ledger internally for durability and future reporting/diagnostics.

Leases can be attached to claimed tasks:

```sh
afk pop --lease 30m
afk requeue-stale --older-than 2h
```

Use `afk explain <id>` to inspect task state, metadata, lifecycle events, and attempts. Use `afk retry <id>` to return a failed task to pending without losing its previous attempt history.

## Loop prompt

Generate the `/loop` instruction prompt from the current binary:

```sh
afk prompt
```

Write it directly to a file:

```sh
afk prompt --output ~/.claude/loop.md
```

`afk prompt` uses the same queue path resolution as other commands, but it does not open or create the SQLite database.

## Architecture

- `cmd/afk`: process setup and signal-aware command execution.
- `internal/commands`: Cobra commands, argument parsing, and presentation wiring.
- `internal/app`: task use cases such as add, list, pop, done, fail, reset, and prune.
- `internal/store`: persistence boundary; the SQLite implementation owns atomic claim/update operations and one-time JSONL import.
- `internal/task`: persisted task schema, statuses, and state transitions.
- `internal/output`: human and JSON/JSONL CLI rendering.
- `internal/prompt`: generated loop instruction Markdown.

## Install

```sh
go build -o afk ./cmd/afk
ln -sf "$(pwd)/afk" ~/go/bin/afk
```
