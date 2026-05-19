# Changelog

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
