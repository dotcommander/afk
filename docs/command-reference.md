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
| `afk take [--dry-run] [--limit N] [--lease DURATION] [--worker ID] [--json]` | Preview or atomically claim the first ready task. |
| `afk set <id> <status> [note...]` | Move a task to `todo`, `doing`, `done`, `failed`, or `deleted`. |
| `afk prompt [--task ID] [--discover] [--output PATH]` | Emit LLM-agent instruction prompts. |
| `afk serve [--addr HOST:PORT]` | Start the local web UI and API. |

## replacement map

| Old behavior | New command |
|---|---|
| `afk ls` | `afk tasks` |
| `afk explain <id>` / `afk show <id>` | `afk task <id>` |
| `afk pop` | `afk take` |
| `afk ready` / `afk run --dry-run` | `afk take --dry-run` |
| `afk done <id>` | `afk set <id> done` |
| `afk fail <id> <reason>` | `afk set <id> failed <reason>` |
| `afk prune` / `afk rm` | `afk set <id> deleted` |
| `afk run` | External loop: `afk take`, execute the task, then `afk set`. |

## statuses

Canonical statuses are `todo`, `doing`, `done`, `failed`, and `deleted`.
Old stored values `pending` and `working` are migrated to `todo` and `doing`.
