# command reference

```sh
afk --help
afk <command> --help
afk --version
```

Every `afk` subcommand. Group by intent; follow the per-topic doc when you need detail.

## task lifecycle

| Command | Behavior |
|---|---|
| `afk add <body...>` | Append a new pending task and print its id. |
| `afk add --dry-run <body...>` | Validate a task body and metadata without adding it. |
| `afk show <id>` | Show one task. Add `--json` for machine output. |
| `afk ls` | List tasks. Filter with `--status pending\|working\|done\|failed`. Add `--json`. |
| `afk status` | Print per-status tallies, plus pending and working task lists. |
| `afk edit <id> <new-body>` | Replace a task body. |
| `afk rm <id>` | Remove one task. |
| `afk prune` | Remove `done` and `failed` tasks. Override with `--status <list>`. |

## scheduling

| Command | Behavior |
|---|---|
| `afk next` | Print the first ready task without claiming it. |
| `afk ready` | List pending tasks ready to run. Add `--json`. |
| `afk why <id>` | Explain a task's readiness gate state. Add `--json`. |
| `afk deps add <id> --blocked-by <other-id>` | Add a dependency. |
| `afk deps rm <id> --blocked-by <other-id>` | Remove a dependency. |
| `afk deps ls <id>` | List dependencies. Add `--json`. |
| `afk block <id> <reason>` | Manually block a pending task. |
| `afk unblock <id>` | Remove a manual block. |
| `afk top <id>` | Promote a pending task ahead of peers with the same effective priority. |

See [scheduling.md](scheduling.md).

## worker actions

| Command | Behavior |
|---|---|
| `afk pop` | Claim the first ready task and print it as JSON. Flags: `--lease DURATION`, `--worker ID`. |
| `afk run --exec TEMPLATE` | Claim ready tasks and run a shell command per claim. See [runner.md](runner.md). |
| `afk heartbeat <id> --worker ID` | Extend a worker-owned lease. Flag: `--lease DURATION`. |
| `afk done <id>` | Mark a task done. |
| `afk fail <id> <reason>` | Mark a task failed with a reason. |
| `afk retry <id>` | Reset a failed task to pending while preserving attempt history. |
| `afk reset <id>` | Return a task to pending and clear started, finished, lease, error. |
| `afk requeue-stale` | Reset stale working tasks to pending. Flag: `--older-than DURATION`. |

See [workers.md](workers.md).

## inspection

| Command | Behavior |
|---|---|
| `afk explain <id>` | Show task metadata, events, and attempts. Add `--json`. |
| `afk doctor` | Check queue health, queue path, and binary install. |
| `afk discover` | Print the task-discovery workflow stub without opening or creating the queue. |

## prompts

| Command | Behavior |
|---|---|
| `afk prompt` | Emit the loop instructions for Claude Code as Markdown. |
| `afk prompt --task <id>` | Emit a focused prompt for one queued task. |
| `afk prompt --output PATH` | Write the prompt to a file instead of stdout. |

## global flags

| Flag | Behavior |
|---|---|
| `--queue PATH` | Override the queue database path for this invocation. |
| `--version` (`-v`) | Print the binary version. |
| `--help` (`-h`) | Print help for the command. |

## metadata flags (`afk add`)

| Flag | Behavior |
|---|---|
| `--tag VALUE` | Repeatable tag. Supplying any tag disables the inferred repo tag. |
| `--priority VALUE` | Scheduler priority: `urgent`, `high`, `normal`, or `low`. Unknown values are rejected. |
| `--cwd PATH` | Working-directory context. Defaults to the current directory. |
| `--no-cwd` | Do not record a working directory or infer repo context. |
| `--source VALUE` | Origin. Defaults to `cli`; examples: `roadmap.md`, `todo-scan`. |
| `--agent VALUE` | Preferred worker profile metadata. |
| `--group VALUE` | Grouping key for related tasks. |
| `--resource VALUE` | Resource key for lock arbitration. Defaults to `repo:<git-root>` inside a git repo; use `none` to disable. |
| `--blocked-by ID\|none` | Task dependency. |
| `--after ID` | Alias for `--blocked-by`. |
| `--dry-run` | Validate without mutating the queue. |

See [tasks.md](tasks.md).
