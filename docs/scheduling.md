# scheduling

`afk take` claims only ready tasks. Readiness is separate from status.

A task is ready when:

- its status is `todo`
- all dependencies are `done`
- no other `doing` task holds the same non-empty resource key

Preview readiness:

```sh
afk take --dry-run --limit 10 --json
```

Use `--limit 0` to print every ready task. This is useful for duplicate checks
and discovery runs that need the whole ready set:

```sh
afk take --dry-run --limit 0 --json
```

Claim readiness:

```sh
afk take --lease 30m --worker codex:1
```

A blocked task remains `todo`; it simply will not be returned by `take` until its dependencies and resource locks clear.
When every visible `todo` task has the same non-empty resource key as an active
`doing` task, `afk take` returns no task even though `afk status` still shows
todo work. In that case stdout remains empty for worker-loop compatibility, and
stderr explains the active resource-lock blocker.

To replace a bad dependency shape, add a corrected task and mark the old one failed or deleted:

```sh
afk add --blocked-by 42 "corrected dependent task"
afk set 43 deleted "superseded by corrected dependency set"
```
