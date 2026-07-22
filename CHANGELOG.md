# Changelog

## v0.6.1 (2026-07-22)

### Fixes

- Harden worker-fencing rules and CLI help so worker identity and lifecycle guidance remain clear and safe.

### Other

- Complete post-v0.6.0 public documentation, cleanup, and regression-test coverage.
- Add polling discipline to the worker prompt: wait at least 60 seconds between read-only probes, poll summary counts only until state changes, and avoid timer loops.

## v0.4.1 (2026-05-22)

### Features

- Add `afk retry <id> --reason ...` as focused sugar for reopening failed tasks into a fresh attempt.

### Other

- Include retry guidance in AFK prompts and add a troubleshooting FAQ for queue workers.

## v0.4.0 (2026-05-22)

### Features

- Harden AFK attempt lifecycle semantics so targeted `set <id> doing` retries open a fresh attempt, terminal `set` operations keep attempt history coherent, and successful completion clears stale task-level errors.
- Add structured completion and context helpers: `afk set --json`, `afk take --summary`, and `afk snapshot` for durable before/after evidence.

## v0.3.6 (2026-05-22)

### Fixes

- Explain why `afk take` returned no claim when todo tasks are blocked by active resource locks.

## v0.3.5 (2026-05-22)

### Fixes

- Build installed binaries with the current git tag as `afk --version`.

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
