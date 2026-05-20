# afk

`afk` is a local SQLite task queue for coding agents and shell workflows.

```sh
tests=$(afk add "write tests for the scheduler")
afk add --blocked-by "$tests" "update README after tests pass"
afk ready
afk run --dry-run --exec 'afk prompt --task {{id}}'
afk run --exec 'afk prompt --task {{id}}' --limit 1
```

Tasks stay in `pending`, `working`, `done`, or `failed`. Scheduler state such as dependencies, manual blocks, leases, and resource locks is stored separately so the task lifecycle stays simple.

The queue is stored in a single SQLite database. There is no JSONL backend.

For task-shaped docs see [`docs/`](docs/index.md):

- [`docs/getting-started.md`](docs/getting-started.md) — install, queue a task, claim and finish it.
- [`docs/tasks.md`](docs/tasks.md) — add tasks and attach metadata.
- [`docs/scheduling.md`](docs/scheduling.md) — dependencies, blocks, resource locks, readiness.
- [`docs/workers.md`](docs/workers.md) — claim, lease, heartbeat, recover.
- [`docs/runner.md`](docs/runner.md) — the `afk run` worker loop.
- [`docs/configuration.md`](docs/configuration.md) — queue path resolution.
- [`docs/command-reference.md`](docs/command-reference.md) — every subcommand.

## Install

```sh
go build -o afk ./cmd/afk
mkdir -p ~/go/bin
rm -f ~/go/bin/afk
install -m 0755 afk ~/go/bin/afk
```

## Quick Start

Add work:

```sh
afk add "fix the failing queue test"
afk add --tag repo:afk --priority high "review scheduler indexes"
afk add --source roadmap.md "implement the next checklist item"
```

Inspect the queue:

```sh
afk ls
afk count
afk next
afk explain 1
```

Claim and finish one task manually:

```sh
task=$(afk add "fix one small bug")
afk pop --lease 30m --worker codex:1
# do the work
afk done "$task"
```

If work fails, keep the attempt history and return it to the queue later:

```sh
task=$(afk add "call the protected API")
afk pop --lease 30m --worker codex:1
afk fail "$task" "missing credentials"
afk retry "$task"
```

## Task Ordering

Use `--blocked-by` when one task must finish before another task can run.

```sh
schema=$(afk add "add migration")
tests=$(afk add --blocked-by "$schema" "update migration tests")
afk deps ls "$tests"
```

The second task stays `pending`, but it is not ready until the first task is `done`.

```sh
afk ready
afk why "$tests"
```

Dependency commands:

```sh
afk deps add 43 --blocked-by 42
afk deps rm 43 --blocked-by 42
afk deps ls 43 --json
```

Rules:

- `--blocked-by none` records no dependency.
- `--after ID` is an alias for `--blocked-by ID`.
- Missing dependency ids, self-dependencies, and dependency cycles are rejected.
- A failed prerequisite does not auto-fail dependent tasks; `afk why` reports the failed prerequisite.

## Scheduler Controls

Manual blocks pause a task without changing its lifecycle status:

```sh
afk block 43 "waiting on API credentials"
afk why 43
afk unblock 43
```

Resource locks prevent two active workers from touching the same resource at the same time:

```sh
afk add --resource repo:/path/to/project "edit package A"
afk add --resource repo:/path/to/project "edit package B"
```

If one task with that resource is `working`, the next task with the same resource is not ready until the active claim is completed, failed, reset, or its lease expires.

Readiness applies consistently to:

- `afk ready`
- `afk why <id>`
- `afk next`
- `afk pop`
- `afk run`

## Leases And Recovery

Leases make abandoned work recoverable.

```sh
afk pop --lease 30m --worker codex:1
afk heartbeat 42 --worker codex:1 --lease 30m
afk requeue-stale --older-than 2h
```

Behavior:

- `afk pop --lease DURATION` records a claim expiration.
- `afk heartbeat` extends the lease, but only for the worker that owns the active attempt.
- `afk requeue-stale` returns expired or old `working` tasks to `pending`.
- `afk explain <id>` shows lifecycle events and execution attempts.

## Runner Mode

`afk run` is the built-in worker loop. It claims tasks through the same atomic scheduler path as `afk pop`, then runs a user-provided shell command template.

Preview what would run:

```sh
afk run --dry-run --exec 'afk prompt --task {{id}}'
```

Run one ready task:

```sh
afk run --exec 'afk prompt --task {{id}}'
```

Run up to five tasks or stop after 30 minutes:

```sh
afk run --exec 'afk prompt --task {{id}}' --limit 5 --max-minutes 30
```

Runner flags:

| Flag | Default | Behavior |
|---|---:|---|
| `--exec TEMPLATE` | empty | Shell command to run for each claimed task. Required unless `--dry-run` is set. |
| `--dry-run` | `false` | Print runnable tasks and waiting reasons without claiming work. |
| `--limit N` | `1` | Maximum tasks to process. `0` means no limit. |
| `--max-minutes N` | `0` | Stop claiming after this many minutes. `0` means no time limit. |
| `--lease DURATION` | `30m` | Lease duration for each claim and heartbeat. |
| `--worker ID` | `hostname:pid` | Worker id recorded on attempts and heartbeats. |
| `--workers N` | `1` | Reserved for parallel runners. Values above `1` are rejected in this version. |

Template variables:

| Variable | Value |
|---|---|
| `{{id}}` | Task id. |
| `{{cwd}}` | Task working directory metadata. |
| `{{body}}` | Task body. |
| `{{queue}}` | Resolved SQLite queue path. |

The runner also sets the following environment variables for every subprocess:

