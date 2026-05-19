# scheduling

```sh
schema=$(afk add "add migration")
tests=$(afk add --blocked-by "$schema" "update migration tests")
afk ready
afk why "$tests"
```

The scheduler decides when a `pending` task is *ready* — claimable by `afk pop` or `afk run`. Dependencies, manual blocks, and resource locks all gate readiness. None of them change the task status; status stays one of `pending`, `working`, `done`, `failed`.

## dependencies

Mark one task as blocked by another:

```sh
schema=$(afk add "add migration")
afk add --blocked-by "$schema" "update migration tests"
```

`--after ID` is an alias for `--blocked-by ID`. Use `--blocked-by none` to record no dependency explicitly.

The dependent task stays `pending` and is excluded from `afk ready` until the prerequisite is `done`.

Add or remove dependencies after creation:

```sh
afk deps add 43 --blocked-by 42
afk deps rm 43 --blocked-by 42
afk deps ls 43
afk deps ls 43 --json
```

Rules:

- Missing dependency ids are rejected at write time.
- Self-dependencies are rejected.
- Dependency cycles are rejected.
- A `failed` prerequisite does not auto-fail dependents. `afk why` reports the failed prerequisite so you can decide whether to retry, reset, or block.

## manual blocks

Pause a task without changing its status:

```sh
afk block 43 "waiting on API credentials"
afk why 43
afk unblock 43
```

A blocked task stays `pending` but is excluded from `afk ready`. Manual blocks survive across worker restarts. They clear only when you run `afk unblock`.

## resource locks

Use `--resource` when two tasks must not run concurrently against the same target:

```sh
afk add --resource repo:/Users/you/code/afk "edit package A"
afk add --resource repo:/Users/you/code/afk "edit package B"
```

When one task with that resource key is `working`, the other task with the same key is not ready. The lock releases when the active claim completes, fails, resets, or its lease expires.

Resource keys are free-form. Use stable strings like `repo:<path>`, `service:<name>`, or `db:<host>`.

## readiness inspection

```sh
afk ready
afk ready --json
afk why 43
afk why 43 --json
```

`afk ready` lists `pending` tasks the scheduler will claim next. `afk why` explains the gate state for one task — whether it is ready, blocked by a dependency, manually blocked, waiting on a resource lock, or held by a lease.

Readiness applies consistently to:

- `afk ready` — listing
- `afk why <id>` — explanation
- `afk next` — preview
- `afk pop` — claim
- `afk run` — runner loop claim

You will never see `afk pop` claim a task that `afk ready` would not list, and you will never see `afk ready` list a task that `afk why` says is blocked.
