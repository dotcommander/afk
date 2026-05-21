# workers

```sh
afk pop --lease 30m --worker codex:1
# do the work
afk done 1
```

A worker is anything that claims a task with `afk pop`, executes its body, and reports the outcome. Workers may be humans, shell scripts, the built-in `afk run` loop, or a coding agent.

## claim a task

```sh
afk pop --lease 30m --worker codex:1
```

`afk pop` atomically:

1. Picks the first ready task (see [scheduling.md](scheduling.md)).
2. Moves it to `working`.
3. Records the worker id, the lease expiration, and a new attempt.
4. Prints the claimed task as JSON.

If no task is ready, `afk pop` exits without claiming anything. Use `afk ready --limit 1` first to preview without mutation.

## leases

A lease is a deadline. If the worker that holds the claim disappears, the lease expires and the task becomes recoverable.

```sh
afk pop --lease 30m --worker codex:1
afk heartbeat 1 --worker codex:1 --lease 30m
```

`afk heartbeat` extends the lease, but only when the worker id matches the active attempt. A different worker cannot steal an active claim.

The default lease is 30 minutes. Pick a duration that exceeds your typical task runtime; an over-aggressive lease will cause `afk requeue-stale` to claw back work that is still in progress.

## finish a claim

Every claim ends in exactly one terminal action:

```sh
afk done 1
afk fail 1 "missing credentials"
```

- `afk done` marks the task complete. The worker is free.
- `afk fail` records the failure reason on the current attempt and moves the task to `failed`.

`afk fail` does not delete history. The full attempt log is available with `afk explain 1`.

## retry failed work

```sh
afk retry 1
```

`afk retry` resets a `failed` task to `pending` and preserves every prior attempt in `afk explain`. Use it after fixing the underlying cause.

`afk reset 1` is the heavier alternative — it returns the task to `pending` and clears `started`, `finished`, `lease`, and `error`. Reach for `reset` only when you want the task to look untouched.

## recover stale claims

When a worker crashes or its host disappears, the claim sits in `working` past its lease. Recover it:

```sh
afk requeue-stale --older-than 2h
```

`afk requeue-stale` returns expired `working` tasks to `pending`. Tasks whose lease has not yet elapsed are left alone. The `--older-than` value is a Go duration string (`30m`, `2h`, `90s`).

Schedule this on a cron or run it from your shell prompt when a long-lived agent goes silent.

## inspect attempts

```sh
afk explain 1
afk explain 1 --json
```

`afk explain` shows the task body, metadata, every lifecycle event, and every attempt with its worker id, start time, finish time, and failure reason. It is the right command before you decide whether to retry or reset.