| Variable | Value |
|---|---|
| `AFK_QUEUE` | Resolved SQLite queue path (for nested `afk` calls). |
| `AFK_TASK_ID` | ID of the task being executed. |
| `AFK_TASK_BODY` | Body text of the task. |
| `AFK_TASK_CWD` | Working directory of the task. |

These complement the template placeholders — worker commands can read task fields from the environment without parsing the `--exec` template.

Important failure behavior: if the command exits while the task is still `working`, `afk run` marks the task failed. Worker commands should call `afk done <id>` or `afk fail <id> <reason>` when they finish the real work.

## Claude Code Loop

`afk prompt` emits queue instructions for Claude Code.

```sh
afk prompt
/loop 15m "run ! afk prompt"
```

To focus the loop on one task:

```sh
/loop 10m "run ! afk prompt --task 42"
```

`afk prompt` uses the same queue path resolution as other commands, but it does not open or create the SQLite database.

## Task Metadata

`afk add` records useful repo context by default so workers have enough metadata without extra flags. It stores the current working directory, sets `source=cli`, and, when run inside a git repo, infers `resource=repo:<git-root>` plus a `repo:<directory-name>` tag.

```sh
afk add "run local tests"
afk add --cwd /path/to/repo "run local tests"
afk add --no-cwd "context-free task"
afk add --resource none "task that does not need a repo lock"
afk add --agent codex --group release-1 "draft release notes"
```

Supported metadata flags:

| Flag | Behavior |
|---|---|
| `--tag VALUE` | Repeatable task tag. Supplying any tag disables the inferred repo tag. |
| `--priority VALUE` | Scheduler priority: `urgent`, `high`, `normal`, or `low`. Unknown values are rejected. |
| `--cwd PATH` | Working-directory context; defaults to the invocation directory. |
| `--no-cwd` | Do not record a working directory. |
| `--source VALUE` | Origin; defaults to `cli`. Examples: `roadmap.md`, `todo-scan`, or `go-test`. |
| `--agent VALUE` | Preferred worker profile metadata. |
| `--group VALUE` | Grouping key for related tasks. |
| `--resource VALUE` | Resource key such as a repo or path lock target. Defaults to `repo:<git-root>` inside a git repo; use `none` to disable. |
| `--blocked-by ID|none` | Task dependency. |
| `--after ID` | Alias for `--blocked-by ID`. |

Priority applies only among ready tasks. It does not bypass dependencies,
manual blocks, resource locks, or non-pending status. Use `afk top <id>` to
promote a pending task ahead of peers with the same effective priority without
changing its priority metadata.

## Configuration

Queue database path resolution order:

1. `--queue <path>` flag
2. `AFK_QUEUE` environment variable
3. `~/.claude/queue/tasks.sqlite`

A `--queue`/`AFK_QUEUE` path with a non-`.sqlite` extension is normalized to a sibling `.sqlite` database with the same basename. For example, `--queue /tmp/tasks.jsonl` uses `/tmp/tasks.sqlite`.

## Command Reference

| Command | Behavior |
|---|---|
| `afk add <body>` | Append a new pending task and print its id. |
| `afk add --dry-run <body>` | Validate a task body and metadata without adding it. |
| `afk ls [--status STATUS] [--json]` | List tasks, optionally filtered by status. |
| `afk show <id> [--json]` | Show one task. |
| `afk count` | Tally tasks per status. |
| `afk next` | Show the task `afk pop` would claim without mutation. |
| `afk ready [--json]` | List pending tasks ready to run. |
| `afk why <id> [--json]` | Explain why a task is or is not ready. |
| `afk explain <id> [--json]` | Show task metadata, events, and attempts. |
| `afk deps add <id> --blocked-by <other-id>` | Add a dependency. |
| `afk deps rm <id> --blocked-by <other-id>` | Remove a dependency. |
| `afk deps ls <id> [--json]` | List dependencies. |
| `afk block <id> <reason>` | Manually block a pending task from scheduling. |
| `afk unblock <id>` | Remove a manual block. |
| `afk top <id>` | Promote a pending task ahead of peers with the same effective priority. |
| `afk pop [--lease DURATION] [--worker ID]` | Atomically claim the next ready task and print it as JSON. |
| `afk run --exec TEMPLATE [--limit N]` | Claim ready tasks and run a shell command template. |
| `afk heartbeat <id> --worker ID [--lease DURATION]` | Extend a worker-owned task lease. |
| `afk requeue-stale [--older-than DURATION]` | Reset stale `working` tasks to `pending`. |
| `afk done <id>` | Mark a task done. |
| `afk fail <id> <reason>` | Mark a task failed. |
| `afk retry <id>` | Reset a failed task to pending while preserving attempt history. |
| `afk reset <id>` | Set a task back to pending and clear started, finished, lease, and error fields. |
| `afk edit <id> <new-body>` | Replace a task body. |
| `afk rm <id>` | Remove one task. |
| `afk prune [--status LIST]` | Remove tasks by status. Defaults to `done,failed`. |
| `afk prompt [--output PATH]` | Generate loop instruction Markdown. |
| `afk prompt --task <id>` | Generate a focused prompt for one queued task. |
| `afk doctor` | Check queue health and installation basics. |
| `afk discover` | Print the task-discovery workflow stub without opening or creating the queue. |

## Architecture

- `cmd/afk`: process setup and signal-aware command execution.
- `internal/commands`: Cobra commands, argument parsing, and presentation wiring.
- `internal/app`: task use cases such as add, list, pop, run, done, fail, reset, and prune.
- `internal/store`: persistence boundary; the SQLite implementation owns atomic claim/update operations.
- `internal/task`: persisted task schema, statuses, metadata, events, attempts, dependencies, and blocks.
- `internal/runner`: built-in command-template runner.
- `internal/output`: human and JSON/JSONL CLI rendering.
- `internal/prompt`: generated loop instruction Markdown.
