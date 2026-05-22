# afk

`afk` is a local SQLite task queue for coding agents. The CLI is organized like a small task API:

```sh
id=$(afk add "fix the failing queue test")
afk tasks
afk task "$id"
afk take --dry-run --limit 5 --json
afk take --lease 30m --worker codex:1
afk set "$id" done
```

Tasks move through `todo`, `doing`, `done`, `failed`, and `deleted`. Scheduler state such as dependencies, leases, and resource locks is stored separately.

## core commands

| Command | Purpose |
|---|---|
| `afk add <body...>` | Add a task. Use `--dry-run --json` to validate without writing. |
| `afk tasks [--status STATUS] [--json]` | List tasks. Deleted tasks are hidden unless requested. |
| `afk task <id> [--json]` | Show one full task with events and attempts. |
| `afk status [--summary] [--json]` | Get queue counts, plus active task lists by default. |
| `afk find <query> [--json]` | Search task text and metadata for duplicate checks. |
| `afk take [--dry-run] [--lease DURATION] [--worker ID]` | Preview or claim ready work. |
| `afk set <id> <status> [note...]` | Set `todo`, `doing`, `done`, `failed`, or `deleted`. |
| `afk prompt [--task ID]` | Generate LLM-agent instructions. |
| `afk serve` | Run the web visibility layer. |

## removed command replacements

- `ls` -> `tasks`
- `explain` / `show` -> `task <id>`
- `pop` -> `take`
- `ready` / `run --dry-run` -> `take --dry-run`
- `done` -> `set <id> done`
- `fail` -> `set <id> failed <reason>`
- `prune` / `rm` -> `set <id> deleted`
- `run` -> a shell or agent loop that calls `take`, executes the task, then calls `set`

Example worker loop:

```sh
task_json=$(afk take --lease 30m --worker "$USER:$$")
test -n "$task_json" || exit 0
id=$(printf '%s\n' "$task_json" | jq -r .id)
body=$(printf '%s\n' "$task_json" | jq -r .body)

if agent-command "$body"; then
  afk set "$id" done
else
  afk set "$id" failed "agent-command failed"
fi
```

## docs

- [`docs/command-reference.md`](docs/command-reference.md)
- [`docs/getting-started.md`](docs/getting-started.md)
- [`docs/tasks.md`](docs/tasks.md)
- [`docs/scheduling.md`](docs/scheduling.md)
- [`docs/workers.md`](docs/workers.md)
- [`docs/configuration.md`](docs/configuration.md)
