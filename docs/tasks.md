# tasks

```sh
afk add --tag repo:afk --priority high "review scheduler indexes"
afk show 1
afk edit 1 "review scheduler indexes and dependency cycle handling"
afk rm 1
```

## add a task

```sh
afk add "fix the failing queue test"
```

`afk add` appends a new task, sets its status to `pending`, and prints the new task id. The body may span multiple words; quote it to prevent shell splitting.

Validate a generated task without mutating the queue:

```sh
afk add --dry-run --source task-discovery --tag discovery --cwd /path/to/repo \
  "[discovery:repo:topic] Evidence: /path/to/repo/file.go:1. Scope: /path/to/repo/file.go. Fix one focused issue. Verify with go test ./..."
```

`--dry-run` applies the same validation as `afk add` but does not create a task.
Use it before enqueueing generated discovery candidates.

Attach metadata at creation time:

```sh
afk add \
  --tag repo:afk \
  --priority high \
  --source roadmap.md \
  --cwd /Users/you/code/afk \
  --agent claude \
  --group release-1 \
  "review scheduler indexes"
```

## metadata flags

| Flag | Behavior |
|---|---|
| `--tag VALUE` | Repeatable tag. Use for namespaced labels like `repo:afk`. |
| `--priority VALUE` | Scheduler priority. Recognized values are `urgent`, `high`, `normal`, and `low`; empty and unknown values schedule as normal. |
| `--source VALUE` | Origin of the task — `cli`, `roadmap.md`, `todo-scan`, `go-test`. |
| `--cwd PATH` | Working-directory context. Defaults to your current directory. |
| `--no-cwd` | Skip the cwd record for a context-free task. |
| `--agent VALUE` | Preferred worker profile metadata. |
| `--group VALUE` | Grouping key for related tasks. |
| `--resource VALUE` | Resource lock target (see [scheduling.md](scheduling.md)). |
| `--blocked-by ID\|none` | Task dependency (see [scheduling.md](scheduling.md)). |
| `--after ID` | Alias for `--blocked-by`. |
| `--dry-run` | Validate the task body and metadata without adding a task. |

`afk add` records the current working directory by default. Pass `--no-cwd` when the task should not carry that context.

Priority applies only among tasks that are already ready. It does not bypass
dependencies, manual blocks, resource locks, or non-pending status. `afk ls`
keeps insertion order for history/inspection; scheduler commands such as
`ready`, `next`, `pop`, and `run --dry-run` use priority order.

Promote an existing pending task ahead of peers with the same effective
priority:

```sh
afk top 42
```

`afk top` does not change the task's priority metadata. Use it when one pending
task should be handled before other tasks with the same priority rank.

## inspect tasks

```sh
afk ls
afk ls --status pending
afk ls --status working --json
afk show 1
afk show 1 --json
```

`afk ls` lists every task. Filter by status with `--status pending|working|done|failed`. Append `--json` for machine-readable output.

`afk show` returns one task with its metadata, dependencies, and current scheduler state.

For full history including events and per-attempt records:

```sh
afk explain 1
afk explain 1 --json
```

`afk explain` is the durable ledger. It is the right command when a task failed and you want to know why.

## tally the queue

```sh
afk count
```

Output:

```
pending: 3
working: 1
done: 12
failed: 0
```

## edit a task

```sh
afk edit 1 "review scheduler indexes and dependency cycle handling"
```

`afk edit` replaces the task body. It does not change metadata or status.

## remove tasks

Remove one task:

```sh
afk rm 1
```

Prune terminal tasks in bulk:

```sh
afk prune                       # default: removes done and failed
afk prune --status done
afk prune --status done,failed
```

`afk prune` only touches `done` and `failed` tasks unless you pass an explicit `--status` list.

## reset a task

```sh
afk reset 1
```

`reset` returns the task to `pending` and clears the `started`, `finished`, `lease`, and `error` fields. Use it when a task should run fresh without preserving the prior attempt. To return a failed task to `pending` while keeping attempt history, use `afk retry` instead.
