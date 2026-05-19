# afk

CLI for CRUD operations on `~/.claude/queue/tasks.sqlite` — the SQLite queue consumed by the `/loop`-driven background task worker.

On first use, `afk` imports existing tasks from `~/.claude/queue/tasks.jsonl` into SQLite when the database is empty. JSONL remains an import format only; new writes go to SQLite.

## Use with Claude Code

`afk` is built to pair with Claude Code's `/loop` skill. Queue work from anywhere, then let Claude drain the queue on an interval:

```
/loop 15m "run ! afk prompt"
```

Every 15 minutes Claude runs `afk prompt`, which emits the current queue as instructions, and works the next pending task. Add work from any shell session:

```sh
afk add "refactor internal/store atomic claim into its own type"
afk add --tag repo:afk --priority high "write benchmark for pop under contention"
afk add --source roadmap.md "next roadmap checklist item"
```

While you're away from the keyboard, the loop drains the queue. When you come back:

```sh
afk count                # see how many tasks ran / failed / remain
afk ls --status done     # review what got done
afk ls --status failed   # see what needs your attention
afk explain 42           # full ledger for a task: events + attempts
```

To focus the worker on a single task:

```
/loop 10m "run ! afk prompt --task 42"
```

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

`afk prompt` uses the same queue path resolution as other commands, but it does not open or create the SQLite database. Pipe it into Claude via `/loop 15m "run ! afk prompt"` — no intermediate file needed.

## Architecture

- `cmd/afk`: process setup and signal-aware command execution.
- `internal/commands`: Cobra commands, argument parsing, and presentation wiring.
- `internal/app`: task use cases such as add, list, pop, done, fail, reset, and prune.
- `internal/store`: persistence boundary; the SQLite implementation owns atomic claim/update operations and one-time JSONL import.
- `internal/task`: persisted task schema, statuses, and state transitions.
- `internal/output`: human and JSON/JSONL CLI rendering.
- `internal/prompt`: generated loop instruction Markdown.

## Examples

End-to-end queue lifecycle from the shell:

```sh
afk add "summarize the open PRs on dotcommander/afk"
afk add --tag review --priority high "review the new sqlite store tests"
afk count
# pending=2  working=0  done=0  failed=0

afk next                       # peek at what `afk pop` would claim
afk pop --lease 30m            # claim the first pending task (JSON)
# ... do the work ...
afk done 1                     # mark it done
afk fail 2 "needs more context" # or fail with a reason
afk retry 2                    # return a failed task to pending
```

Inspect a task in full detail:

```sh
afk explain 1 --json | jq .
```

Housekeeping:

```sh
afk requeue-stale --older-than 2h   # reset stuck working tasks
afk prune --status done,failed      # clear terminal tasks
afk doctor                          # health check
```

## Install

```sh
go build -o afk ./cmd/afk
ln -sf "$(pwd)/afk" ~/go/bin/afk
```
