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

Claim readiness:

```sh
afk take --lease 30m --worker codex:1
```

A blocked task remains `todo`; it simply will not be returned by `take` until its dependencies and resource locks clear.

To replace a bad dependency shape, add a corrected task and mark the old one failed or deleted:

```sh
afk add --blocked-by 42 "corrected dependent task"
afk set 43 deleted "superseded by corrected dependency set"
```
