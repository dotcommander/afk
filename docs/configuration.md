# configuration

```sh
afk --queue /tmp/tasks.sqlite add "scratch task"
AFK_QUEUE=/tmp/tasks.sqlite afk ls
afk doctor
```

`afk` reads its queue from a single SQLite file. Configuration determines which file.

## queue path resolution

`afk` picks the queue database in this order:

1. `--queue <path>` flag on the command.
2. `AFK_QUEUE` environment variable.
3. `~/.claude/queue/tasks.sqlite` default.

The first option that resolves to a non-empty value wins.

```sh
afk --queue /tmp/tasks.sqlite count       # explicit flag
AFK_QUEUE=/tmp/tasks.sqlite afk count     # environment
afk count                                 # default
```

## queue path normalization

SQLite is the only queue backend. A `--queue`/`AFK_QUEUE` path with a non-`.sqlite` extension is normalized to a sibling `.sqlite` database with the same basename:

```sh
afk --queue /tmp/tasks.jsonl ls
# uses /tmp/tasks.sqlite
```

## environment variables

| Variable | Purpose |
|---|---|
| `AFK_QUEUE` | Override the default queue path. Used by `afk` and by subprocesses spawned by `afk run`. |
| `AFK_TASK_ID` | Set **by `afk run`** into the worker subprocess — the ID of the task being executed. |
| `AFK_TASK_BODY` | Set **by `afk run`** into the worker subprocess — the body text of the task being executed. |
| `AFK_TASK_CWD` | Set **by `afk run`** into the worker subprocess — the working directory of the task being executed. |

`afk run` sets `AFK_QUEUE` for every `--exec` subprocess so nested `afk done`, `afk fail`, and `afk explain` calls hit the same queue. It also sets `AFK_TASK_ID`, `AFK_TASK_BODY`, and `AFK_TASK_CWD` so worker commands can read task fields from the environment without relying on the `{{id}}`/`{{body}}`/`{{cwd}}` template placeholders.

## inspect the configuration

```sh
afk doctor
```

`afk doctor` reports:

- The resolved queue path.
- Whether the SQLite database exists.
- Pending, working, done, and failed task counts.
- A warning when working tasks are present.
- The running binary path.
- Prompt health.

Run `afk doctor` first when a command behaves unexpectedly. Most queue surprises trace back to a flag, env var, or stale `AFK_QUEUE` from a parent shell.

## moving the queue

`afk` does not ship a migration command. To move a queue:

```sh
cp ~/.claude/queue/tasks.sqlite /new/location/tasks.sqlite
export AFK_QUEUE=/new/location/tasks.sqlite
afk doctor
```

SQLite is portable across machines that share endianness. Copy `tasks.sqlite` and optionally `tasks.sqlite-wal` / `tasks.sqlite-shm` when the original process is still running.
