# afk documentation

```sh
afk add "fix the failing queue test"
afk pop --lease 30m
# do the work
afk done <id>
```

`afk` is a local SQLite task queue for coding agents and shell workflows. Tasks live in `~/.claude/queue/tasks.sqlite`. Each task moves through four statuses — `pending`, `working`, `done`, `failed` — while the scheduler tracks dependencies, manual blocks, leases, and resource locks separately.

## Topics

| Doc | Covers |
|---|---|
| [getting-started.md](getting-started.md) | Install, queue a task, claim and finish it. |
| [tasks.md](tasks.md) | Add tasks, attach metadata, edit, remove, prune. |
| [scheduling.md](scheduling.md) | Dependencies, manual blocks, resource locks, readiness. |
| [workers.md](workers.md) | Claim work, hold leases, heartbeat, recover stale claims. |
| [runner.md](runner.md) | `afk run` worker loop and command templates. |
| [task-discovery.md](task-discovery.md) | Use `afk discover` and path-specific local probes to mine AFK-ready candidate tasks. |
| [configuration.md](configuration.md) | Queue path resolution and environment variables. |
| [command-reference.md](command-reference.md) | Every subcommand, one row each. |

## Lifecycle in one paragraph

You add a task, the scheduler decides when it is ready, a worker claims it with `afk pop` (or the built-in `afk run` loop), the worker executes the task body and reports back with `afk done` or `afk fail`. Dependencies, blocks, and leases gate the claim step. They never change the task status by themselves.
