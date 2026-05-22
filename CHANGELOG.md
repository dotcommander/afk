# Changelog

## v0.3.4 (2026-05-22)

### Fixes

- Treat `afk take --dry-run --limit 0` as an unbounded ready-task preview.

### Other

- Clarify `take` readiness, resource locks, and `--limit 0` behavior in docs.

## v0.3.3 (2026-05-22)

### Other

- Simplify the public CLI around `add`, `tasks`, `task`, `status`, `find`, `take`, `set`, `prompt`, and `serve`.
- Remove legacy queue commands and document replacements for external worker loops.
- Extract AFK loop and task prompts into embedded templates.
- Expand regression coverage for the trimmed command surface.

## v0.3.0 (2026-05-21)

### Features

- Add newest-first dashboard roster ordering, 100-per-page pagination, and panel overflow fixes.
- Wrap long task bodies in home-page dashboard panels.

### Other

- Clarify path-based task discovery guidance and generated candidate validation docs.
- Support Windows runner and SQLite DSN handling.
- Remove legacy `~/.claude/queue/tasks.jsonl` import support. SQLite is the only queue backend; a non-`.sqlite` queue path is normalized to a sibling `.sqlite` database.

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
