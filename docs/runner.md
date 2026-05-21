# runner

```sh
afk run --dry-run --exec 'afk prompt --task {{id}}'
afk run --exec 'afk prompt --task {{id}}' --limit 1 --lease 30m
```

`afk run` is the built-in worker loop. It claims ready tasks through the same atomic scheduler path as `afk pop`, then runs a user-provided shell command template for each claim.

## preview before running

Use `--dry-run` to see what `afk run` would claim without mutating the queue:

```sh
afk run --dry-run --exec 'afk prompt --task {{id}}'
```

`--dry-run` also prints waiting tasks with the reason each one is held — dependency, manual block, or resource lock. It is the fastest way to debug a queue that is not draining.

## run one task

```sh
afk run --exec 'afk prompt --task {{id}}'
```

The runner claims the next ready task, substitutes `{{id}}` in the command, runs the command in a shell, and exits after one task (the default `--limit` is 1).

## run a bounded batch

```sh
afk run --exec 'afk prompt --task {{id}}' --limit 5 --max-minutes 30
```

The runner keeps claiming tasks until it processes five tasks or thirty minutes elapse, whichever comes first. `--limit 0` means no limit. `--max-minutes 0` means no time limit.

## flags

| Flag | Default | Behavior |
|---|---:|---|
| `--exec TEMPLATE` | empty | Shell command for each claimed task. Required unless `--dry-run` is set. |
| `--dry-run` | `false` | Print runnable tasks and waiting reasons without claiming. |
| `--limit N` | `1` | Maximum tasks to process. `0` means no limit. |
| `--max-minutes N` | `0` | Stop claiming after this many minutes. `0` means no time limit. |
| `--lease DURATION` | `30m` | Lease duration for each claim and heartbeat. |
| `--worker ID` | `hostname:pid` | Worker id recorded on attempts and heartbeats. |
| `--poison-guard` | `false` | Before each claim, block any ready task that has already failed 3 times so a repeatedly failing ("poison") task cannot stall the run. |
| `--workers N` | `1` | Reserved for parallel runners. Only `1` is supported. |

## template variables

| Variable | Value |
|---|---|
| `{{id}}` | Task id. |
| `{{cwd}}` | Task working-directory metadata. |
| `{{body}}` | Task body. |
| `{{queue}}` | Resolved SQLite queue path. |

The runner also exports `AFK_QUEUE` to the subprocess so nested `afk` commands operate on the same queue.

## close every claim

The runner does not infer success from the exit code of your command. If the command exits while the task is still `working`, `afk run` marks the task `failed`. Worker commands must close the loop themselves:

```sh
afk run --exec 'do-the-work {{id}} && afk done {{id}} || afk fail {{id}} "do-the-work crashed"'
```

This contract matches the manual `afk pop` / `afk done` cycle. The runner is a scheduler, not a wrapper that decides whether your code worked.

## claude code loop

Pair `afk run` with `afk prompt` to drive a Claude Code session that picks one queued task per tick:

```sh
afk prompt
/loop 15m "run ! afk prompt"
```

`afk prompt` emits the queue contract as Markdown — the same instructions Claude follows when claiming and finalizing work. To focus a loop on a single task, use `afk prompt --task <id>`.

## parallel workers

`--workers N` for `N > 1` is reserved for a future release and is rejected today. Run multiple processes against the same queue when you need parallelism; each will claim distinct tasks through the atomic pop path.
