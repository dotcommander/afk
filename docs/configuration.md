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

## legacy jsonl import

On first use, `afk` imports tasks from `~/.claude/queue/tasks.jsonl` into SQLite when the SQLite database is empty. JSONL is import-only — new writes go to SQLite. Hand-edits to the JSONL file are not replayed after the import completes.

If you pass `--queue <path>.jsonl`, `afk` treats it as the legacy path and stores the SQLite database next to it with the same basename:

```sh
afk --queue /tmp/tasks.jsonl ls
# imports /tmp/tasks.jsonl once into /tmp/tasks.sqlite
# subsequent commands write to /tmp/tasks.sqlite
```

## environment variables

| Variable | Purpose |
|---|---|
| `AFK_QUEUE` | Override the default queue path. Used by `afk` and by subprocesses spawned by `afk run`. |

`afk run` sets `AFK_QUEUE` for every `--exec` subprocess so nested `afk done`, `afk fail`, and `afk explain` calls hit the same queue.

## inspect the configuration

```sh
afk doctor
```

`afk doctor` reports:

- The resolved queue path.
- Whether the SQLite database exists and is writable.
- The schema version.
- Whether legacy JSONL was imported.
- Whether the binary is on `$PATH`.

Run `afk doctor` first when a command behaves unexpectedly. Most queue surprises trace back to a flag, env var, or stale `AFK_QUEUE` from a parent shell.

## moving the queue

`afk` does not ship a migration command. To move a queue:

```sh
cp ~/.claude/queue/tasks.sqlite /new/location/tasks.sqlite
export AFK_QUEUE=/new/location/tasks.sqlite
afk doctor
```

SQLite is portable across machines that share endianness. Copy `tasks.sqlite` and optionally `tasks.sqlite-wal` / `tasks.sqlite-shm` when the original process is still running.
