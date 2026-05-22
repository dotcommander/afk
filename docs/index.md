# afk docs

`afk` is a local SQLite task queue for coding agents. The public lifecycle is:

```text
todo -> doing -> done | failed | deleted
```

Start here:

| Page | Topic |
|---|---|
| [getting-started.md](getting-started.md) | Add, inspect, claim, finish, serve. |
| [command-reference.md](command-reference.md) | Public commands and removed-command replacements. |
| [tasks.md](tasks.md) | Task metadata, search, deletion, and history. |
| [workers.md](workers.md) | Claiming and finalizing work. |
| [runner.md](runner.md) | How to replace the removed `afk run`. |
| [scheduling.md](scheduling.md) | Readiness, dependencies, and resource locks. |
| [configuration.md](configuration.md) | Queue path behavior. |
| [task-discovery.md](task-discovery.md) | Discovery prompt workflow. |
