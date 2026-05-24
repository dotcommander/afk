# scheduling

```sh
afk take --dry-run --limit 0 --json --full
afk status --blocked
afk tasks --status doing --json
```

Use those three commands when `afk status` shows `todo` work but workers are
not claiming anything.

Readiness is separate from status. A task is ready only when:

- its status is `todo`
- every dependency is `done`
- no other `doing` task holds the same non-empty resource key

Preview claimable work without mutating the queue:

```sh
afk take --dry-run --limit 10 --json --full
```

Use `--limit 0` to print every ready task. This is useful for duplicate checks
and discovery runs that need the whole ready set:

```sh
afk take --dry-run --limit 0 --json --full
```

Explain dependency-blocked `todo` tasks:

```sh
afk status --blocked
```

`--blocked` reports only unfinished dependency blockers. It does not list
resource-lock blockers because resource locks are held by active `doing` tasks.
Inspect those separately:

```sh
afk tasks --status doing --json
```

Claim one ready task:

```sh
afk take --lease 30m --worker codex:1 --summary
```

The returned task moves to `doing`. Status and snapshot output then derive
claim age from the task's `started` timestamp. If the lease expires, status JSON
includes:

```json
"claim": {"age_seconds": 1860, "stale": true, "reason": "lease_expired"}
```

If no lease was set, a task is treated as stale after one hour with
`reason=unleased_age`. Stale claim diagnostics are read-only; AFK does not
silently requeue or fail the task.

A blocked task remains `todo`; it simply will not be returned by `take` until
its dependencies and resource locks clear. When no task can be claimed, `afk
take` keeps stdout empty for worker-loop compatibility and writes a short
explanation to stderr.

To replace a bad dependency shape, add a corrected task and mark the old one failed or deleted:

```sh
afk add --blocked-by 42 "corrected dependent task"
afk set 43 deleted --note "superseded by corrected dependency set"
```
