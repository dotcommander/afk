# AFK Discovery Tasks

Review depth: level-2
Classification: repo
Target: `/Users/vampire/go/src/afk`

## Review Signals

- Local guidance and manifests inspected: `CLAUDE.md`, `README.md`, `go.mod`, and `justfile`.
- Recent churn and current worktree state show a broad uncommitted AFK edit set touching discovery guidance, command tests, queue operations, runner behavior, server actions, and SQLite storage.
- Current queue already has one todo AFK repo task for `repo:/Users/vampire/go/src/afk` about extracting import-validation helpers.
- An initial `go test ./...` run reported a discover-stub expectation mismatch, but a current rerun of `go test ./...` passes all packages, so that stale/transient output was not promoted.

## Primary Surfaces

- CLI entrypoint and commands: `/Users/vampire/go/src/afk/cmd/afk`, `/Users/vampire/go/src/afk/internal/commands`
- Discovery command contract: `/Users/vampire/go/src/afk/internal/commands/discover.go`, `/Users/vampire/go/src/afk/internal/commands/discover_stub.md`, `/Users/vampire/go/src/afk/internal/commands/commands_test.go`
- Task validation and queue behavior: `/Users/vampire/go/src/afk/internal/task`, `/Users/vampire/go/src/afk/internal/store`

## Files Inspected

- `/Users/vampire/go/src/afk/CLAUDE.md`
- `/Users/vampire/go/src/afk/README.md`
- `/Users/vampire/go/src/afk/go.mod`
- `/Users/vampire/go/src/afk/justfile`
- `/Users/vampire/go/src/afk/internal/commands/commands_test.go`
- `/Users/vampire/go/src/afk/internal/commands/discover_stub.md`
- `/Users/vampire/go/src/afk/internal/commands/discover.go`
- `/Users/vampire/go/src/afk/internal/task/validation.go`

## Probes Run

- `git status --porcelain=v2`
- `git diff --stat HEAD`
- `git log --name-only --pretty=format: -n 30 | sort | uniq -c | sort -rn | head -20`
- `rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" --glob '!vendor/**' --glob '!node_modules/**'`
- `go test ./...` initially failed in `internal/commands`; rerun passed all packages.
- Queue duplicate probes from `/Users/vampire/go/src`: `afk status`, `afk take --dry-run --json`, `afk tasks --status failed --json`, and `afk tasks --status doing --json`

## Rejected Leads

- Import-validation helper extraction: rejected here as an accepted duplicate already todo in AFK queue id `1779394084` for `repo:/Users/vampire/go/src/afk`.
- Initial discover-stub test mismatch: rejected because the current rerun of `go test ./...` passes all packages.
- TODO/discovery marker text in docs and stub: rejected as standalone leads because no current failing command remains after rerun.

## Candidate Tasks

No strong candidate.

## Validation Status

- No `afk add --dry-run` was retained because the only new candidate lead did not survive current verification.
