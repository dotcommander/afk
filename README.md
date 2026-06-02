# afk

`afk` is a local SQLite task queue for coding agents. The CLI is organized like a small task API:

```sh
id=$(afk add "fix the failing queue test")
afk tasks
afk task "$id"
afk take --dry-run --limit 5 --json --full
afk status --blocked
afk take --lease 30m --worker codex:1 --summary
afk set "$id" done --note "verified" --summary
```

Tasks move through `todo`, `doing`, `done`, `failed`, and `deleted`.
Readiness is narrower than `todo`: a task is claimable only when every
`blocks` relation is `done`, no other active `doing` task holds its resource
key, and no gate is unsatisfied.
`afk status --blocked` explains dependency blockers; `doing` task status output
also shows claim age and stale lease diagnostics.

`--stage` is a free-form human pipeline state (e.g. `triage`, `in-review`),
independent of the five execution states; it does not affect readiness.

## core commands

| Command | Purpose |
|---|---|
| `afk add <body...> [--stage VALUE]` | Add a task. Use `--dry-run --json` to validate without writing. |
| `afk tasks [--status STATUS] [--stage VALUE] [--json]` | List tasks. Deleted tasks are hidden unless requested. `--stage` filters by pipeline state. |
| `afk task <id> [--json]` | Show one full task with events and attempts. |
| `afk status [--summary] [--blocked] [--json]` | Get queue counts, plus active task lists by default. `--blocked` explains dependency-blocked todo tasks. |
| `afk find <query> [--json]` | Search task text and metadata for duplicate checks. |
| `afk take [--dry-run] [--lease DURATION] [--worker ID] [--summary] [--full] [--envelope]` | Preview or claim ready work. |
| `afk set <id> <status> [note...] [--note TEXT] [--note-file PATH|-] [--stage VALUE] [--json] [--summary]` | Set `todo`, `doing`, `done`, `failed`, or `deleted`. |
| `afk retry <id> [--reason TEXT] [--json]` | Open a new attempt for a failed task. |
| `afk relate <task-id> <related-id> [--type blocks\|relates\|duplicates\|parent]` | Record a typed relation between tasks. Defaults to `blocks`. Only `blocks` edges gate readiness; `relates`/`duplicates`/`parent` are informational. |
| `afk gate add <id> <name>` / `afk gate satisfy <id> <name>` | Named boolean preconditions. A task with any unsatisfied gate is not claimable until every gate is satisfied. Satisfy is one-way. |
| `afk snapshot [--label LABEL] [--task ID] [--output PATH]` | Export read-only JSON evidence for before/after comparisons. |
| `afk prompt [--task ID]` | Generate LLM-agent instructions. |
| `afk serve` | Run the web visibility layer. |

Useful triage sequence when workers claim nothing:

```sh
afk status --blocked
afk tasks --status doing --json
afk take --dry-run --limit 0 --json --full
```

## removed command replacements

- `ls` -> `tasks`
- `explain` / `show` -> `task <id>`
- `pop` -> `take`
- `ready` / `run --dry-run` -> `take --dry-run`
- `done` -> `set <id> done --note <evidence>`
- `fail` -> `set <id> failed --note <reason>`
- `retry` -> `retry <id> --reason <reason>`
- `reset` -> `set <id> doing "retrying"` for targeted retry, or `set <id> todo <note>` to return work to the ready queue
- `prune` / `rm` -> `set <id> deleted`
- `run` -> a shell or agent loop that calls `take`, executes the task, then calls `set`

Example worker loop:

```sh
task_json=$(afk take --lease 30m --worker "$USER:$$" --summary)
test -n "$task_json" || exit 0
id=$(printf '%s\n' "$task_json" | jq -r .task.id)
body=$(printf '%s\n' "$task_json" | jq -r .task.body)

if agent-command "$body"; then
  afk set "$id" done --note "agent-command completed" --summary
else
  afk set "$id" failed --note "agent-command failed" --summary
fi
```

## docs

- [`docs/command-reference.md`](docs/command-reference.md)
- [`docs/faq.md`](docs/faq.md)
- [`docs/getting-started.md`](docs/getting-started.md)
- [`docs/tasks.md`](docs/tasks.md)
- [`docs/scheduling.md`](docs/scheduling.md)
- [`docs/workers.md`](docs/workers.md)
- [`docs/configuration.md`](docs/configuration.md)
