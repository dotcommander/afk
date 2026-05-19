# Changelog

## v0.1.2 (2026-05-19)

### Features

- Add `afk add --diagnose` to run all generated-task validation checks without mutating the queue.

### Other

- Share fail-fast and diagnose validation checks so generated-task rules cannot drift.

## v0.1.1 (2026-05-19)

### Fixes

- Track the `cmd/afk` entrypoint so clean checkouts can build the CLI.
- Scope the ignored build artifact to `/afk`.

### Other

- Add sidecar persistence for rejected generated tasks.

## v0.1.0 (2026-05-19)

### Features

- Add priority-aware scheduling for `ready`, `next`, `pop`, and `run --dry-run`.
- Add `afk top` for promoting pending tasks within their effective priority.
- Add the `afk discover` workflow stub.

### Fixes

- Mark tasks failed after runner command cancellation using a cleanup context.

### Other

- Expand runner, store, task, output, and command test coverage.
- Update AFK command and scheduling documentation.
