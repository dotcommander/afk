# scheduling

```sh
afk take --dry-run --limit 0 --json --full
afk status --blocked
afk tasks --status doing --json
```

Use those three commands when `afk status` shows `todo` work but workers are
not claiming anything.

Readiness is separate from status. `store.Ready` (SQL) is the single authority.
A task is ready only when all hold:

- its status is `todo`
- every `blocks` relation points to a `done` task
- no other `doing` task holds the same non-empty resource key
- it has no unsatisfied gate

Dependencies are `blocks` relations: `afk add --blocked-by <id>` creates one.

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

Record a typed relation between two tasks:

```sh
afk relate "$id" "$other" --type blocks
```

`--type` defaults to `blocks`. Only `blocks` edges gate readiness: a `blocks`
edge to a not-`done` task keeps the dependent out of the ready set. `relates`,
`duplicates`, and `parent` are informational links that never block. `afk add
--blocked-by <id>` is the shorthand for a `blocks` relation.

Hold a task on an external precondition with a gate:

```sh
afk add "publish the release"
afk gate add "$id" review-approved
afk take --dry-run --limit 0          # the task does not appear
afk gate satisfy "$id" review-approved
afk take --dry-run --limit 0          # now it appears
```

An unsatisfied gate keeps a task out of the ready set until you satisfy it. Use
gates to hold work on a precondition like a review or a CI run.

`--stage` is a free-form pipeline label, orthogonal to readiness:

```sh
afk add --stage triage "review the migration"
```

It is not a scheduling input; it does not affect readiness and is independent of
status.

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
