# command reference

```sh
afk --help
afk <command> --help
afk --version
```

The public command surface is intentionally small.

| Command | Behavior |
|---|---|
| `afk add <body...>` | Append a new `todo` task and print its id. |
| `afk add --dry-run [--json] <body...>` | Validate task shape and metadata without mutating the queue. |
| `afk tasks [--status STATUS] [--json]` | List tasks. Default hides `deleted`; use `--status deleted` or `--status all` when needed. |
| `afk task <id> [--json]` | Show one task with metadata, dependencies, events, and attempts. |
| `afk status [--summary] [--json]` | Print queue counts; without `--summary`, also includes `todo` and `doing` task lists. |
| `afk find <query> [--status STATUS] [--json]` | Search id, body, status, cwd, source, tags, resource, agent, group, and error text. |
| `afk take [--dry-run] [--limit N] [--lease DURATION] [--worker ID] [--json] [--summary]` | Preview or atomically claim the first ready task. |
| `afk set <id> <status> [note...] [--json]` | Move a task to `todo`, `doing`, `done`, `failed`, or `deleted`. |
| `afk snapshot [--label LABEL] [--task ID] [--output PATH]` | Export a read-only JSON evidence snapshot for before/after comparisons. |
| `afk prompt [--task ID] [--discover] [--output PATH]` | Emit LLM-agent instruction prompts. |
| `afk serve [--addr HOST:PORT]` | Start the local web UI and API. |

## take readiness

`afk take` claims only tasks that are ready. A `todo` task is not ready while it
has unfinished dependencies or while another `doing` task holds the same
non-empty resource key.

Preview ready tasks without claiming:

```sh
afk take --dry-run --limit 5 --json
```

Use `--limit 0` to print all currently ready tasks:

```sh
afk take --dry-run --limit 0 --json
```

When no task can be claimed, `afk take` keeps stdout empty so worker loops can
test for an empty claim. It writes a short explanation to stderr, including
active resource-lock blockers when they are the reason visible `todo` work is
not ready.

Use `--summary` to claim a task and include queue counts plus
`ready_remaining` after the claim:

```sh
afk take --summary
```

## targeted retry

Use `set <id> doing` when retrying one specific failed task. This opens a new
attempt, clears stale task-level error text, and keeps prior failed attempts in
history:

```sh
afk task "$id" --json
afk set "$id" doing "retrying after fixing the blocker"
# do the work
afk set "$id" done
```

`afk set <id> done` and `afk set <id> failed` always leave attempt history
coherent. If there is no open attempt, AFK records a synthetic terminal attempt
so direct manual finalization remains auditable.

## evidence snapshots

Use `afk snapshot` before and after task work when the verification asks for a
queue-state comparison. Snapshots are read-only JSON and include queue counts,
ready tasks, todo tasks, and doing tasks:

```sh
afk snapshot --label before --output before.json
afk snapshot --label after --task "$id" --output after.json
```

## replacement map

| Old behavior | New command |
|---|---|
| `afk ls` | `afk tasks` |
| `afk explain <id>` / `afk show <id>` | `afk task <id>` |
| `afk pop` | `afk take` |
| `afk ready` / `afk run --dry-run` | `afk take --dry-run` |
| `afk done <id>` | `afk set <id> done` |
| `afk fail <id> <reason>` | `afk set <id> failed <reason>` |
| `afk retry <id>` / `afk reset <id>` | `afk set <id> doing "retrying"` for a targeted retry, or `afk set <id> todo <note>` to return work to the ready queue. |
| `afk prune` / `afk rm` | `afk set <id> deleted` |
| `afk run` | External loop: `afk take`, execute the task, then `afk set`. |

## statuses

Canonical statuses are `todo`, `doing`, `done`, `failed`, and `deleted`.
Old stored values `pending` and `working` are migrated to `todo` and `doing`.
